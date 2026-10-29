# 6.2 · Status and Body

Read request bodies and write real responses through a handler. The `Handler` and `ResponseWriter` shapes here are deliberately close to net/http's, so 7.1 is a diff rather than a rewrite.

## Build

```go
// httpraw/response.go
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
//   Body          io.Reader // never nil; reads io.ErrUnexpectedEOF if the connection ends early

// Server gains:
//   Handler      Handler // nil: the hello page from 6.1
//   MaxBodyBytes int64   // 0 means 1 MiB

// Responses: Write without WriteHeader means 200. Buffer the body, then send status line,
// headers, blank line, body. Always send Date (GMT) and Content-Length. Default
// Content-Type text/plain; charset=utf-8 when there's a body and none was set. Standard
// reason phrases; any 3-digit status is accepted. 204 and 304 have no body and no
// Content-Length and Write returns ErrBodyNotAllowed. HEAD gets GET's headers and no body.
// A handler that panics produces a 500 and the server keeps running.
// Requests: Content-Length must be plain digits (400 otherwise). Over MaxBodyBytes: 413
// without calling the handler.
```

- `Handler`, `HandlerFunc`, `ResponseWriter` with buffered body and header snapshot
- `Request.Body` as a length-enforcing reader; `MaxBodyBytes`
- 204/304/HEAD rules, reason phrases, Date header; `recover` to 500

Decisions the tests rely on:

- `Date` uses `http.TimeFormat` in UTC. The default `Content-Type` is only added when the body is non-empty.
- An unknown status code is written with an empty reason phrase: `HTTP/1.1 299 \r\n`.

## The tests check

- Write without WriteHeader: 200, Date in http.TimeFormat, Content-Length, default Content-Type; custom status and headers appear; WriteHeader twice keeps the first; header changes after WriteHeader are ignored
- Reason phrases for 200/201/204/301/304/400/404/413/431/500/505 and a bare `299 ` for an unknown status, all parsed by net/http's ReadResponse
- A handler echoing the body: 5-byte, 0-byte and 512 KiB bodies round-trip through net/http's client
- No Content-Length: Body reads EOF immediately and ContentLength is 0
- Content-Length `abc`, `-1`, `1e3`: 400 before the handler runs
- Over MaxBodyBytes (and over the 1 MiB default): 413, handler not called, connection closed
- A body cut short by the client: the handler's Read returns io.ErrUnexpectedEOF
- 204 and 304: no body, no Content-Length, Write returns ErrBodyNotAllowed; HEAD gets GET's Content-Length and no body bytes
- A panicking handler: the client sees 500 and the next request on a new connection is served

## Uses

- HTTP parser (built in 6.1 · Request Line)

## In the wild

net/http's response and body types; the panic-to-500 behaviour you get for free from net/http is this

## New in Go

io.LimitReader and your own io.Reader · http.TimeFormat · recover · bytes.Buffer · method values as adapters

Standard library: `io`, `strconv`, `time`, `fmt`, `bytes`
