# 4.1 · Request Line

Speak HTTP over a raw socket. No `net/http` in this chapter, except in the tests, where it acts as the judge.

## Build

```go
// httpraw/request.go
package httpraw

const (
	MaxHeaderBytes = 8 << 10 // request line + all headers
	MaxHeaders     = 100
)

var (
	ErrMalformed          = errors.New("httpraw: malformed request")          // → 400
	ErrUnsupportedVersion = errors.New("httpraw: unsupported HTTP version")   // → 505
	ErrHeaderTooLarge     = errors.New("httpraw: request header too large")   // → 431
)

type Header map[string][]string // store names lower-cased
func (h Header) Get(name string) string        // first value, case-insensitive
func (h Header) Values(name string) []string
func (h Header) Set(name, value string)
func (h Header) Add(name, value string)
func (h Header) Del(name string)

type Request struct {
	Method   string // "GET"
	Target   string // as sent: "/hello?x=1"
	Path     string // "/hello", still percent-encoded
	RawQuery string // "x=1"
	Proto    string // "HTTP/1.1"
	Header   Header
}

// ReadRequest reads the request line and headers, stopping right after the blank line.
func ReadRequest(br *bufio.Reader) (*Request, error)
```

```go
// httpraw/server.go
type Server struct{}
func (s *Server) Serve(ln net.Listener) error
```

For now the server answers every request with `200 OK` and the body `Hello from Burrow\n`, with a correct `Content-Length`. (Keep that as the behaviour when no Handler is set, later.) Malformed requests get 400, unsupported versions 505, oversized headers 431, each followed by closing the connection.

## The tests check

- Method, target, path, query and version parsing; absolute-form targets (`http://host/path?q`) and `OPTIONS *`.
- Header names case-insensitive, values trimmed at both ends only, repeated headers kept in order, empty values present.
- Bare `\n` line endings accepted; one blank line before the request line ignored.
- 16 kinds of malformed request, from double spaces and lowercase `http/1.1` to space before the colon, obsolete line folding and control bytes.
- Size limits on the whole head and on the number of headers.
- `io.EOF` for a clean close before any bytes, `io.ErrUnexpectedEOF` for a request cut short.
- `ReadRequest` doesn't read past the blank line.
- A real `net/http` client gets your hello page; a half-sent request from one client doesn't block another.

## Try it

```bash
curl -v localhost:8080/hello
printf 'GET / HTTP/2.0\r\n\r\n' | nc localhost 8080
```

## New in Go

`bufio.Reader.ReadSlice` · byte-level parsing · token rules from RFC 9112 · `net/url` for absolute targets
