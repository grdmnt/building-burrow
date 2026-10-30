# 7.3 · Signal Chain

> Logging, request IDs and panic recovery are not features of any one handler. They are layers every request passes through.

## Your goal

Write three pieces of *middleware* for the HTTP API: `RequestID`, which stamps every request with a random ID that the client sees in the `X-Request-ID` response header; `AccessLog`, which writes one JSON log line per request with the method, path, status, byte count and duration; and `Recover`, which turns a panicking handler into a JSON `500` and keeps the server alive. A middleware is a function that takes an `http.Handler` and returns a new one that does something before or after calling the original. `Chain` stacks them, and `Server.Handler()` wraps the whole API in all three.

The one tricky piece is seeing the status code. A handler writes it into the `http.ResponseWriter` and never tells you, so the logger has to wrap the writer with a type of its own that records what passes through.

## What you're using from before

**JSON API (7.2 · JSON In and Out).** `Recover` answers with the same `writeError` helper and the same `ErrorResponse` shape as every other error, using the code `internal`. `Server.Handler()` from 7.1 still builds the mux; it now returns the mux wrapped in the chain:

```go
func (s *Server) Handler() http.Handler {
    mux := http.NewServeMux()
    // ... the routes from 7.1 and 7.2
    return Chain(mux, Recover(), RequestID(), AccessLog(s.Logger))
}
```

## What's new here

**Closures as decorators.** `type Middleware func(http.Handler) http.Handler` is a function that wraps a handler. The returned handler is a function literal that closes over `next`, so it can run code before and after the original:

```go
func Timing() Middleware {
    return func(next http.Handler) http.Handler {
        fn := func(w http.ResponseWriter, r *http.Request) {
            start := time.Now()
            next.ServeHTTP(w, r)
            fmt.Println(r.URL.Path, time.Since(start))
        }
        return http.HandlerFunc(fn)
    }
}
```

`Chain(h, mw...)` applies them so that `mw[0]` is the outermost: it runs first on the way in and last on the way out. Build it by looping backwards over `mw` and wrapping `h` each time. Order matters: `Recover` goes outermost so a panic inside `AccessLog` or `RequestID` is caught too.

**Embedding an interface in a struct.** A struct can embed an interface type, not just another struct. The struct then has every method of the interface, forwarded to the embedded value, and you override only the ones you care about. That is how a status recorder sees `WriteHeader` without reimplementing `Header` and `Write`:

```go
type statusRecorder struct {
    http.ResponseWriter // embedded: Header, Write, WriteHeader free
    status int
    bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
    s.status = code
    s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
    if s.status == 0 {
        s.status = http.StatusOK // Write without WriteHeader means 200
    }
    n, err := s.ResponseWriter.Write(b)
    s.bytes += n
    return n, err
}
```

**The trap, and `Unwrap`.** Embedding gives your struct the three interface methods and nothing else. The real writer underneath also implements `http.Flusher` (for streaming) and `http.Hijacker`, and a handler that asks `f, ok := w.(http.Flusher)` gets `ok == false` once your wrapper is in the way, so streaming silently stops streaming. That `v, ok := x.(T)` form is the comma-ok type assertion: `ok` is false instead of a panic when the value does not implement `T`. The fix since Go 1.20 is `http.NewResponseController(w)`: it calls `Flush`, `SetReadDeadline`, `SetWriteDeadline` and `Hijack` on the writer, and when the writer has an `Unwrap() http.ResponseWriter` method it follows that chain down to the real one. Give `statusRecorder` that method and every wrapper in the chain stays transparent:

```go
func (s *statusRecorder) Unwrap() http.ResponseWriter {
    return s.ResponseWriter
}

// in a handler, through any number of wrappers:
rc := http.NewResponseController(w)
rc.Flush() // reaches the real connection
```

Keep the comma-ok form in mind for everything else: `if t, ok := err.(interface{ Timeout() bool }); ok && t.Timeout()` is the same move on an error.

**Context values with an unexported key type.** `context.WithValue(ctx, key, value)` attaches a value to a context, and `ctx.Value(key)` reads it back. The key's *type* is part of the lookup, so a key of an unexported type defined in your package cannot collide with any other package's keys, even if both use the zero value:

```go
type ctxKey struct{}

ctx := context.WithValue(r.Context(), ctxKey{}, id)
r = r.WithContext(ctx)

func RequestIDFrom(ctx context.Context) string {
    id, _ := ctx.Value(ctxKey{}).(string) // comma-ok: "" when absent
    return id
}
```

Use this sparingly: context values are for request-scoped data that crosses package boundaries, like a request ID, not for passing ordinary arguments.

**Random IDs.** `crypto/rand.Read` fills a byte slice from the operating system's random source, and `hex.EncodeToString` turns 16 bytes into 32 hex characters. Not `math/rand`: IDs must not be guessable or repeat after a restart.

**Structured logging.** `log/slog` writes key-value pairs instead of formatted strings. With `slog.NewJSONHandler` each line is a JSON object, which is what log collectors want:

```go
l := slog.New(slog.NewJSONHandler(os.Stderr, nil))
l.Info("request", "id", id, "method", r.Method,
    "path", r.URL.Path, "status", rec.status, "bytes", rec.bytes,
    "duration", time.Since(start))
// {"time":"...","level":"INFO","msg":"request","id":"3f9a...",
//  "method":"GET","path":"/v1/kv/k","status":200,"bytes":5,
//  "duration":183000}
```

## What to do

1. Create `api/middleware.go` with `Middleware` and `Chain`. `Chain(h, a, b, c)` returns `a(b(c(h)))`.
2. `RequestID()`: 16 random bytes from `crypto/rand`, hex-encoded; set `X-Request-ID` on the response, put the ID in the request context under an unexported key, and call `next` with `r.WithContext(ctx)`. Write `RequestIDFrom(ctx)` to read it back.
3. `statusRecorder`: embed `http.ResponseWriter`, record the status and the byte count, default the status to 200 on the first `Write`, and add `Unwrap() http.ResponseWriter`.
4. `AccessLog(l)`: wrap `w` in a `statusRecorder`, note the start time, call `next`, then log one line at `Info` with `id`, `method`, `path`, `status`, `bytes` and `duration`. A nil logger means no logging.
5. `Recover()`: `defer` a function that calls `recover()`; on a panic, log the value and `runtime/debug.Stack()` at `Error` with the request ID, and write a `500` `ErrorResponse{Code: "internal"}` with the request ID in the message, unless the handler had already started writing.
6. Make `Server.Handler()` return `Chain(mux, Recover(), RequestID(), AccessLog(s.Logger))`.

## Example

```bash
$ curl -i localhost:8080/v1/kv/greeting
HTTP/1.1 200 OK
Content-Type: application/octet-stream
Etag: "1"
X-Request-Id: 9c1f4e7b2a6d4f0e8b3c5a7d1e2f3a4b

hello
```

On the server's stderr, one line per request:

```json
{"time":"2026-10-31T09:41:03.120Z","level":"INFO","msg":"request",
 "id":"9c1f4e7b2a6d4f0e8b3c5a7d1e2f3a4b","method":"GET",
 "path":"/v1/kv/greeting","status":200,"bytes":5,"duration":210458}
```

And a handler that panics (add a temporary `/boom` route to see it):

```bash
$ curl -s localhost:8080/boom
{"error":"internal error (request 5d2e...)","code":"internal"}
$ curl -s localhost:8080/v1/kv/greeting     # still serving
hello
```

## Done when

- [ ] `Chain` runs the outermost middleware first: three middlewares that each append their name to a slice produce the names in order
- [ ] Every response carries `X-Request-ID` with 32 hex characters, unique across 1000 requests, and the same ID appears in the access log line
- [ ] The access log line is valid JSON with `id`, `method`, `path`, `status`, `bytes` and `duration`; `status` is what the handler wrote (not always 200) and `bytes` is the body length
- [ ] A panicking handler produces a `500` `ErrorResponse` with code `internal`, the panic value is in the log, and the next request works
- [ ] A panic inside `AccessLog`'s handler is still caught, because `Recover` is outermost
- [ ] Through the full chain, `http.NewResponseController(w).Flush()` succeeds, a comma-ok assertion on the unwrapped writer finds `http.Flusher`, and two flushed chunks arrive separately at the client

## Reference

The exact contract the tests will hold you to.

```go
// api/middleware.go
type Middleware func(http.Handler) http.Handler
// mw[0] is outermost
func Chain(h http.Handler, mw ...Middleware) http.Handler

// X-Request-ID: 16 hex bytes from crypto/rand, echoed in the response,
// stored in ctx
func RequestID() Middleware
func RequestIDFrom(ctx context.Context) string
// one JSON line per request: id, method, path, status, bytes, duration
func AccessLog(l *slog.Logger) Middleware
// panic -> 500 ErrorResponse{code: internal}, logged with the stack
func Recover() Middleware

// A statusRecorder embeds http.ResponseWriter and overrides
// WriteHeader and Write. It also implements
// Unwrap() http.ResponseWriter, returning the embedded writer, so that
// http.NewResponseController(w).Flush() and SetWriteDeadline() still
// reach the real connection through the wrapper.
// Server.Handler() returns
// Chain(mux, Recover(), RequestID(), AccessLog(l)).
```

## The tests check

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/7.3/files/`, matching the earlier chapters.

Pass when: every request logs one JSON line with id, method, path, status and duration; a panicking handler returns a JSON 500 with the request id and the server keeps serving.

The suite will cover:

- Chain order: outermost runs first; a slice of names recorded by three middlewares reads in order
- X-Request-ID is 32 hex chars, unique across 1000 requests, echoed in the response, and present in the access log line
- Access log line parses as JSON with the listed fields; status is the one the handler wrote (not 200) and bytes equals the body length
- A panicking handler produces 500 with ErrorResponse{code: internal} and the next request succeeds; the log contains the panic value
- Recover is outermost: a panic inside AccessLog's handler is still caught
- statusRecorder has Unwrap() http.ResponseWriter: through the full chain, http.NewResponseController(w).Flush() succeeds and a comma-ok assertion on the unwrapped writer finds http.Flusher; a handler that flushes twice through the chain sees both chunks arrive separately at an httptest client

## Try it

```bash
./burrow test
go run ./cmd/burrowd serve 2>&1 | grep '"msg":"request"' &
curl -i localhost:8080/v1/kv/greeting
```

## In the wild

justinas/alice-style chains; chi's middleware package; every observability vendor's Go SDK wraps the ResponseWriter this way.

## New in Go

closures as decorators · embedding an interface in a struct · comma-ok type assertions · Unwrap() and http.NewResponseController · context values with unexported key types · crypto/rand · structured logging with slog

Standard library: `log/slog`, `crypto/rand`, `encoding/hex`, `context`, `runtime/debug`
