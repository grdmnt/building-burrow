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

`defer`, precisely, because the panic-to-500 path depends on it:

- The deferred call's arguments are evaluated at the `defer` statement, not when it runs. `defer log.Println(status)` logs the status as it was then; `defer func() { log.Println(status) }()` logs it as it is at exit. The same applies to the receiver of a method value.
- Deferred calls run last-in, first-out, after the `return` expression has been evaluated but before the function actually returns.
- Which is why a deferred `recover` can only change what the caller sees through named results: `func serve(w, r) (err error) { defer func() { if p := recover(); p != nil { err = fmt.Errorf("panic: %v", p) } }(); ... }`. Without the named result, the recovery swallows the panic and the function returns the zero value.
- `recover()` returns non-nil only when called directly by a deferred function during a panic. Calling it from a helper the deferred function calls does nothing.

Decisions the tests rely on:

- `Date` uses `http.TimeFormat` in UTC. The default `Content-Type` is only added when the body is non-empty.
- An unknown status code is written with an empty reason phrase: `HTTP/1.1 299 \r\n`.

## The tests check

- Write without WriteHeader: 200, Content-Length 2, default Content-Type text/plain; charset=utf-8, a Date in HTTP date format ending in GMT and within a minute of now
- Custom status and headers appear (Set, Add twice sends two values, Del removes); a Content-Type the handler set is kept
- Reason phrases for 200/201/400/404/405/500/503; 299 produces a status line net/http can parse, exactly `HTTP/1.1 299 ` with an empty reason
- WriteHeader twice keeps the first; header changes after WriteHeader are not sent
- A handler echoing the body sees ContentLength 11 and 5-byte, 0-byte and 512 KiB bodies round-trip through net/http's client
- A GET with no body has a non-nil Body that reads as empty
- Content-Length `abc`, `-1`, `1e3`, `+5`, `5 5` and a 23-digit number: 400 before the handler runs, then the connection closes
- MaxBodyBytes 10: an 11-byte body is 413 (either reason phrase) without calling the handler, a 10-byte body is fine; with MaxBodyBytes unset, 1 MiB + 1 is 413
- A body cut short by the client: the handler's Read returns io.ErrUnexpectedEOF
- 204 and 304: no body, no Content-Length, Write returns ErrBodyNotAllowed; HEAD gets GET's Content-Length (12) and no body bytes
- A panicking handler: the client sees 500 and the next request is served

## Uses

- HTTP parser (built in 6.1 · Request Line)

## In the wild

net/http's response and body types; the panic-to-500 behaviour you get for free from net/http is this

## New in Go

io.LimitReader and your own io.Reader · http.TimeFormat · defer: argument evaluation time and LIFO order · recover through named results · bytes.Buffer · method values as adapters

Standard library: `io`, `strconv`, `time`, `fmt`, `bytes`
