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

// A statusRecorder embeds http.ResponseWriter and overrides WriteHeader and Write. It also
// implements Unwrap() http.ResponseWriter, returning the embedded writer, so that
// http.NewResponseController(w).Flush() and SetWriteDeadline() still reach the real
// connection through the wrapper (8.3's event stream and 7.4's deadlines depend on it).
// Server.Handler() returns Chain(mux, Recover(), RequestID(), AccessLog(l)).
```

- `Middleware`, `Chain`
- `RequestID`, `AccessLog`, `Recover`
- A ResponseWriter wrapper by embedding, with `Unwrap() http.ResponseWriter`

The trap in wrapping a `ResponseWriter`: embedding gives your struct the three interface methods and nothing else. The underlying `*http.response` also implements `http.Flusher` and `http.Hijacker`, and a handler that does `f, ok := w.(http.Flusher)` (the comma-ok type assertion: `ok` is false instead of a panic when the value doesn't implement it) gets `false` once your wrapper is in the way, so streaming silently stops streaming. The modern fix is `http.NewResponseController(w)` (Go 1.20): it calls `Flush`, `SetReadDeadline`, `SetWriteDeadline` and `Hijack` on the writer, and when the writer has an `Unwrap() http.ResponseWriter` method it follows that chain down to the real one. Give `statusRecorder` that method and every wrapper in the chain stays transparent. Keep the comma-ok form in mind for everything else: `if s, ok := err.(interface{ Timeout() bool }); ok && s.Timeout()` is the same move on an error.

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/7.3/files/`, matching the earlier chapters.

## Pass when

Every request logs one JSON line with id, method, path, status and duration; a panicking handler returns a JSON 500 with the request id and the server keeps serving.

The suite will cover:

- Chain order: outermost runs first; a slice of names recorded by three middlewares reads in order
- X-Request-ID is 32 hex chars, unique across 1000 requests, echoed in the response, and present in the access log line
- Access log line parses as JSON with the listed fields; status is the one the handler wrote (not 200) and bytes equals the body length
- A panicking handler produces 500 with ErrorResponse{code: internal} and the next request succeeds; the log contains the panic value
- Recover is outermost: a panic inside AccessLog's handler is still caught
- statusRecorder has Unwrap() http.ResponseWriter: through the full chain, http.NewResponseController(w).Flush() succeeds and a comma-ok assertion on the unwrapped writer finds http.Flusher; a handler that flushes twice through the chain sees both chunks arrive separately at an httptest client

## Uses

- JSON API (built in 7.2 · JSON In and Out)

## In the wild

justinas/alice-style chains; chi's middleware package; every observability vendor's Go SDK wraps the ResponseWriter this way

## New in Go

closures · decorators · embedding an interface in a struct · comma-ok type assertions · Unwrap() and http.NewResponseController · context values with unexported key types · structured logging

Standard library: `log/slog`, `crypto/rand`, `encoding/hex`, `context`, `runtime/debug`
