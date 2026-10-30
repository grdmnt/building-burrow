# H.1 · Keep-Alive

> One phone call, many questions. Hang up when the caller says goodbye, goes quiet, or has asked enough.

## Your goal

Right now your hand-written HTTP server answers one request and then closes the TCP connection. Real clients expect *keep-alive*: many requests, one connection, which is the HTTP/1.1 default. Give each connection a loop that reads request after request, follow the `Connection` header rules, skip any request body the handler didn't read, and add timeouts so a client that goes quiet or never finishes its headers can't hold a goroutine forever.

## What you're using from before

**Router (6.3 · Router).** `httpraw.Router` maps a method and path pattern to a handler, and `KVRoutes` mounts the raw KV API on it. Nothing about routing changes here. The router is just the `Handler` your `Server` calls, and this level changes what happens around that call: the `Server` now calls it once per request instead of once per connection.

```go
r := httpraw.NewRouter()
httpraw.KVRoutes(r, st)
srv := &httpraw.Server{Handler: r, IdleTimeout: 30 * time.Second}
srv.Serve(ln) // each connection now serves many requests
```

The per-connection goroutine, `ReadRequest`, `ResponseWriter` and `Request.Body` from 6.1 and 6.2 are still the pieces inside; you are rearranging them into a loop.

## What's new here

**The request loop.** In 6.1 the per-connection code was: read one request, call the handler, write the response, close. Now it is a `for` loop: read, handle, write, then decide whether to go round again. Because every request on the connection is read from the same `bufio.Reader`, in order, *pipelining* (a client sending three requests back to back without waiting for replies) works by itself: you read them one at a time and answer them in the same order.

**Deadlines are points in time, not durations.** `net.Conn` has `SetReadDeadline(t time.Time)`. After `t`, any `Read` on that connection returns an error immediately. It is absolute, it stays set until you change it, and you re-arm it whenever a new phase starts. This level has two phases with different budgets:

- *Idle*: waiting for the first byte of the next request. Budget `IdleTimeout` (30 s by default). A kept-alive connection with nothing to say is normal, so this is generous.
- *Header*: once a byte has arrived, the whole request head must finish within `ReadHeaderTimeout` (10 s). This is what stops a "slowloris" client that sends one header byte a minute.

To switch from one to the other you need to know when the first byte arrives without consuming it. `bufio.Reader.Peek(n)` blocks until `n` bytes are buffered and returns them without advancing the reader:

```go
conn.SetReadDeadline(time.Now().Add(30 * time.Second))
_, err := br.Peek(1) // returns when a byte arrives, or at the deadline
```

Re-arming per request is what makes "timeouts reset per request": a client that sends a request every 120 ms under a 200 ms idle timeout stays connected forever.

**Telling a timeout from a dead connection.** A deadline error satisfies the `net.Error` interface and its `Timeout()` method returns true. The idiom is `errors.As` into an interface variable:

```go
var ne net.Error
if errors.As(err, &ne) && ne.Timeout() {
	// the client went quiet; io.EOF would mean it hung up
}
```

Either way you close, but logging "idle timeout" and "client closed" differently makes the server far easier to debug.

**Draining a body you didn't read.** If a handler ignores a `POST` body, those bytes are still sitting in the reader, and the next `ReadRequest` would parse `hello world` as a request line. Before looping, read and throw away whatever is left. `io.Discard` is a `Writer` that drops everything, and `io.LimitReader(r, n)` is a `Reader` that stops after `n` bytes, so copying one into the other skips at most `n` bytes:

```go
n, _ := io.Copy(io.Discard, io.LimitReader(src, 10))
// n is how many bytes were actually skipped, at most 10
```

The cap matters: a client could send a 10 GB body it knows you'll ignore. If more than 256 KiB is left, it is cheaper to close the connection than to read it.

**Connection header tokens.** The header is a comma-separated list and tokens are case-insensitive, so `connection: Keep-Alive, CLOSE` means close. `strings.Split` on `,`, `strings.TrimSpace` each part, and compare with `strings.EqualFold` (case-insensitive string equality) rather than lower-casing everything.

## What to do

1. Add `IdleTimeout`, `ReadHeaderTimeout` and `MaxRequestsPerConn` to `Server`, each with its default when zero (30 s, 10 s, 100).
2. Turn the per-connection function into a loop. Each pass serves one request.
3. At the top of each pass: set the idle deadline, `Peek(1)`, then set the header deadline and call `ReadRequest`. Any error (timeout, `io.EOF`, malformed) ends the loop. Malformed input still gets its 400/505/431 from 6.1 before closing.
4. Before calling the handler, decide whether this is the last request on the connection:
   - HTTP/1.1 stays open unless the client's `Connection` header contains the `close` token.
   - HTTP/1.0 closes unless the client sent `Connection: keep-alive`; when it did, reply with `Connection: keep-alive` too.
   - The `MaxRequestsPerConn`-th request is always the last.
   - When closing, the response carries `Connection: close`.
5. After the handler returns and the response is written, drain what is left of the request body, up to 256 KiB. If there was more than that, close instead of looping.
6. Keep every connection on its own goroutine, as before, and make sure a closed connection is always closed exactly once.

## Example

Two requests on one connection, the second one asking to close. With your `httpraw` server listening on port 8080 and `KVRoutes` mounted, as in 6.3's Try it:

```bash
$ printf 'PUT /v1/kv/a HTTP/1.1\r\nContent-Length: 2\r\n\r\nhi'\
'GET /v1/kv/a HTTP/1.1\r\nConnection: close\r\n\r\n' | nc localhost 8080
HTTP/1.1 204 No Content
ETag: "1"
Date: Sat, 31 Oct 2026 03:00:00 GMT

HTTP/1.1 200 OK
Connection: close
Content-Length: 2
Content-Type: application/octet-stream
ETag: "1"
Date: Sat, 31 Oct 2026 03:00:00 GMT

hi
```

Both responses come back on the same socket (header order is yours to choose). Without the `Connection: close`, `nc` would sit there until the idle timeout closed it from the server side, about 30 seconds later. curl shows the reuse explicitly when given two URLs:

```bash
$ curl -sv localhost:8080/v1/keys localhost:8080/v1/keys 2>&1 \
    | grep -i connection
* Connected to localhost (127.0.0.1) port 8080
* Re-using existing connection with host localhost
```

## Done when

- [ ] Two requests sent one after the other on one connection both get answers, and three sent at once come back in order
- [ ] `Connection: close` from the client is answered with `Connection: close` and then the connection closes; `connection: Keep-Alive, CLOSE` also closes
- [ ] HTTP/1.0 closes after one request, but stays open when the client sends `Connection: keep-alive`
- [ ] A `POST` whose body the handler ignored, or only half read, doesn't corrupt the next request
- [ ] An idle connection closes after `IdleTimeout`; a client that starts its headers and stops is dropped after `ReadHeaderTimeout`; a connection that keeps sending stays open past both
- [ ] With `MaxRequestsPerConn: 3`, the third response carries `Connection: close` and the connection closes right after it
- [ ] `net/http`'s client sends three requests over one connection

## Reference

The exact contract the tests hold you to.

```go
// httpraw/server.go additions
// Server gains:
//   IdleTimeout        time.Duration // between requests on a
//                                    // kept-alive conn; 0 means 30s
//   ReadHeaderTimeout  time.Duration // to receive a full request
//                                    // head; 0 means 10s
//   MaxRequestsPerConn int           // 0 means 100

// Connection rules:
//   - HTTP/1.1 connections stay open by default. HTTP/1.0 ones close
//     unless the client sends Connection: keep-alive (then reply with
//     it too).
//   - Connection: close from the client (tokens are case-insensitive,
//     comma-separated) means: reply with Connection: close, then close.
//   - Before reading the next request, skip whatever body the handler
//     didn't read. If more than 256 KiB is left, close instead.
//   - The last request allowed on a connection gets Connection: close.
```

Decisions the tests rely on:

- Timeouts reset per request: a connection that keeps sending requests inside the idle window stays open.
- The `MaxRequestsPerConn`-th response carries `Connection: close` and the server closes right after it.

## The tests check

- Two sequential requests on one connection; three pipelined requests answered in order
- Connection: close from the client is answered with Connection: close; `connection: Keep-Alive, CLOSE` closes (tokens are case-insensitive); HTTP/1.0 closes by default and stays open with Connection: keep-alive
- An ignored 11-byte body and a half-read body don't corrupt the next request
- IdleTimeout 100 ms closes an idle connection; ReadHeaderTimeout 150 ms drops a client that never finishes its headers; timeouts reset per request on a busy connection
- MaxRequestsPerConn 3: the third response carries Connection: close and the connection closes after it
- net/http's client reuses one connection for three requests

## Try it

```bash
./burrow watch
printf 'GET /v1/keys HTTP/1.1\r\n\r\nGET /v1/keys HTTP/1.1\r\n\r\n' \
  | nc localhost 8080                 # two responses, one socket
nc localhost 8080                     # say nothing; closes after 30 s
curl -sv localhost:8080/v1/keys localhost:8080/v1/keys 2>&1 \
  | grep -i re-using
```

## In the wild

net/http's conn.serve loop

## New in Go

SetReadDeadline per phase · bufio.Reader.Peek · net.Error.Timeout · io.Discard for draining · strings.EqualFold

Standard library: `net`, `time`, `errors`, `io`, `strings`
