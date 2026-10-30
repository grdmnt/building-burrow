# 6.1 · Request Line

Speak HTTP over a raw socket. No `net/http` in this chapter, except in the tests, where it acts as the judge. Three levels from now you will swap all of this for the standard library, and know exactly what it is doing for you.

## Build

```go
// httpraw/request.go
package httpraw

const (
	MaxHeaderBytes = 8 << 10 // request line + all headers
	MaxHeaders     = 100
)

var (
	ErrMalformed          = errors.New("httpraw: malformed request")        // -> 400
	ErrUnsupportedVersion = errors.New("httpraw: unsupported HTTP version") // -> 505
	ErrHeaderTooLarge     = errors.New("httpraw: request header too large") // -> 431
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

// ReadRequest reads the request line and headers, stopping right after the blank line.
func ReadRequest(br *bufio.Reader) (*Request, error)

// httpraw/server.go
type Server struct{ Logger *slog.Logger }
func (s *Server) Serve(ln net.Listener) error // wire.ServeConns underneath
// For now every request gets `200 OK` with body `Hello from Burrow\n` and a correct
// Content-Length, then the connection closes. Malformed: 400, unsupported version: 505,
// oversized head: 431, each followed by closing the connection.
```

- `Header` with case-insensitive access
- `ReadRequest` with the RFC 9112 token rules and both size limits
- A `Server` on `wire.ServeConns` that serves the hello page

`strings.Cut(s, sep)` returns `(before, after, found)` and is the right tool for every split in this level: the request line into three fields (twice), a header line at its first `:`, the target at its `?`. It replaces the `strings.Index` plus two slices dance and never allocates a slice of parts. `strings.CutPrefix` and `CutSuffix` are the same idea for `HTTP/` and `\r`.

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
curl -v localhost:8080/hello
printf 'GET / HTTP/2.0\r\n\r\n' | nc localhost 8080
```

## Uses

- Accept loop (built in 3.1 · Loopback)

## In the wild

net/http's textproto.Reader and readRequest; the same rules make nginx and Envoy reject request smuggling

## New in Go

bufio.Reader.ReadSlice · byte-level parsing · strings.Cut, CutPrefix and CutSuffix · token rules from RFC 9112 · net/url for absolute targets · your own map type with methods

Standard library: `bufio`, `strings`, `net/url`, `net`, `log/slog`
