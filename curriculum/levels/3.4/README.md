# 3.4 · Deadlines

> A call that can wait forever will, eventually, wait forever. Give every one a way out.

## Your goal

Make every `Client` call obey its `context.Context`. Right now a method checks the context once, before sending, and then blocks in `ReadFrame` for as long as the server takes. If the server is slow or silent, the caller is stuck, and so is every other goroutine waiting on the client's mutex. After this level a context with a deadline makes the call give up on time, cancelling a context cuts a call short mid-read, and either way the caller gets back `context.DeadlineExceeded` or `context.Canceled`, the same errors it would get from a local store.

There is a second half to giving up. If a call stops waiting, the server's reply is still on its way. Reuse the connection and the next caller reads that stale reply as if it were its own. So after any failure mid-call the client throws the connection away and dials a fresh one on the next call. No new API: `burrowctl -timeout` now actually works.

## What you're using from before

**Frame client** (3.3 · Remote Store). All of this goes inside the one unexported `call` helper that every method already goes through, and `Dial`. The mutex you hold for the whole call is what makes "close the connection and set it to nil" safe to do from inside it: no other goroutine is looking at `c.conn` at that moment.

```go
func (c *Client) call(ctx context.Context, req wire.Frame) (
	wire.Frame, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// before: write, read, return
	// now: dial if needed, arm the deadline, write, read, disarm,
	//      and on any error decide what to tell the caller
}
```

## What's new here

**Deadlines on a connection.** A blocked `Read` on a `net.Conn` has no timeout by itself. `SetDeadline(t)` gives it one: any `Read` or `Write` still in progress at time `t` fails. The deadline is absolute, not a duration, and it stays set until you change it, so clear it when the call is over:

```go
if deadline, ok := ctx.Deadline(); ok {
	conn.SetDeadline(deadline)
}
// ... write and read ...
conn.SetDeadline(time.Time{}) // the zero time means no deadline
```

Setting a deadline in the past is legal, and useful: it makes a `Read` that is already blocked fail right now.

**Recognising a timeout.** When the deadline hits, the read fails with an error that satisfies the `net.Error` interface and whose `Timeout()` method returns true. `errors.As` pulls it out of whatever wrapped it:

```go
var ne net.Error
if errors.As(err, &ne) && ne.Timeout() {
	// the deadline fired
}
```

**`context.AfterFunc`: reacting to a cancel.** A deadline covers `context.WithTimeout`, because that context has a `Deadline()`. It does nothing for `context.WithCancel`, whose cancel can come at any moment. `context.AfterFunc(ctx, f)` runs `f` in its own goroutine the instant `ctx` is done, and returns a `stop` function that unregisters `f` if the call finishes first:

```go
stop := context.AfterFunc(ctx, func() {
	// a time long past: unblock Read now
	conn.SetDeadline(time.Unix(1, 0))
})
defer stop()
```

This is the Go 1.21 replacement for starting a goroutine per call that selects on `ctx.Done()`.

**Report the context's error, not the socket's.** After a failed read, the caller wants to know *why*. If the context is done, that is the reason, whatever the socket said:

```go
if ctx.Err() != nil {
	return wire.Frame{}, ctx.Err() // DeadlineExceeded or Canceled
}
return wire.Frame{}, err // a real network error
```

Now `errors.Is(err, context.DeadlineExceeded)` works for the caller even though the error was born three layers down in the `net` package.

**A small connection state machine.** The client's connection is now in one of two states, and every call moves it:

```text
  no connection ---- call: dial ok ----> connected
       ^                                    |
       |                                    | call failed mid-way
       +------- close conn, conn = nil <----+
```

`Dial` puts it in the connected state. Any error inside `call` puts it back to "no connection": close the socket, set the field to nil. The next `call` sees nil and dials again before sending. The same thing covers a server that hangs up: the read fails, the connection is dropped, the following call redials.

**`Dial` with a context.** `net.Dialer.DialContext` already gives up when its context does. Check `ctx.Err()` first so an already-cancelled context never even tries to connect.

## What to do

1. In `Dial`, return `ctx.Err()` if the context is already done, before dialling. Keep using `net.Dialer.DialContext`.
2. In `call`, after taking the lock:
   - If `c.conn` is nil (dropped by an earlier failure), dial again with `ctx`. Remember the address from `Dial`.
   - If `ctx` has a deadline, `SetDeadline` it on the connection.
   - Register `context.AfterFunc(ctx, ...)` that sets a deadline in the past; `defer stop()`.
   - `WriteFrame`, `ReadFrame`.
   - On success, clear the deadline and return the reply.
   - On any error: close the connection, set `c.conn = nil`, and return `ctx.Err()` if it is non-nil, otherwise the error you got.
3. In `burrowctl`, make `-timeout` a `context.WithTimeout` around the one command. When the error matches `context.DeadlineExceeded`, print a message containing `timeout` to stderr and exit 1.
4. Run `gofmt -l .` and `go vet ./...` until both print nothing.

## Example

Against a server that takes five seconds to answer:

```go
bg := context.Background()
ctx, cancel := context.WithTimeout(bg, 50*time.Millisecond)
defer cancel()
start := time.Now()
_, err := c.Get(ctx, "slow")
// time.Since(start) is about 50ms, not 5s
// errors.Is(err, context.DeadlineExceeded) == true

ctx2, cancel2 := context.WithCancel(bg)
time.AfterFunc(50*time.Millisecond, cancel2)
_, err = c.Get(ctx2, "slow")
// errors.Is(err, context.Canceled) == true, again after about 50ms

e, err := c.Get(context.Background(), "fast")
// the client redialled; e is the reply to *this* request,
// not the late reply to "slow"
```

From the shell:

```bash
$ burrowctl -timeout 50ms get k        # server never replies
get k: timeout after 50ms
$ echo $?
1
```

## Done when

- [ ] A `Get` with a 50 ms deadline against a 5 s server returns within a second with `context.DeadlineExceeded`
- [ ] Cancelling the context 50 ms into a call returns `context.Canceled` within a second
- [ ] After a timed-out call, the next call gets its own reply, never the late one, and the server sees a second connection
- [ ] If the server hangs up mid-call, that call errors and the next one redials and works
- [ ] `Dial` with an already-cancelled context fails with `context.Canceled` without connecting; `Dial` to an unroutable address gives up at the context deadline
- [ ] 100 timed-out calls leave no goroutines behind
- [ ] `burrowctl -timeout 50ms` against a silent server exits 1 within 2 s and says `timeout`
- [ ] `gofmt -l .` and `go vet ./...` print nothing

## Reference

The exact contract the tests hold you to.

```go
// No new exported API. Behaviour added to every Client method:
//   - a call past ctx's deadline returns within a second with an error
//     matching context.DeadlineExceeded
//   - cancelling ctx mid-call returns context.Canceled promptly
//   - after any mid-call error the connection is closed and the next
//     call redials
//   - Dial honours its ctx
// burrowctl's -timeout flag now actually bounds each command.
```

| Situation | The call returns |
|---|---|
| Context deadline passes mid-call | an error matching `context.DeadlineExceeded` |
| Context cancelled mid-call | an error matching `context.Canceled` |
| Server hangs up mid-call | the network error |
| Any of the above | and the connection is dropped; the next call redials |

Decisions the tests rely on:

- After a mid-call failure the client returns `ctx.Err()` if the context is done, otherwise the network error. Redialing is lazy: the next call does it.

## The tests check

- A fake server that sleeps 5 s: Get with a 50 ms deadline returns within 1 s with errors.Is(err, context.DeadlineExceeded)
- Cancel after 50 ms mid-call: returns within 1 s with context.Canceled
- No stale replies: the first call times out, its reply arrives 300 ms later; the next call must get its own value and the fake server has seen a second connection
- The server hangs up mid-call: that call errors, the next call redials and succeeds
- Dial with an already-cancelled ctx fails with context.Canceled without connecting; Dial to an unroutable address with a 100 ms context deadline returns within 2 s
- No goroutines leak across 100 timed-out calls (testkit.NoLeaks)
- burrowctl -timeout 50ms against a server that never replies exits 1 within 2 s and prints `timeout`

## Try it

```bash
./burrow watch
go test -run 'TestL3_4_NoStaleReplies' -v ./client  # the one that bites
```

To see a timeout by hand, run a listener that accepts and never answers (`nc -l 7070` does it), then `go run ./cmd/burrowctl -timeout 1s ping`.

## In the wild

net/http's Transport does exactly this per request; database/sql drivers and gRPC clients too. context.AfterFunc (Go 1.21) replaced a goroutine-per-call idiom.

## New in Go

net.Conn.SetDeadline · context.AfterFunc · net.Error.Timeout · errors.Is across layers · connection state machines

Standard library: `net`, `context`, `time`, `errors`
