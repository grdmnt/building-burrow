# H.3 · Conformance

Harden the server against injection and smuggling, add `Expect: 100-continue` and graceful shutdown on `conntrack`, prove it against net/http with differential tests and fuzzing, then read how net/http does it all.

## Build

```go
// httpraw/server.go additions
var ErrServerClosed = errors.New("httpraw: server closed")

// Shutdown has the same contract as wire.Server.Shutdown (3.5): stop accepting, close idle
// keep-alive connections, let in-flight requests finish (their responses carry
// Connection: close), force-close at the context deadline and return ctx.Err().
// After Shutdown, Serve returns ErrServerClosed. Build it on conntrack.Tracker.
func (s *Server) Shutdown(ctx context.Context) error

// Hardening:
//   - Expect: 100-continue: send HTTP/1.1 100 Continue\r\n\r\n before reading the body.
//   - Never let a header value containing CR or LF, or an invalid header name, reach the wire.
//   - Two Content-Length headers with different values, or a comma-separated list with different
//     values, is 400 (request smuggling bait).
//   - Request.Path stays percent-encoded; net/http compares with URL.EscapedPath().
```

- `Shutdown` and `ErrServerClosed`, on `conntrack.Tracker` from 3.5: a connection waiting for its next request is idle
- `Expect: 100-continue`
- Header injection and conflicting `Content-Length` protection
- Pass the differential tests and the fuzzer; then read `$(go env GOROOT)/src/net/http/server.go` (`conn.serve`, `response.finishRequest`, `Server.Shutdown`) and write down three things it handles that yours doesn't

## The tests check

- Differential parsing: 9 requests go through both your ReadRequest and net/http's; where both accept, method, escaped path, query and headers must agree
- FuzzLH_3_ReadRequest: accepted requests never have empty Method/Proto/Path, stray whitespace in the request line, headers Get can't find, or CR/LF in header values
- 50 clients x 20 PUT/GET pairs through keep-alive connections
- net/http's client follows a 302 through your server; Expect: 100-continue gets `HTTP/1.1 100 Continue` before the body is read
- Shutdown: an idle keep-alive connection closes, an in-flight request finishes with Connection: close, Serve returns ErrServerClosed; a stuck handler makes Shutdown return context.DeadlineExceeded at its deadline
- A header value containing CR/LF never injects a new header on the wire; a header name with a space is not sent
- Two different Content-Length headers, or a `1, 2` list, are 400 without calling the handler

## Uses

- Chunked codec (built in H.2 · Streaming)
- Keep-alive loop (built in H.1 · Keep-Alive)
- Shutdown (built in 3.5 · Power Down)

## In the wild

h2spec and the HTTP/1.1 conformance suites that nginx and Envoy run

## New in Go

differential testing · fuzzing parsers · reading the standard library

Standard library: `net/http (tests only)`, `testing`
