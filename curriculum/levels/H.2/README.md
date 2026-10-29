# H.2 · Streaming

Send a response before you know how long it is, and accept uploads that don't say.

## Build

```go
// httpraw/response.go additions
func (w *ResponseWriter) Flush() // send what's buffered now

var ErrNotImplemented = errors.New("httpraw: not implemented") // -> 501 for a transfer coding other than chunked

// Responses:
//   - Buffer up to 64 KiB. If the handler writes more, or calls Flush before finishing, switch to
//     Transfer-Encoding: chunked (HTTP/1.1) or a close-delimited body (HTTP/1.0).
//   - Chunked: hex length, CRLF, bytes, CRLF; a final 0\r\n\r\n. The connection stays reusable.
//   - Small bodies keep Content-Length.
// Requests:
//   - Transfer-Encoding: chunked bodies are decoded. Ignore chunk extensions (;name=value) and trailers.
//   - Transfer-Encoding with Content-Length: 400. A coding other than chunked: 501. chunked not last: 400.
//   - Bad hex, an overflowing size, a negative size, or a missing CRLF after a chunk: 400.
//   - A chunked body growing past MaxBodyBytes: 413.

// KVRoutes (6.3) changes: GET /v1/keys?prefix= now streams, writing keys as it goes and calling
// Flush periodically, so a large listing arrives chunked instead of being buffered.
```

- `Flush` and the automatic switch to chunked (or close-delimited for HTTP/1.0) past 64 KiB
- A chunked request-body reader: a small state machine with limits
- Update `KVRoutes` so `GET /v1/keys` streams with `Flush`; the H.2 tests call `KVRoutes` and expect 2,500 keys to arrive chunked

Decisions the tests rely on:

- The first flushed chunk of a handler that wrote `hello` is exactly `5\r\nhello\r\n`.
- The 64 KiB buffer is the threshold: a 60 KB body keeps `Content-Length`, a 1 MiB body is chunked.

## The tests check

- Flushed data reaches the client while the handler is still blocked, as Transfer-Encoding: chunked with the first chunk `5\r\nhello`
- A 1 MiB response goes chunked and the connection stays reusable; a 60 KB body keeps Content-Length; HTTP/1.0 streams and ends by closing
- Chunked uploads: raw bytes with a chunk extension and a trailer decode, and the next request on the connection parses; a chunked upload from net/http echoes back
- Bad hex, a size that overflows int64, a missing CRLF after a chunk and a negative-looking size are 400
- A chunked body that grows past MaxBodyBytes 8 is 413
- Transfer-Encoding with Content-Length is 400; an unsupported coding is 501; `chunked, gzip` (chunked not last) is 400
- GET /v1/keys?prefix= from KVRoutes streams 2500 keys chunked, one per line, sorted, as text/plain

## Uses

- Keep-alive loop (built in H.1 · Keep-Alive)

## In the wild

net/http/internal.chunkedReader

## New in Go

chunk framing · hex lengths · io.Writer wrappers

Standard library: `bufio`, `strconv`, `io`
