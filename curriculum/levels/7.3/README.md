# 7.3 · Signal Chain

Logging, request IDs and panic recovery as composable layers. A middleware is a function from handler to handler; wrapping the ResponseWriter is where struct embedding earns its keep again.

## Build

```go
// api/middleware.go
type Middleware func(http.Handler) http.Handler
func Chain(h http.Handler, mw ...Middleware) http.Handler // mw[0] is outermost

func RequestID() Middleware                 // X-Request-ID: 16 hex bytes from crypto/rand, echoed in the response, stored in ctx
func RequestIDFrom(ctx context.Context) string
func AccessLog(l *slog.Logger) Middleware   // one JSON line per request: id, method, path, status, bytes, duration
func Recover() Middleware                   // panic -> 500 ErrorResponse{code: internal}, logged with the stack

// A statusRecorder embeds http.ResponseWriter and overrides WriteHeader and Write.
// Server.Handler() returns Chain(mux, Recover(), RequestID(), AccessLog(l)).
```

- `Middleware`, `Chain`
- `RequestID`, `AccessLog`, `Recover`
- A ResponseWriter wrapper by embedding

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/7.3/files/`, matching the earlier chapters.

## Pass when

Every request logs one JSON line with id, method, path, status and duration; a panicking handler returns a JSON 500 with the request id and the server keeps serving.

The suite will cover:

- Chain order: outermost runs first; a slice of names recorded by three middlewares reads in order
- X-Request-ID is 32 hex chars, unique across 1000 requests, echoed in the response, and present in the access log line
- Access log line parses as JSON with the listed fields; status is the one the handler wrote (not 200) and bytes equals the body length
- A panicking handler produces 500 with ErrorResponse{code: internal} and the next request succeeds; the log contains the panic value
- Recover is outermost: a panic inside AccessLog's handler is still caught

## Uses

- JSON API (built in 7.2 · JSON In and Out)

## In the wild

justinas/alice-style chains; chi's middleware package; every observability vendor's Go SDK wraps the ResponseWriter this way

## New in Go

closures · decorators · embedding an interface in a struct · context values with unexported key types · structured logging

Standard library: `log/slog`, `crypto/rand`, `encoding/hex`, `context`, `runtime/debug`
