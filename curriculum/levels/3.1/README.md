# 3.1 · Loopback

Your first network server. The accept loop you write here is reused unchanged by the frame server, the hand-written HTTP server, the chaos proxy and the queue broker, so it carries the one rule that matters: return nil when the listener closes.

## Build

```go
// wire/serve.go
package wire

// ServeConns accepts connections on ln and calls handle(conn) in its own goroutine
// for each. It returns nil once ln is closed (errors.Is(err, net.ErrClosed)) and any
// other Accept error as-is. logger may be nil. handle must close conn.
func ServeConns(ln net.Listener, handle func(conn net.Conn), logger *slog.Logger) error

// ServeEcho echoes every byte back on each connection. Built on ServeConns.
// Logs `client connected` (remote) and `client disconnected` (remote, bytes).
func ServeEcho(ln net.Listener, logger *slog.Logger) error
```

- `ServeConns` with goroutine per connection and the net.ErrClosed rule
- `ServeEcho` on top of it with `io.Copy(conn, conn)` and `defer conn.Close()`
- Two slog lines per connection with `remote` and `bytes` attributes

## The tests check

- A basic echo and a 4 MiB payload echoed intact
- An idle client doesn't block a second one (goroutine per connection)
- 50 concurrent clients each get their own 64 KiB back uncorrupted
- Half-close: client CloseWrite, server echoes what it got then closes; client reads EOF
- ServeEcho and ServeConns return nil (not an error) once the listener closes; a listener that fails Accept for another reason has that error returned
- Log lines contain `client connected`, `remote=127.0.0.1:`, `client disconnected`, `bytes=5`; a nil logger works
- ServeConns: a handler that panics is not required to be recovered here, but a handler that returns leaves no goroutine behind (testkit.NoLeaks)

## Try it

Add a `burrowd echo` command if you like, then in two terminals:

```bash
nc localhost 7070
```

## Uses

Nothing from earlier levels. This is a root part.

## In the wild

net/http's Server.Serve is this loop with timeouts and a connection state machine on top; you'll read it in H.3

## New in Go

net.Listen/Accept · net.Conn as io.Reader and io.Writer · io.Copy · goroutine per connection · defer Close · log/slog attributes · slog.DiscardHandler

Standard library: `net`, `io`, `log/slog`, `errors`
