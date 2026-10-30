# 3.5 · Power Down

> Pulling the plug loses whatever was mid-write. A proper power switch finishes the sentence first.

## Your goal

Give the server a clean way to stop. Today, Ctrl-C kills `burrowd` outright: a client whose request was being handled gets a dropped connection instead of its reply. After this level `Server.Shutdown(ctx)` stops accepting new connections, lets every request already in progress finish and get its reply, closes connections that are sitting idle, and gives up (closing everything) only if `ctx` runs out first. That is called a *graceful shutdown* or a *drain*.

To do it the server has to know, for every open connection, whether it is *idle* (waiting for the next request) or *busy* (a request has been read and its reply hasn't gone out yet). That bookkeeping is a small package of its own, `conntrack` (connection tracker). Finally `burrowd serve` ties it together: start the server, wait for a signal, shut down with a 10 second budget.

## What you're using from before

**Frame server** (3.2 · Switchboard). The connection loop you already have gets four tracker calls at the moments that matter, and `Serve` gets a listener it can close from `Shutdown`:

```go
func (s *Server) handleConn(conn net.Conn) {
	s.tracker.Add(conn)          // starts idle
	defer s.tracker.Remove(conn)
	for {
		req, err := ReadFrame(conn)
		// ...
		s.tracker.SetBusy(conn, true)  // a full request is in hand
		reply, keep := s.handle(req)
		err = WriteFrame(conn, reply)
		s.tracker.SetBusy(conn, false) // the reply is out; idle again
		// ...
	}
}
```

**Deadline-aware client** (3.4 · Deadlines). Nothing to wire in. The test that builds the real `burrowd` binary talks to it with `client.Dial`, `Set` and `Get` before sending the signal, so the client and the server have to work end to end.

**burrowd skeleton** (0.1 · Power On). `serve` is a new case in `run`'s command switch, next to `version` and `help`. Its stdout, stderr and exit code go through the same `run(args, stdout, stderr) int` as everything else.

## What's new here

**Signals and `signal.NotifyContext`.** When you press Ctrl-C the terminal sends the process `SIGINT` (interrupt). `kill <pid>` sends `SIGTERM` (terminate), which is what service managers and Kubernetes send first. Both are polite: the program may catch them and tidy up. (`SIGKILL`, from `kill -9`, cannot be caught; the kernel just ends the process.) In Go the simplest way to catch a signal is as a context:

```go
ctx, stop := signal.NotifyContext(context.Background(),
	os.Interrupt, syscall.SIGTERM)
defer stop()
// ctx.Done() is closed the moment either signal arrives
```

`os.Interrupt` is the portable name for `SIGINT`; `SIGTERM` comes from the `syscall` package.

**Shutdown ordering.** The order of the steps is the whole design:

1. Close the listener. `Accept` fails, the accept loop in `ServeConns` returns, and no new client can get in.
2. Drain: close every idle connection now, and close each busy one the moment its reply has been written.
3. If the context ends before the tracker is empty, close everything that is left and return `ctx.Err()`.

Closing the listener first matters: draining while still accepting would never finish.

**Bounded waits.** Never wait for something without a way out. `Drain` is a polling loop with a deadline from the context:

```go
for {
	t.CloseIdle()
	if t.Len() == 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		t.CloseAll()
		return ctx.Err()
	case <-time.After(5 * time.Millisecond):
	}
}
```

And `burrowd serve` gives `Shutdown` a budget with `context.WithTimeout(ctx, 10*time.Second)`, so a stuck client can delay exit by ten seconds and not forever.

**`sync.Once` for a one-shot transition.** `Shutdown` can be called twice, or from two goroutines at once (a signal and a test, say). The listener must be closed exactly once, and every caller should wait for the same drain and get `nil`. `sync.Once` runs its function the first time and makes later callers wait for it to finish:

```go
s.shutdownOnce.Do(func() {
	// close the listener, mark the server as shut down
})
// every caller continues here, after the first one's function returned
```

Mark the server as shut down inside that `Do`, so a `Serve` called afterwards can return `ErrServerClosed` straight away.

## What to do

1. Create `conntrack/conntrack.go`. A `Tracker` is a map from `net.Conn` to a busy flag under a mutex:
   - `Add` inserts as idle; `SetBusy` flips the flag; `Remove` deletes; `Len` counts.
   - `CloseIdle` closes and removes every idle connection and returns how many; `CloseAll` does the same for all of them.
   - `Drain` loops as shown above, polling every 5 ms, until the tracker is empty or `ctx` is done.
2. In `wire/server.go`, add `ErrServerClosed` and give `Server` a tracker, the listener `Serve` is using, a "closed" flag and a `sync.Once`.
3. Add the four tracker calls to the connection loop: `Add` on entry, `Remove` on exit, `SetBusy(true)` once a frame has been read, `SetBusy(false)` once the reply is written.
4. Make `Serve` return `ErrServerClosed` if the server is already shut down, and keep the listener where `Shutdown` can reach it.
5. Write `Shutdown`: inside a `sync.Once`, mark the server closed and close the listener; then (every caller) `Drain(ctx)` the tracker and return its result. A second `Shutdown` after a finished one returns `nil`.
6. Make `Serve` return `ErrServerClosed` (not `nil`) when its listener was closed by `Shutdown`.
7. In `cmd/burrowd`, add `serve [-addr 127.0.0.1:7070]`:
   - Parse the flag with a `flag.FlagSet`; a stray word or unknown flag is a usage error, exit 2.
   - `net.Listen`; a bad address prints the error to stderr and exits 1.
   - Print `listening on <ln.Addr()>` to stdout first, so `-addr :0` is usable.
   - Build a `signal.NotifyContext`, run `Serve` in a goroutine, and wait for the context. When it fires: log `shutting down` to stderr, call `Shutdown` with a 10 s timeout, exit 0.
8. Run `gofmt -l .` and `go vet ./...` until both print nothing.

## Example

```bash
$ go run ./cmd/burrowd serve &
listening on 127.0.0.1:7070
$ go run ./cmd/burrowctl set k v
OK 1
$ kill -TERM %1
shutting down
[1]+  Done    go run ./cmd/burrowd serve
```

From Go, a request caught mid-flight still gets its reply:

```go
srv := &wire.Server{Store: slowStore} // Set takes 300ms
go srv.Serve(ln)

c, _ := net.Dial("tcp", ln.Addr().String())
wire.WriteFrame(c,
	wire.Frame{Op: wire.OpSet, Key: "k", Value: []byte("v")})
// while that Set is running:
go srv.Shutdown(ctx)

r, _ := wire.ReadFrame(c) // r.Op == wire.OpOK: the reply still arrives
c.Read(make([]byte, 1))   // then io.EOF: the connection was closed
// srv.Shutdown returned nil; Serve returned wire.ErrServerClosed
```

## Done when

- [ ] `Tracker` counts, flips and removes connections; `CloseIdle` closes only idle ones and `CloseAll` closes everything; both return the count
- [ ] `Drain` returns `nil` once the last busy connection goes idle, and `ctx.Err()` with everything force-closed when the context ends first
- [ ] `Shutdown` with no connections is instant; idle connections are closed right away (their reads see EOF)
- [ ] A request already being handled when `Shutdown` starts still gets its reply, then its connection closes
- [ ] `Shutdown` with a 100 ms context against a 3 s request returns `context.DeadlineExceeded` and force-closes
- [ ] No connection made after `Shutdown` is served; repeated and concurrent `Shutdown` calls all return `nil`; `Serve` after `Shutdown` returns `ErrServerClosed`
- [ ] Nothing leaks a goroutine
- [ ] The built `burrowd serve -addr 127.0.0.1:0` prints `listening on <addr>` first, serves a `Set` and `Get`, and on SIGINT or SIGTERM logs `shutting down` and exits 0 within 5 s
- [ ] `serve` with a bad address exits 1 with the reason on stderr; `serve extra` and `serve -bogus` exit 2
- [ ] `gofmt -l .` and `go vet ./...` print nothing

## Reference

The exact contract the tests hold you to.

```go
// conntrack/conntrack.go
package conntrack

// Tracker records which connections are idle (waiting for a request)
// and which are busy.
type Tracker struct { /* yours */ }

func New() *Tracker
func (t *Tracker) Add(c net.Conn)            // starts idle
func (t *Tracker) SetBusy(c net.Conn, busy bool)
func (t *Tracker) Remove(c net.Conn)
func (t *Tracker) Len() int
// CloseIdle closes and removes idle conns and returns the count.
func (t *Tracker) CloseIdle() int
func (t *Tracker) CloseAll() int
// Drain closes idle connections now and again every few ms, until no
// connections remain or ctx is done, in which case it closes everything
// and returns ctx.Err().
func (t *Tracker) Drain(ctx context.Context) error

// wire/server.go additions
var ErrServerClosed = errors.New("wire: server closed")

// Shutdown stops accepting, lets in-flight requests finish and get
// their replies, closes idle connections at once and busy ones when
// they go idle. If ctx ends first it force-closes everything and
// returns ctx.Err(). Safe to call more than once, and concurrently.
// After Shutdown, Serve returns ErrServerClosed, including a Serve
// called later.
func (s *Server) Shutdown(ctx context.Context) error

// cmd/burrowd
//   burrowd serve [-addr 127.0.0.1:7070]
// Prints `listening on <actual address>` as its first stdout line (so
// -addr :0 is usable). Serves the frame protocol from a Memory store.
// On SIGINT or SIGTERM: logs `shutting down` to stderr, Shutdown with a
// 10 s budget, exits 0. Bad address exits 1; stray args exit 2.
```

| `burrowd serve ...` | Does | Exit |
|---|---|---|
| `serve`, `serve -addr 127.0.0.1:0` | prints `listening on <addr>`, serves until a signal, logs `shutting down` | 0 |
| `serve -addr not-an-address` | explains the listen error on stderr | 1 |
| `serve extra`, `serve -bogus` | usage error on stderr | 2 |

Decisions the tests rely on:

- `Drain` polls every 5 ms.
- A connection is busy from the moment a full frame has been read until its reply has been flushed.
- `Shutdown` closes the listener first, then drains. `burrowd serve` gives `Shutdown` a 10 s budget.

## The tests check

- conntrack: Add/SetBusy/Remove/Len; CloseIdle closes only idle connections and removes them; CloseAll closes busy and idle alike; Drain returns nil once a busy connection goes idle (and waits for it); Drain with a stuck connection returns ctx.Err() at the deadline and force-closes everything; 100 goroutines mutating the tracker under -race
- Shutdown with no connections is instant; 5 idle connections are closed right away (their reads see EOF)
- A request mid-flight (a fake store blocking 300 ms) still gets its reply, then that connection closes
- A 100 ms ctx against a 3 s request: Shutdown returns context.DeadlineExceeded and force-closes
- No new connections are served after Shutdown starts; concurrent and repeated Shutdowns all return nil; Serve after Shutdown returns ErrServerClosed
- testkit.NoLeaks across the whole suite
- The real binary: build, start with -addr 127.0.0.1:0, parse the `listening on` line, set and get a key with client.Client, send SIGINT then (fresh process) SIGTERM; both exit 0 within 5 s with `shutting down` on stderr
- burrowd serve with an unusable address exits 1 and explains on stderr; `serve extra` and `serve -bogus` exit 2

## Try it

```bash
./burrow watch
go run ./cmd/burrowd serve &
go run ./cmd/burrowctl set k v
kill -TERM %1
```

Then try it with a request in flight: run `serve` in one terminal, `nc localhost 7070` in another (it connects and sits idle), press Ctrl-C on the server, and watch `nc` exit as its idle connection is closed.

## In the wild

net/http.Server.Shutdown tracks exactly these states (StateNew/Active/Idle); Kubernetes sends SIGTERM and waits terminationGracePeriodSeconds, which is this ctx.

## New in Go

os/signal.NotifyContext · syscall.SIGTERM · shutdown ordering · bounded waits · sync.Once

Standard library: `os/signal`, `syscall`, `context`, `sync`, `errors`, `net`
