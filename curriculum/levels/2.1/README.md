# 2.1 · Loopback

Your first network server: accept TCP connections and echo every byte back.

## Build

```go
// wire/echo.go
package wire

// ServeEcho accepts connections on ln and echoes each one back to itself.
// It returns nil once ln is closed, and any other Accept error as-is.
// logger may be nil, meaning no logging.
func ServeEcho(ln net.Listener, logger *slog.Logger) error
```

Log two structured lines per connection with slog:

- `client connected` with attribute `remote` (the client's address)
- `client disconnected` with `remote` and `bytes` (how many bytes were echoed)

## The tests check

- A basic echo, and a 4 MiB payload echoed intact.
- An idle client doesn't block anyone else: one goroutine per connection.
- 50 concurrent clients each get their own 64 KiB back, uncorrupted.
- **Half-close:** when the client closes its write side, the server echoes what it got, then closes.
- `ServeEcho` returns `nil` (not an error) once its listener closes. Look at `net.ErrClosed`.
- Log lines contain `client connected`, `remote=127.0.0.1:…`, `client disconnected`, `bytes=5`.
- A nil logger works.

## Try it

Add a `burrowd echo` command if you like, then in two terminals:

```bash
nc localhost 7070
```

## New in Go

`net.Listen` / `Accept` · `net.Conn` as `io.Reader` + `io.Writer` · `io.Copy` · goroutine per connection · `log/slog` attributes · `slog.DiscardHandler`
