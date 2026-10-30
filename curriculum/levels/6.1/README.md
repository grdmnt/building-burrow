# 6.1 · Request Line

> HTTP is text over a socket. Before the standard library does it for you, do it yourself once, strictly.

## Your goal

Write `httpraw`, a package that reads an HTTP/1.1 request straight off a TCP connection: the request line (`GET /hello HTTP/1.1`), the headers, and the blank line that ends them. Then put a tiny server on it that answers every request with `Hello from Burrow`. No `net/http` in your code for this chapter; the tests use it as the judge, because if Go's own client can talk to your server, your server speaks HTTP.

"Strictly" is the point. A parser that accepts sloppy input is how request smuggling attacks work, so every malformed request gets `ErrMalformed`, every oversized one gets `ErrHeaderTooLarge`, and only HTTP/1.0 and 1.1 get through.

## What you're using from before

**Accept loop (3.1 · Loopback).** `wire.ServeConns(ln, handle, logger)` accepts connections and runs `handle(conn)` in a goroutine per connection, returning nil when the listener closes. `httpraw.Server.Serve` is that loop with an HTTP handler:

```go
func (s *Server) Serve(ln net.Listener) error {
    return wire.ServeConns(ln, s.handle, s.Logger)
}

func (s *Server) handle(conn net.Conn) {
    defer conn.Close()
    req, err := ReadRequest(bufio.NewReader(conn))
    // write a response, then return (which closes the connection)
}
```

The goroutine per connection is what makes a client that sends half a request and stalls harmless to everyone else.

## What's new here

**Reading lines from a socket with `bufio.Reader`.** A `net.Conn` hands you bytes in whatever chunks the network delivers. `bufio.NewReader(conn)` puts a 4 KiB buffer in front of it so you can ask for one line at a time. `ReadSlice('\n')` returns everything up to and including the next `\n`, as a slice that points into the buffer: it is valid only until the next read, so convert what you keep to a `string` (which copies). If a line is longer than the buffer, `ReadSlice` returns what it has with `bufio.ErrBufferFull`; call it again and append until you see the `\n` or go past your size limit.

```go
line, err := br.ReadSlice('\n')
// line == []byte("Host: example.com\r\n"), err == nil
// a 9000-byte line: err == bufio.ErrBufferFull, so call again
// and append
```

When the connection ends before the `\n`, `ReadSlice` returns `io.EOF`. Before any byte of a request that is a clean close, and you return `io.EOF` itself. Mid-request it is a cut-off request, and you return `io.ErrUnexpectedEOF`.

**Splitting with `strings.Cut`.** `strings.Cut(s, sep)` returns `(before, after, found)`. It is the right tool for every split in this level: the request line into three fields (cut twice on a single space), a header line at its first `:`, the target at its `?`. `strings.CutPrefix(s, "HTTP/")` and `strings.CutSuffix(line, "\r")` are the same idea for a fixed start or end.

```go
method, rest, ok := strings.Cut("GET /x?a=1 HTTP/1.1", " ")
// method == "GET", rest == "/x?a=1 HTTP/1.1", ok == true
target, proto, ok := strings.Cut(rest, " ")
// target == "/x?a=1", proto == "HTTP/1.1"
path, query, _ := strings.Cut(target, "?")
// path == "/x", query == "a=1"
```

A double space shows up as an empty field, and a missing or extra space as `ok == false` or leftover text. Each of those is `ErrMalformed`.

**Byte-level checks and the token rule.** HTTP (RFC 9112) says a method and a header name are *tokens*: one or more of letters, digits and `!#$%&'*+-.^_` plus backtick, `|` and `~`. Anything else, including a space before the colon in `Host : x`, makes the request malformed. Write one `isToken(s string) bool` that loops over the bytes and reuse it. A header value may contain any byte except control characters (tab is allowed), so a NUL byte is a reject; a path may not contain control characters either. A line that starts with a space or tab is old-style line folding and is also rejected.

**Absolute targets with `net/url`.** Most requests use `/path?query`, but a proxy-style client may send `GET http://example.com/a/b?c=d HTTP/1.1`. `url.Parse` splits that into `.Path` (`/a/b`) and `.RawQuery` (`c=d`); `Target` keeps what was sent. The only other allowed target is `*` (for `OPTIONS *`). A bare word like `hello` is malformed.

**Your own map type with methods.** `type Header map[string][]string` is a named type, so it can have methods, and because a map is a reference, a value receiver can still change it. Store names lower-cased on the way in and lower-case the lookup, and `Get("HOST")` finds `host`. One trap: a nil map panics on write, so build the request's `Header` with `make` before adding to it.

```go
h := Header{}
h.Add("Accept", "text/html")
h.Add("ACCEPT", "application/json")
h.Get("accept")     // "text/html" (the first value)
h.Values("Accept")  // []string{"text/html", "application/json"}
```

## What to do

1. Create `httpraw/request.go` with the constants, errors, `Header` and `Request` from the Reference, and the five `Header` methods.
2. Write `ReadRequest(br *bufio.Reader) (*Request, error)`:
   - Write a `readLine` helper on top of `ReadSlice('\n')` that strips `\r\n` or a bare `\n`, keeps appending on `ErrBufferFull`, counts bytes toward `MaxHeaderBytes`, and turns `io.EOF` into `io.ErrUnexpectedEOF` once any byte of the request has been read.
   - Skip one empty line before the request line (clients sometimes send a stray `\r\n`).
   - Cut the request line into method, target and version. Check the method is a token, the target is `/...`, `*` or an absolute URL, and the version is `HTTP/` followed by digit, dot, digit. `HTTP/1.0` and `HTTP/1.1` are fine; any other well-formed version is `ErrUnsupportedVersion`.
   - Read header lines until the empty line. Cut each at the first `:`, check the name is a token, trim spaces and tabs from both ends of the value only, and `Add` it. More than `MaxHeaders` lines is `ErrHeaderTooLarge`.
   - Stop right after the blank line. Do not read further; the next bytes belong to the body.
3. Create `httpraw/server.go` with `Server{Logger}` and `Serve` on `wire.ServeConns`. For each connection: `ReadRequest`, then write `HTTP/1.1 200 OK`, a `Content-Length`, a blank line and `Hello from Burrow\n`, every line ending in `\r\n`. Close the connection afterwards.
4. Map the three errors to status lines: `ErrMalformed` to `400 Bad Request`, `ErrUnsupportedVersion` to `505 HTTP Version Not Supported`, `ErrHeaderTooLarge` to `431 Request Header Fields Too Large`. Send the status line, a blank line, and close.
5. Run `gofmt` and `go vet` as always.

## Example

```go
br := bufio.NewReader(strings.NewReader(
    "GET /hello?name=gerard HTTP/1.1\r\nHost: localhost\r\n\r\n"))
req, err := httpraw.ReadRequest(br)
// err == nil
// req.Method == "GET", req.Path == "/hello"
// req.RawQuery == "name=gerard"
// req.Proto == "HTTP/1.1", req.Header.Get("host") == "localhost"

_, err = httpraw.ReadRequest(bufio.NewReader(strings.NewReader(
    "GET  / HTTP/1.1\r\n\r\n")))
// errors.Is(err, httpraw.ErrMalformed):
// the double space is an empty field
```

And on the wire, with the server listening on 8080:

```bash
$ curl -i localhost:8080/hello
HTTP/1.1 200 OK
Content-Length: 18

Hello from Burrow
$ printf 'GET / HTTP/2.0\r\n\r\n' | nc localhost 8080
HTTP/1.1 505 HTTP Version Not Supported

$ printf 'GET / HTTP/1.1\r\nHost : x\r\n\r\n' | nc localhost 8080
HTTP/1.1 400 Bad Request

```

## Done when

- [ ] A valid request parses into `Method`, `Target`, `Path`, `RawQuery`, `Proto` and `Header`; absolute-form targets and `OPTIONS *` work
- [ ] Header lookup ignores case, values are trimmed only at the ends, repeated headers keep every value in order, and an empty value is still present
- [ ] Bare `\n` line endings and one leading blank line are accepted; `HTTP/1.0` is accepted
- [ ] All 16 malformed shapes in the tests return `ErrMalformed`; `HTTP/2.0`, `3.0`, `0.9` and `1.2` return `ErrUnsupportedVersion`
- [ ] A 9 KB header, a 9 KB request line and 101 headers return `ErrHeaderTooLarge`
- [ ] No bytes at all is `io.EOF`; a request cut short is `io.ErrUnexpectedEOF`; `ReadRequest` leaves the body unread
- [ ] `http.Get` against your server receives `Hello from Burrow\n` with the right `Content-Length`, and bad requests get 400, 505 or 431 followed by a close

## Reference

The exact contract the tests hold you to.

```go
// httpraw/request.go
package httpraw

const (
	MaxHeaderBytes = 8 << 10 // request line + all headers
	MaxHeaders     = 100
)

var (
	// -> 400
	ErrMalformed = errors.New("httpraw: malformed request")
	// -> 505
	ErrUnsupportedVersion = errors.New(
		"httpraw: unsupported HTTP version")
	// -> 431
	ErrHeaderTooLarge = errors.New("httpraw: request header too large")
)

type Header map[string][]string // names stored lower-cased
func (h Header) Get(name string) string
func (h Header) Values(name string) []string
func (h Header) Set(name, value string)
func (h Header) Add(name, value string)
func (h Header) Del(name string)

type Request struct {
	Method   string // "GET"
	Target   string // as sent: "/hello?x=1"
	Path     string // "/hello", still percent-encoded
	RawQuery string // "x=1"
	Proto    string // "HTTP/1.1" or "HTTP/1.0"
	Header   Header
}

// ReadRequest reads the request line and headers, stopping right after
// the blank line.
func ReadRequest(br *bufio.Reader) (*Request, error)

// httpraw/server.go
type Server struct{ Logger *slog.Logger }
// Serve runs on wire.ServeConns underneath.
func (s *Server) Serve(ln net.Listener) error
// For now every request gets `200 OK` with body `Hello from Burrow\n`
// and a correct Content-Length, then the connection closes.
// Malformed: 400, unsupported version: 505, oversized head: 431,
// each followed by closing the connection.
```

Decisions the tests rely on:

- `Header.Set` and `Add` store the name lower-cased; `Get` returns the first value; `Values` returns the stored slice, which callers must not modify.
- For `OPTIONS *` the `Path` is `*`. For an absolute-form target, `Path` and `RawQuery` come from the URL and `Target` is kept as sent.

## The tests check

- Method, target, path, query and version parsing; absolute-form targets (http://host/path?q) and `OPTIONS *`
- Header names case-insensitive, values trimmed at both ends only, internal whitespace kept, repeated headers kept in order, empty values present
- Bare \n line endings accepted; one blank line before the request line ignored; HTTP/1.0 accepted
- 16 malformed cases: two parts, four parts, double space, no method, bad method char, relative target, lowercase http/1.1, not HTTP, version missing minor, header without colon, space before colon, empty header name, bad header name char, obsolete line folding, NUL in header value, control char in path
- HTTP/2.0, 3.0, 0.9, 1.2 are ErrUnsupportedVersion (well-formed but unsupported)
- A 9 KB header and a 9 KB request line are ErrHeaderTooLarge; 101 headers is ErrHeaderTooLarge
- io.EOF for a clean close before any bytes, io.ErrUnexpectedEOF for a request cut short; ReadRequest doesn't read past the blank line (the next bytes are the body)
- A real net/http client gets the hello page; curl -i shape checked byte for byte; 400/505/431 statuses on the wire for bad input; a half-sent request from one client doesn't block another

## Try it

```bash
./burrow watch
curl -v localhost:8080/hello
printf 'GET / HTTP/2.0\r\n\r\n' | nc localhost 8080
```

## In the wild

net/http's textproto.Reader and readRequest; the same rules make nginx and Envoy reject request smuggling.

## New in Go

bufio.Reader.ReadSlice · byte-level parsing · strings.Cut, CutPrefix and CutSuffix · token rules from RFC 9112 · net/url for absolute targets · your own map type with methods

Standard library: `bufio`, `strings`, `net/url`, `net`, `log/slog`
