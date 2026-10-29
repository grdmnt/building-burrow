# H.2 · Streaming

Send a response before you know how long it is, and accept uploads that don't say.

## Build

```go
func (w *ResponseWriter) Flush()
var ErrNotImplemented // 501 for unknown transfer codings
```

- 64 KiB buffer then switch to chunked
- Chunked reader with limits
- Streaming keys

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/H.2/files/`, matching the earlier chapters.

## Pass when

Flushed data reaches the client while the handler is still blocked, chunked uploads decode, every malformed case is rejected, and 2,500 keys stream back in order.

The suite will cover:

- Flush streams before return
- 1 MiB goes chunked, 60 KB keeps Content-Length
- Chunked uploads raw and from net/http
- Bad hex, overflow, missing CRLF, TE+CL conflict

## Uses

- Keep-alive loop (built in H.1 · Keep-Alive)

## In the wild

net/http/internal.chunkedReader

## New in Go

chunk framing · hex lengths · io.Writer wrappers

Standard library: `bufio`, `strconv`, `io`
