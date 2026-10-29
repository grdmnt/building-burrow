# 4.2 · Status and Body

Read request bodies and write real responses through a handler.

## Build

```go
var ErrBodyNotAllowed = errors.New("httpraw: response status does not allow a body")

type Handler interface {
	ServeHTTP(w *ResponseWriter, r *Request)
}
type HandlerFunc func(w *ResponseWriter, r *Request) // implements Handler

type ResponseWriter struct { /* yours */ }
func (w *ResponseWriter) Header() Header
func (w *ResponseWriter) WriteHeader(status int) // first call wins; header changes after it are ignored
func (w *ResponseWriter) Write(p []byte) (int, error)

// Request gains:
//   ContentLength int64     // 0 when there's no body
//   Body          io.Reader // never nil

// Server gains:
//   Handler      Handler // nil: the hello page from 4.1
//   MaxBodyBytes int64   // 0 means 1 MiB
```

Responses:

- `Write` without `WriteHeader` means 200. Buffer the body, then send status line, headers, blank line, body.
- Always send `Date` (HTTP date, GMT) and `Content-Length`. Default `Content-Type: text/plain; charset=utf-8` when there's a body and the handler didn't set one.
- Standard reason phrases (`200 OK`, `404 Not Found`, …); any 3-digit status must still parse.
- 204 and 304 have no body and no `Content-Length`; `Write` returns `ErrBodyNotAllowed`.
- `HEAD` gets the headers a `GET` would, including its `Content-Length`, and no body.
- Never let a header value containing CR or LF, or an invalid header name, reach the wire.
- A handler that panics produces a 500, and the server keeps running.

Requests:

- `Content-Length` must be plain digits; conflicting values are a 400 (request smuggling bait).
- Over `MaxBodyBytes`: 413, without calling the handler.
- A body that ends before its `Content-Length` reads as `io.ErrUnexpectedEOF`.

## New in Go

your own `io.Reader` · `http.TimeFormat`-style dates · `recover` · header injection and request smuggling
