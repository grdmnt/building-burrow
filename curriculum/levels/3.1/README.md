# 3.1 · Loopback

> Before the store can talk to anyone, something has to answer the phone. This level is the part that picks up.

## Your goal

Write your first network server. It has two pieces. `ServeConns` is an *accept loop*: it waits for clients to connect over TCP and hands each connection to a function of your choice, in its own goroutine, so one slow client never holds up the others. `ServeEcho` is the simplest thing you can build on top of it: a server that sends back every byte a client sends it, so you can see the loop working with nothing but `nc` in a terminal.

**Why "loopback"?** The tests connect to `127.0.0.1`, the loopback address. Traffic to it never leaves your machine; it loops straight back to a program listening there. Every server in this course starts out this way.

The accept loop has one rule that matters more than the rest: when the listener is closed on purpose, `ServeConns` returns `nil`, not an error. That is how a server tells the difference between "someone shut me down" and "something broke".

## What you're using from before

Nothing. This is a root part: no earlier code is wired into it. You will write goroutines, which you first met in 1.5, and that is all.

## What's new here

**Listening and accepting.** A TCP server is two calls. `net.Listen` claims an address and gives you a `net.Listener`. `Accept` on that listener blocks until a client connects, then returns a `net.Conn` for that one client. You call `Accept` again for the next client, forever:

```go
ln, err := net.Listen("tcp", "127.0.0.1:7070")
// ...
for {
	conn, err := ln.Accept() // blocks until a client connects
	if err != nil {
		// decide: shut down cleanly, or report the error
	}
	go handle(conn) // the loop goes straight back to Accept
}
```

The `go` in front of `handle(conn)` is the whole point. Without it the loop would sit inside `handle` serving one client, and nobody else could connect until that client left.

**A connection is a Reader and a Writer.** `net.Conn` satisfies both `io.Reader` and `io.Writer`, so everything in the standard library that works on streams works on a socket. `io.Copy(dst, src)` copies bytes from `src` to `dst` until `src` reports end of input, and returns how many bytes it moved. Copying a connection to itself is an echo server:

```go
n, err := io.Copy(conn, conn) // read from the client, write it back
```

`Read` on a connection reports `io.EOF` once the client has closed its sending side, which is how `io.Copy` knows it is finished. The handler should then close the connection: `defer conn.Close()` as the first line of the handler guarantees it, whatever path the function takes out.

**Closing the listener.** `ln.Close()` makes every blocked and future `Accept` fail with an error that matches `net.ErrClosed`. That is your signal to stop:

```go
if errors.Is(err, net.ErrClosed) {
	return nil // we were told to stop; not a failure
}
return err // anything else is real (out of file descriptors, ...)
```

**Structured logging with `log/slog`.** `slog` logs a message plus key/value pairs instead of one formatted string, so log lines can be searched by field. The keys and values alternate after the message:

```go
logger.Info("client connected", "remote", conn.RemoteAddr())
// with slog.NewTextHandler this prints something like:
// time=... level=INFO msg="client connected" remote=127.0.0.1:52114
```

A `*slog.Logger` that is `nil` would panic on use. The clean fix is to swap a nil logger for one that throws everything away: `slog.New(slog.DiscardHandler)` (Go 1.24). Do that once at the top of `ServeConns` and the rest of the code never has to check.

## What to do

1. Create `wire/serve.go` in `package wire`.
2. Write `ServeConns(ln, handle, logger)`:
   - If `logger` is nil, replace it with a discard logger.
   - Loop on `ln.Accept()`. For every connection, start `handle(conn)` in a new goroutine.
   - When `Accept` fails with `net.ErrClosed`, return `nil`. For any other error, return it unchanged.
3. Write `ServeEcho(ln, logger)` by calling `ServeConns` with a handler that:
   - Defers `conn.Close()`.
   - Logs `client connected` with a `remote` attribute (the client's address).
   - Runs `io.Copy(conn, conn)` and keeps the byte count it returns.
   - Logs `client disconnected` with `remote` and `bytes` attributes.
4. Run `gofmt -l .` and `go vet ./...` until both print nothing.

## Example

Start the echo server from a throwaway program, then talk to it with `nc` (netcat). Each line you type comes straight back:

```bash
$ nc localhost 7070
hello burrow
hello burrow
one more
one more
^C
```

On the server side, with a text logger, the two connections above look like this:

```text
level=INFO msg="client connected" remote=127.0.0.1:52114
level=INFO msg="client disconnected" remote=127.0.0.1:52114 bytes=22
```

From Go, the same server used the way the tests use it:

```go
ln, _ := net.Listen("tcp", "127.0.0.1:0") // :0 picks a free port
go wire.ServeEcho(ln, nil)                // nil logger: no output

c, _ := net.Dial("tcp", ln.Addr().String())
c.Write([]byte("ping"))
buf := make([]byte, 4)
io.ReadFull(c, buf) // buf is now "ping"

ln.Close() // ServeEcho returns nil shortly after
```

## Done when

- [ ] A client gets back exactly what it sent, whether that is 13 bytes or 4 MiB
- [ ] A client that connects and sends nothing does not stop a second client from being served
- [ ] 50 clients at once each get their own data back, uncorrupted
- [ ] When a client closes its sending side, the server echoes what it has and closes the connection
- [ ] With a logger, every connection produces a `client connected` line and a `client disconnected` line with `remote=` and `bytes=` attributes; with a nil logger it works silently
- [ ] `ServeConns` calls the handler once per connection, returns `nil` after `ln.Close()`, and leaves no goroutine running
- [ ] An `Accept` error that is not `net.ErrClosed` comes back from `ServeConns` unchanged
- [ ] `gofmt -l .` and `go vet ./...` print nothing

## Reference

The exact contract the tests hold you to.

```go
// wire/serve.go
package wire

// ServeConns accepts connections on ln and calls handle(conn) in its
// own goroutine for each. It returns nil once ln is closed
// (errors.Is(err, net.ErrClosed)) and any other Accept error as-is.
// logger may be nil. handle must close conn.
func ServeConns(ln net.Listener, handle func(conn net.Conn),
	logger *slog.Logger) error

// ServeEcho echoes every byte back on each connection. Built on
// ServeConns. Logs `client connected` (remote) and `client
// disconnected` (remote, bytes).
func ServeEcho(ln net.Listener, logger *slog.Logger) error
```

| Event | Log message | Attributes |
|---|---|---|
| A client connects | `client connected` | `remote` |
| A client's connection ends | `client disconnected` | `remote`, `bytes` (as echoed) |

- `ServeConns` starts one goroutine per connection and never runs the handler inline.
- `ServeConns` returns `nil` for `net.ErrClosed` and any other `Accept` error as-is.
- `ServeEcho`'s handler closes the connection when `io.Copy` returns.

## The tests check

- A basic echo and a 4 MiB payload echoed intact
- An idle client doesn't block a second one (goroutine per connection)
- 50 concurrent clients each get their own 64 KiB back uncorrupted
- Half-close: client CloseWrite, server echoes what it got then closes; client reads EOF
- Log lines contain `client connected`, `remote=127.0.0.1:`, `client disconnected`, `bytes=5`; a nil logger works
- ServeConns calls the handler once per connection (three connections, three handlers), returns nil once the listener closes, and leaves no goroutine behind (testkit.NoLeaks)
- A listener whose Accept fails with another error: ServeConns returns that error as-is

## Try it

```bash
./burrow watch             # re-runs the tests every time you save
```

To play with it by hand, write a throwaway `main` somewhere outside the module (or a `cmd/scratch/main.go` you delete afterwards):

```go
ln, err := net.Listen("tcp", "127.0.0.1:7070")
if err != nil {
	log.Fatal(err)
}
logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
log.Fatal(wire.ServeEcho(ln, logger))
```

Then in another terminal: `nc localhost 7070`, type, and watch the lines come back and the log lines appear.

## In the wild

net/http's `Server.Serve` is this loop with timeouts and a connection state machine on top.

## New in Go

net.Listen/Accept · net.Conn as io.Reader and io.Writer · io.Copy · goroutine per connection · defer Close · log/slog attributes · slog.DiscardHandler

Standard library: `net`, `io`, `log/slog`, `errors`
