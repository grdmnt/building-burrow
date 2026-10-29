# 4.6 · Conformance

Prove your server speaks real HTTP, add graceful shutdown, then read how `net/http` does it all.

## Build

```go
var ErrServerClosed = errors.New("httpraw: server closed")

// Same contract as wire.Server.Shutdown: stop accepting, close idle keep-alive
// connections, let in-flight requests finish (their responses carry
// Connection: close), force-close at the context deadline.
func (s *Server) Shutdown(ctx context.Context) error
```

Also support `Expect: 100-continue`: send `HTTP/1.1 100 Continue\r\n\r\n` before reading the body.

## The tests check

- **Differential parsing:** 9 requests go through both your `ReadRequest` and `net/http`'s. Where both accept, method, path, query and headers must agree.
- A fuzz target: `go test -fuzz=FuzzL4_6 ./httpraw`. Accepted requests must never have empty fields, stray whitespace, or CR/LF in header values.
- 50 clients × 20 PUT/GET pairs through keep-alive connections.
- Redirects followed by `net/http`'s client; `Expect: 100-continue` uploads.
- Shutdown: an idle keep-alive connection closes, an in-flight request finishes with `Connection: close`, `Serve` returns `ErrServerClosed`, nothing leaks. A stuck handler makes Shutdown give up at its deadline.

## Then read

`$(go env GOROOT)/src/net/http/server.go`: find `conn.serve`, `response.finishRequest` and `Server.Shutdown`. Write down three things it handles that yours doesn't.

## New in Go

differential testing · fuzzing parsers · reading the standard library
