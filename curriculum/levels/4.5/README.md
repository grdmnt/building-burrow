# 4.5 · Streaming

Send a response before you know how long it is, and accept uploads that don't say.

## Build

```go
func (w *ResponseWriter) Flush() // send what's buffered now

var ErrNotImplemented = errors.New("httpraw: not implemented") // → 501
```

Responses:

- Buffer up to 64 KiB. If the handler writes more, or calls `Flush` before finishing, switch to `Transfer-Encoding: chunked` (HTTP/1.1) or a close-delimited body (HTTP/1.0).
- Chunked: hex length, CRLF, bytes, CRLF; a final `0\r\n\r\n`. The connection stays reusable.
- Small bodies keep `Content-Length`.

Requests:

- `Transfer-Encoding: chunked` bodies are decoded. Ignore chunk extensions (`;name=value`) and trailers.
- `Transfer-Encoding` with `Content-Length`: 400. A coding other than chunked: 501. `chunked` not last: 400.
- Bad hex, an overflowing size, or a missing CRLF after a chunk: 400. A chunked body growing past `MaxBodyBytes`: 413 (or 400).

KV API gains `GET /v1/keys?prefix=…`: one key per line, sorted, streamed with periodic `Flush`.

## The tests check

- Flushed data reaches the client while the handler is still blocked.
- A 1 MiB response goes chunked; a 60 KB one keeps `Content-Length`; HTTP/1.0 streams and closes.
- Chunked uploads from raw bytes and from `net/http`, followed by another request on the same connection.
- Every malformed chunked case above, and the limit.
- 2500 keys stream back in order.

## New in Go

`strconv.ParseInt(s, 16, 64)` · `io.Writer` wrappers · state machines for parsing · `net/url.ParseQuery`
