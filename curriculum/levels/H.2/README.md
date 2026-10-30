# H.2 · Streaming

> You can't put a length on the envelope when you haven't finished writing the letter.

## Your goal

Your `ResponseWriter` buffers the whole body, counts it, and sends `Content-Length` with the head. That fails for two kinds of handler: one that writes more than you want to hold in memory, and one that wants to send part of its answer *now* (a progress line, the first rows of a listing) before it knows the rest. HTTP's answer is *chunked transfer encoding*: send the body in pieces, each with its own length in front, and a zero-length piece to say "the end". Teach the writer to switch to chunks, teach the request side to decode chunked uploads, and make `GET /v1/keys` stream.

## What you're using from before

**Keep-alive loop (H.1 · Keep-Alive).** The per-connection loop reads a request, calls the handler, writes the response, drains anything unread, and goes round again. Chunked encoding exists so that loop still works when a body has no `Content-Length`: the final `0\r\n\r\n` tells the reader exactly where the body ends and the next request begins. Two parts of the loop change:

```go
// H.1: Request.Body was a LimitReader over Content-Length bytes.
// Now it may be your chunked reader instead; the loop doesn't care.
body := bodyReader(req, br) // length-delimited or chunked
req.Body = body
handler.ServeHTTP(w, req)
drain(body)                 // reads through the 0 chunk and trailers
```

The drain step from H.1 must read *through* the chunked body to its terminator, not a fixed number of bytes. And for HTTP/1.0, a streamed response has no terminator at all, so the loop ends by closing the connection.

## What's new here

**Chunk framing.** A chunked body is a sequence of chunks, each `<size in hex>\r\n<bytes>\r\n`, ended by a zero-size chunk and a blank line. The word `hello` as one chunk is:

```
5\r\nhello\r\n
```

and a full body of `hello` then ` world` then the end is `5\r\nhello\r\n6\r\n world\r\n0\r\n\r\n`. The size line may carry *extensions* after a semicolon (`5;name=value`) and the final chunk may be followed by *trailers*, header lines after the body. You ignore both, but you must parse past them. Writing a chunk is one `fmt.Fprintf(w, "%x\r\n", len(p))`, the bytes, and `\r\n`.

**Hex lengths, safely.** `strconv.ParseUint(s, 16, 64)` parses a hex string. Use the unsigned version: `ParseInt` happily accepts `-5`, which is not a chunk size. A size that does not fit in 64 bits returns an error you can test with `errors.Is(err, strconv.ErrRange)`, and since `int64` holds 15 hex digits with room to spare, anything longer is a 400 before you even parse it. The empty string and `zz` are errors too.

**An `io.Writer` that changes what passes through.** Until now `ResponseWriter.Write` appended to a `bytes.Buffer`. A writer can instead transform data and hand it to another writer. The shape is a struct holding the inner writer:

```go
type upper struct{ w io.Writer }

func (u upper) Write(p []byte) (int, error) {
	return u.w.Write(bytes.ToUpper(p))
}
```

A chunked writer has the same shape: it frames `p` with its hex length and CRLFs and writes that to the connection. Your `ResponseWriter` now has two modes. *Buffering*: nothing has been sent, bytes pile up in the buffer, and if the handler finishes under 64 KiB the response goes out with `Content-Length` exactly as before. *Streaming*: the head has already gone out with `Transfer-Encoding: chunked`, and every `Write` is framed and sent at once. Two things move it from the first mode to the second: the buffer passing 64 KiB, or the handler calling `Flush`.

**Flush.** `Flush()` means "send everything you have right now". If the head hasn't been sent, send it, and since the body isn't finished the length is unknown, so it goes out chunked. If the connection is wrapped in a `bufio.Writer`, flush that too, or the bytes sit in your own buffer and the client sees nothing. The test for this has the handler block on a channel after `Flush` and checks the client already received the first line.

**HTTP/1.0 has no chunks.** An HTTP/1.0 client doesn't understand `Transfer-Encoding`. For it, a streamed body is *close-delimited*: send the head with no length, send the bytes as they come, and close the connection when the handler returns. The close is the terminator.

**A reader that is a state machine.** Decoding chunks on the request side is an `io.Reader` whose `Read` method behaves differently depending on where it is in the framing. Keep the position as a field and switch on it:

```go
type chunked struct {
	br    *bufio.Reader
	// expecting a size line, inside a chunk, expecting CRLF, done
	state int
	left  int64 // bytes still to deliver from the current chunk
	total int64 // bytes delivered so far, checked against MaxBodyBytes
}
```

Each `Read` call advances through as many states as it needs to produce some bytes or reach the end. When the size is `0`, skip trailer lines until the blank line and report `io.EOF`. A missing `\r\n` after a chunk's bytes or a bad size is an error, and the handler sees it from `Read`; a well-behaved handler answers 400. Passing the limit is different: the client must get a 413 even if the handler answered 400 or ignored the error. The response is still buffered at that point, so have the reader remember that it hit the limit, and after the handler returns, throw the buffered response away and send 413 instead, as you did for a panic in 6.2.

**Which bodies are chunked.** `Transfer-Encoding: chunked` on a request means chunked body, and it must be the *last* coding listed. Any other coding (`gzip`) you don't implement: 501 Not Implemented, with the `ErrNotImplemented` sentinel. `Transfer-Encoding` together with `Content-Length` is ambiguous (it is the classic request smuggling setup) and is a 400.

## What to do

1. Add `Flush()` to `ResponseWriter` and the `ErrNotImplemented` sentinel.
2. Give the writer a streaming mode. When the buffer passes 64 KiB, or on `Flush`, send the head: with `Transfer-Encoding: chunked` for HTTP/1.1, with no length for HTTP/1.0. From then on each `Write` goes straight to the connection, chunk-framed for HTTP/1.1, raw for HTTP/1.0.
3. When a streamed handler returns, finish the body: `0\r\n\r\n` for HTTP/1.1 (the connection stays reusable), or close the connection for HTTP/1.0.
4. Keep the old path for small bodies: a handler that writes under 64 KiB and never flushes still gets `Content-Length`.
5. Write the chunked body reader. Decode sizes with `ParseUint` base 16, skip extensions and trailers, require `\r\n` after each chunk's bytes, count the bytes against `MaxBodyBytes`.
6. In the request setup, pick the body reader from the headers: `Transfer-Encoding` with `Content-Length` is 400, a coding other than chunked is 501, chunked not last is 400, otherwise chunked means your new reader.
7. Make H.1's drain read through the chunked body to its end before the next request.
8. Change `KVRoutes` so `GET /v1/keys` writes keys as it goes and calls `Flush` every so often, instead of building the whole listing first.

## Example

A streamed response, raw, with two keys stored. The key list is `a\nb\n`, four bytes, so it arrives as one chunk of size `4` and then the end marker:

```bash
$ printf 'GET /v1/keys HTTP/1.1\r\nConnection: close\r\n\r\n' \
    | nc localhost 8080
HTTP/1.1 200 OK
Connection: close
Content-Type: text/plain; charset=utf-8
Transfer-Encoding: chunked
Date: Sat, 31 Oct 2026 03:00:00 GMT

4
a
b

0

```

A chunked upload, the value `hello` sent as two pieces:

```bash
$ printf 'PUT /v1/kv/greeting HTTP/1.1\r\n'\
'Transfer-Encoding: chunked\r\nConnection: close\r\n\r\n'\
'3\r\nhel\r\n2\r\nlo\r\n0\r\n\r\n' | nc localhost 8080
HTTP/1.1 204 No Content
Connection: close
ETag: "3"
Date: Sat, 31 Oct 2026 03:00:00 GMT

$ curl localhost:8080/v1/kv/greeting
hello
```

A handler that flushes, as a client sees it. `curl -N` turns off curl's own buffering, so the first line appears before the handler has finished:

```go
func(w *httpraw.ResponseWriter, r *httpraw.Request) {
	w.Write([]byte("starting\n"))
	w.Flush() // "starting" is on the wire now
	time.Sleep(2 * time.Second)
	w.Write([]byte("done\n"))
}
```

## Done when

- [ ] A handler that writes, flushes and then blocks: the client has the first part while the handler is still blocked, and the response says `Transfer-Encoding: chunked`
- [ ] A 1 MiB response is chunked and the connection can be used for another request afterwards; a 60 KB response still has `Content-Length`
- [ ] An HTTP/1.0 client gets the streamed bytes with no `Transfer-Encoding`, and the connection closes to end the body
- [ ] A chunked upload with a chunk extension and a trailer decodes to the right bytes, and the next request on the connection still parses; an upload from `net/http` with unknown length echoes back
- [ ] Bad hex, a size that overflows, a missing CRLF after a chunk, and a negative size are each 400
- [ ] A chunked body that grows past `MaxBodyBytes` is 413
- [ ] `Transfer-Encoding` with `Content-Length` is 400; `gzip` is 501; `chunked, gzip` is 400; the connection closes after each
- [ ] `GET /v1/keys?prefix=user:` with 2,500 matching keys streams them chunked, one per line, sorted, as `text/plain`

## Reference

The exact contract the tests hold you to.

```go
// httpraw/response.go additions
func (w *ResponseWriter) Flush() // send what's buffered now

// -> 501 for a transfer coding other than chunked
var ErrNotImplemented = errors.New("httpraw: not implemented")

// Responses:
//   - Buffer up to 64 KiB. If the handler writes more, or calls Flush
//     before finishing, switch to Transfer-Encoding: chunked (HTTP/1.1)
//     or a close-delimited body (HTTP/1.0).
//   - Chunked: hex length, CRLF, bytes, CRLF; a final 0\r\n\r\n. The
//     connection stays reusable.
//   - Small bodies keep Content-Length.
// Requests:
//   - Transfer-Encoding: chunked bodies are decoded. Ignore chunk
//     extensions (;name=value) and trailers.
//   - Transfer-Encoding with Content-Length: 400. A coding other than
//     chunked: 501. chunked not last: 400.
//   - Bad hex, an overflowing size, a negative size, or a missing CRLF
//     after a chunk: 400.
//   - A chunked body growing past MaxBodyBytes: 413.

// KVRoutes (6.3) changes: GET /v1/keys?prefix= now streams, writing
// keys as it goes and calling Flush periodically, so a large listing
// arrives chunked instead of being buffered.
```

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

## Try it

```bash
./burrow watch
printf 'GET /v1/keys HTTP/1.1\r\nConnection: close\r\n\r\n' \
  | nc localhost 8080
curl -N localhost:8080/v1/keys      # -N: show chunks as they land
printf 'hi' | curl -T - localhost:8080/v1/kv/x  # sent chunked
```

## In the wild

net/http/internal.chunkedReader

## New in Go

chunk framing · hex lengths with strconv.ParseUint · io.Writer wrappers · a state-machine io.Reader · Flush through bufio.Writer

Standard library: `bufio`, `strconv`, `io`
