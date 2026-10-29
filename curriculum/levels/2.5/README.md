# 2.5 · Power Down

Stop on Ctrl-C without dropping requests that are already in progress.

## Build

```go
// wire/server.go
var ErrServerClosed = errors.New("wire: server closed")

// Shutdown stops accepting, closes idle connections right away, lets in-flight
// requests finish and get their replies, then closes those connections too.
// If ctx ends first it force-closes everything and returns ctx.Err().
// Safe to call more than once, and concurrently.
func (s *Server) Shutdown(ctx context.Context) error
```

After `Shutdown`, `Serve` returns `ErrServerClosed`, including a `Serve` called later.

```
// cmd/burrowd
burrowd serve [-addr 127.0.0.1:7070]
```

- Prints `listening on <actual address>` as its **first stdout line** (so `-addr 127.0.0.1:0` is usable).
- Serves the frame protocol from a Memory store.
- On SIGINT or SIGTERM: logs `shutting down` to stderr, shuts down gracefully, exits 0.
- A bad address exits 1; stray arguments or unknown flags exit 2.

## The tests check

- Shutdown with no connections is instant; 5 idle connections are closed right away (their reads see EOF).
- A request mid-flight when Shutdown starts still gets its reply and its write lands, then that connection closes.
- A 100ms context against a 3-second request: Shutdown returns `context.DeadlineExceeded` and force-closes.
- No new connections are served; concurrent and repeated Shutdowns all return nil.
- No goroutines leak.
- The real binary: build, start, use with your client, send each signal, expect exit 0 and the log line.

## Try it

```bash
go run ./cmd/burrowd serve &
go run ./cmd/burrowctl set k v
kill -TERM %1
```

## New in Go

`os/signal.NotifyContext` · shutdown ordering · tracking connections · `sync.WaitGroup` as a drain barrier
