# 6.2 · Status and Body

> A request has a body and a response has a status. Put a handler between them and the server stops being a hello page.

## Your goal

Turn the hello-page server from 6.1 into one that runs *your* code for each request. You write a `Handler` interface (something with a `ServeHTTP(w, r)` method), a `ResponseWriter` that collects a status, headers and a body, and a `Request.Body` that reads exactly `Content-Length` bytes. The server does the bookkeeping: `Date` and `Content-Length` headers, reason phrases like `404 Not Found`, no body on `204`, `304` or `HEAD`, a `413` for bodies over the limit, and a `500` when a handler panics, without the server dying.

The shapes are deliberately close to `net/http`'s. Chapter 7 swaps this package for the standard library, and you will recognise every piece.

## What you're using from before

**HTTP parser (6.1 · Request Line).** `ReadRequest(br)` gives you the method, path and headers and stops right after the blank line, so `br` is positioned at the first byte of the body. The body reader you build here wraps that same `br`. The `Header` type is reused as-is for response headers, and the 6.1 `Server` gains two fields, `Handler` and `MaxBodyBytes`:

```go
req, err := ReadRequest(br)
// ... 400/505/431 on error, as in 6.1
n, err := parseContentLength(req.Header.Get("content-length"))
// 0 when absent; err means 400
req.ContentLength = n
req.Body = &bodyReader{r: io.LimitReader(br, n), want: n}
```

When `Handler` is nil the server still serves `Hello from Burrow\n`, so the 6.1 tests keep passing.

## What's new here

**An interface with one method, and a function that satisfies it.** `Handler` is an interface with a single method. `HandlerFunc` is a named function type with a `ServeHTTP` method that just calls the function, so any plain function with the right signature can be converted into a `Handler`:

```go
type HandlerFunc func(w *ResponseWriter, r *Request)

func (f HandlerFunc) ServeHTTP(w *ResponseWriter, r *Request) {
    f(w, r)
}

hi := func(w *ResponseWriter, r *Request) {
    w.Write([]byte("hi"))
}
srv := &Server{Handler: HandlerFunc(hi)}
```

A method value works the same way: `s.hello` (no parentheses) is a function bound to `s`, so `HandlerFunc(s.hello)` adapts a method into a `Handler`.

**Buffering a response in `bytes.Buffer`.** You cannot send `Content-Length` before you know how long the body is, so the `ResponseWriter` collects the body in a `bytes.Buffer` (a growable byte slice with a `Write` method, so it is an `io.Writer`) and the server sends everything after the handler returns: status line, headers, blank line, body. Buffering also makes the panic case easy: nothing has gone out yet, so a `500` can replace whatever the handler wrote.

```go
var buf bytes.Buffer
buf.Write([]byte("hello"))
buf.Len()     // 5, the Content-Length
buf.Bytes()   // the body to send
```

`WriteHeader(status)` records the status and takes a copy of the headers at that moment; later changes are ignored and a second call does nothing. `Write` without `WriteHeader` first means `200`.

**`io.LimitReader` and a reader of your own.** `io.LimitReader(r, n)` returns a reader that stops after `n` bytes, so a handler calling `io.ReadAll(r.Body)` cannot read into the next request. It reports `io.EOF` both when the limit is reached and when the underlying connection ends early, and those are different situations: the second one is a client that promised 100 bytes and sent 16. Writing your own `io.Reader` is one method:

```go
type bodyReader struct {
    r    io.Reader // the LimitReader
    want int64     // Content-Length
    got  int64
}

func (b *bodyReader) Read(p []byte) (int, error) {
    n, err := b.r.Read(p)
    b.got += int64(n)
    if err == io.EOF && b.got < b.want {
        return n, io.ErrUnexpectedEOF
    }
    return n, err
}
```

**HTTP dates.** Every response carries `Date: Tue, 15 Nov 1994 08:12:31 GMT`. Go formats times with a layout written as a reference date (`Mon, 02 Jan 2006 15:04:05 GMT`) instead of `%Y-%m-%d` codes; `net/http` exports that exact layout as `http.TimeFormat`. `httpraw` doesn't import `net/http`, so copy the layout string into a constant of your own. It ends in a literal `GMT`, so format a UTC time:

```go
const timeFormat = "Mon, 02 Jan 2006 15:04:05 GMT" // = http.TimeFormat

time.Now().UTC().Format(timeFormat)
// "Sat, 31 Oct 2026 09:15:02 GMT"
```

**`defer`, precisely.** The panic-to-500 path depends on four details:

- The deferred call's arguments are evaluated at the `defer` statement, not when it runs. `defer log.Println(status)` logs the status as it was then; `defer func() { log.Println(status) }()` logs it as it is at exit. The same applies to the receiver of a method value.
- Deferred calls run last-in, first-out, after the `return` expression has been evaluated but before the function actually returns.
- `recover()` returns non-nil only when called directly by a deferred function during a panic. Calling it from a helper the deferred function calls does nothing.
- A deferred `recover` can only change what the caller sees through named results. Without the named result the recovery swallows the panic and the function returns the zero value:

```go
func callHandler(h Handler, w *ResponseWriter, r *Request) (err error) {
    defer func() {
        if p := recover(); p != nil {
            err = fmt.Errorf("handler panic: %v", p)
        }
    }()
    h.ServeHTTP(w, r)
    return nil
}
```

The server calls this, and on a non-nil error throws away the buffered response and sends a `500`.

## What to do

1. In `httpraw/response.go` add `ErrBodyNotAllowed`, `Handler`, `HandlerFunc` and `ResponseWriter` with `Header()`, `WriteHeader(status)` and `Write(p)`. Keep a status, a header snapshot, a `wroteHeader bool` and a `bytes.Buffer`.
2. Add `ContentLength` and `Body` to `Request`, and `Handler` and `MaxBodyBytes` to `Server`.
3. In the connection loop, after `ReadRequest`:
   - Parse `Content-Length`. It must be plain digits (no sign, no spaces, no exponent) and fit in an `int64`; anything else is a `400`, sent before the handler runs, then close.
   - If it is over `MaxBodyBytes` (default 1 MiB when the field is 0) answer `413` without calling the handler.
   - Set `Body` to your counting reader over `io.LimitReader(br, n)`. With no body it still must not be nil: an empty reader works.
4. Call the handler through the `recover` helper. If it panicked, log it and send a `500` with an empty body.
5. Write the response: status line, then headers. Always `Date` (UTC, `http.TimeFormat`) and `Content-Length`; add `Content-Type: text/plain; charset=utf-8` when the body is non-empty and the handler set none. Reason phrases for the usual codes; an unknown code gets an empty phrase, `HTTP/1.1 299 ` with the trailing space.
6. Apply the no-body rules. `204` and `304`: no body, no `Content-Length`, and `Write` returns `ErrBodyNotAllowed`. `HEAD`: send the headers (including the `Content-Length` a `GET` would have) and no body bytes.
7. Keep the hello page for a nil `Handler`, and close the connection after each response as in 6.1.

## Example

```go
srv := &httpraw.Server{Handler: httpraw.HandlerFunc(
    func(w *httpraw.ResponseWriter, r *httpraw.Request) {
        if r.Path == "/boom" {
            panic("oops")
        }
        w.Header().Set("X-Len", strconv.FormatInt(r.ContentLength, 10))
        w.WriteHeader(201)
        io.Copy(w, r.Body) // echo
    })}
```

```bash
$ curl -i -X POST --data 'hello there' localhost:8080/echo
HTTP/1.1 201 Created
Content-Length: 11
Content-Type: text/plain; charset=utf-8
Date: Sat, 31 Oct 2026 09:15:02 GMT
X-Len: 11

hello there
$ curl -i localhost:8080/boom
HTTP/1.1 500 Internal Server Error
Content-Length: 0
Date: Sat, 31 Oct 2026 09:15:03 GMT

$ curl -i -X POST -H 'Content-Length: abc' localhost:8080/echo
HTTP/1.1 400 Bad Request
```

The server is still up after `/boom`; the next request is served normally.

## Done when

- [ ] `Write` without `WriteHeader` is a `200` with `Content-Length`, the default `Content-Type` and a `Date` in GMT that `net/http` can parse
- [ ] A status and headers the handler set are sent; `Add` twice sends two values, `Del` removes one; the first `WriteHeader` wins and later header changes are not sent
- [ ] Standard reason phrases for 200, 201, 400, 404, 405, 500 and 503; `299` gives exactly `HTTP/1.1 299 `
- [ ] A handler sees `ContentLength` and can read the body: 0, 5 and 512 KiB bodies round-trip; a `GET` has a non-nil empty `Body`
- [ ] `Content-Length` `abc`, `-1`, `1e3`, `+5`, `5 5` and a 23-digit number are a `400` before the handler, then the connection closes
- [ ] A body over `MaxBodyBytes` is `413` without calling the handler; exactly at the limit is fine; the default limit is 1 MiB
- [ ] A body cut short by the client reads as `io.ErrUnexpectedEOF` in the handler
- [ ] `204` and `304` send no body and no `Content-Length` and `Write` returns `ErrBodyNotAllowed`; `HEAD` sends `GET`'s `Content-Length` and no body
- [ ] A panicking handler gives the client a `500` and the next request works

## Reference

The exact contract the tests hold you to.

```go
// httpraw/response.go
var ErrBodyNotAllowed = errors.New(
	"httpraw: response status does not allow a body")

type Handler interface {
	ServeHTTP(w *ResponseWriter, r *Request)
}
// HandlerFunc implements Handler.
type HandlerFunc func(w *ResponseWriter, r *Request)

type ResponseWriter struct { /* yours */ }
func (w *ResponseWriter) Header() Header
// first call wins; header changes after it are ignored
func (w *ResponseWriter) WriteHeader(status int)
func (w *ResponseWriter) Write(p []byte) (int, error)

// Request gains:
//   ContentLength int64     // 0 when there's no body
//   Body          io.Reader // never nil; reads io.ErrUnexpectedEOF
//                           // if the connection ends early

// Server gains:
//   Handler      Handler // nil: the hello page from 6.1
//   MaxBodyBytes int64   // 0 means 1 MiB

// Responses: Write without WriteHeader means 200. Buffer the body,
// then send status line, headers, blank line, body. Always send Date
// (GMT) and Content-Length. Default Content-Type text/plain;
// charset=utf-8 when there's a body and none was set. Standard reason
// phrases; any 3-digit status is accepted. 204 and 304 have no body
// and no Content-Length, and Write returns ErrBodyNotAllowed. HEAD gets
// GET's headers and no body.
// A handler that panics produces a 500 and the server keeps running.
// Requests: Content-Length must be plain digits (400 otherwise). Over
// MaxBodyBytes: 413 without calling the handler.
```

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

## Try it

```bash
./burrow watch
curl -i -X POST --data 'hello there' localhost:8080/echo
curl -i -I localhost:8080/            # HEAD: headers, no body
```

## In the wild

net/http's response and body types; the panic-to-500 behaviour you get for free from net/http is this.

## New in Go

methods on function types (HandlerFunc) · method values as adapters · bytes.Buffer · io.LimitReader and your own io.Reader · time layouts and http.TimeFormat · defer: argument evaluation time and LIFO order · recover through named results

Standard library: `io`, `strconv`, `time`, `fmt`, `bytes`
