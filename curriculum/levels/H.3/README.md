# H.3 · Conformance

> A circuit that works on the bench is a prototype. One that survives the test rig is a product.

## Your goal

Finish the hand-written server and prove it speaks the same HTTP as everyone else. Three jobs. Harden it: a handler must not be able to smuggle a second header into a response, and a request with two different `Content-Length` values must be refused. Add two behaviours real clients expect: `Expect: 100-continue` and a graceful `Shutdown`. Then prove it: feed the same bytes to your parser and to `net/http`'s and check they agree, let a fuzzer throw random input at `ReadRequest`, and run 50 clients at once. Last, read how `net/http` does it and write down three things it handles that yours doesn't.

## What you're using from before

**Keep-alive loop (H.1 · Keep-Alive).** The per-connection loop is where shutdown hooks in. A connection is *idle* while the loop waits in `Peek` for the next request and *busy* from the moment `ReadRequest` returns until the response is written. Shutdown closes idle ones at once and waits for busy ones. The loop also needs to know shutdown has started, so the response it is about to write can carry `Connection: close` and the loop can stop afterwards.

**Chunked codec (H.2 · Streaming).** The body reader is where `Expect: 100-continue` lands: the client has sent only the head and is waiting for `HTTP/1.1 100 Continue` before it sends the body, so that line must go out before the body reader reads its first byte.

**Shutdown (3.5 · Power Down).** `conntrack.Tracker` is the whole mechanism; you wrote it in 3.5 as its own package precisely so this server could reuse it. Wire the loop's states into it and `Shutdown` is mostly `Drain`:

```go
tr.Add(conn)              // on accept: idle
req, err := ReadRequest(br)
tr.SetBusy(conn, true)    // a request is in flight
// ... handler, response ...
tr.SetBusy(conn, false)   // back to waiting
tr.Remove(conn)           // when the loop ends

// Shutdown: stop accepting, then
return tr.Drain(ctx)      // closes idle now, busy as they finish,
                          // everything at ctx's deadline
```

## What's new here

**Header injection.** A handler sets a header from user input, say `w.Header().Set("Location", next)` where `next` came from a query string. If `next` contains `\r\nSet-Cookie: session=pwned`, and you write header values straight to the wire, the client sees a second header you never meant to send. The fix is at write time: never emit a value containing CR or LF, and never emit a name that is not a valid token (letters, digits and a few punctuation characters; no spaces). Either drop the header or replace the line breaks with spaces; the test only cares that `\nset-cookie` and `Bad Name` never reach the socket.

**Request smuggling.** Two parsers in a chain (a proxy and your server) that disagree about where a request body ends can be made to see different requests in the same bytes. `Content-Length: 5` and `Content-Length: 6` on one request is the classic bait; so is `Content-Length: 1, 2`. Refuse with 400 before the handler runs. H.2 already refused `Transfer-Encoding` together with `Content-Length` for the same reason.

**Interim responses.** A status in the 100s is not a final answer. `Expect: 100-continue` means "I have a body to send; tell me whether to bother". The server replies `HTTP/1.1 100 Continue\r\n\r\n` (no headers, no body), the client sends the body, and the real response follows later on the same connection. `net/http`'s client waits up to `ExpectContinueTimeout` for that line, so without it the test's upload stalls.

**Shutdown that tells the client.** `Shutdown` has the same contract as `wire.Server.Shutdown` from 3.5, plus one HTTP detail: a response written after shutdown began carries `Connection: close`, so the client doesn't try to reuse the connection. The loop needs a cheap way to ask "are we shutting down?" each time round. A channel that `Shutdown` closes, checked with a non-blocking `select`, is the idiom from 1.5; `sync/atomic`'s `atomic.Bool` is the other common answer:

```go
var closing atomic.Bool
closing.Store(true)   // in Shutdown
if closing.Load() {   // in the loop, before writing the response
	w.Header().Set("Connection", "close")
}
```

After `Shutdown`, `Serve` returns `ErrServerClosed`, a sentinel error callers test with `errors.Is`.

**Differential testing.** Two implementations of the same spec are fed identical inputs and must produce identical outputs. Here the oracle is `net/http.ReadRequest`. The test only compares fields where both parsers accepted the input, but it also fails when one accepts and the other rejects, so those nine requests are a list of edge cases to agree on: a path with `;params`, an empty header value, bare `\n` line endings, an absolute-form target, `OPTIONS *`. One subtlety: `net/http` keeps the path percent-encoded in `URL.EscapedPath()` (`%E2%9C%93` stays as is), and so must your `Request.Path`.

**Fuzzing.** A fuzz test is a property check driven by generated input. It lives in a `_test.go` file like any test, but takes `*testing.F`, seeds a corpus with `f.Add`, and hands the property to `f.Fuzz`:

```go
func FuzzParse(f *testing.F) {
	f.Add("GET / HTTP/1.1\r\n\r\n")
	f.Fuzz(func(t *testing.T, raw string) {
		req, err := ReadRequest(...)
		if err != nil {
			return // rejecting is always fine
		}
		// accepted input must satisfy every invariant
	})
}
```

The property is never "the output equals X"; it is "if you accepted this, these things hold": no empty method, no whitespace in the target, no CR/LF inside a header value. A plain `go test` runs only the seeds. `go test -fuzz FuzzLH_3_ReadRequest -run '^$' ./httpraw` mutates them for as long as you let it, and when it finds a failing input it writes the bytes to `testdata/fuzz/FuzzLH_3_ReadRequest/<hash>` and tells you how to re-run that one case. Those files are tests now; commit them.

**Reading the standard library.** The source is on your disk and it is ordinary Go. `go env GOROOT` prints the root; `net/http` is under `src/net/http/`. `go doc net/http.Server.Shutdown` prints a symbol's documentation without opening the file. `server.go` is long; `conn.serve`, `response.finishRequest` and `Server.Shutdown` are the three functions that correspond to what you just built.

## What to do

1. At response-write time, drop (or sanitise) any header whose name isn't a valid token or whose value contains `\r` or `\n`.
2. In request setup, reject a request with more than one `Content-Length` value, as repeated headers or as a comma list, unless every value is the same. 400, before the handler.
3. When a request carries `Expect: 100-continue`, write `HTTP/1.1 100 Continue\r\n\r\n` to the connection before anything reads the body.
4. Add `ErrServerClosed` and `Shutdown(ctx)`. Track every connection in a `conntrack.Tracker`, busy from `ReadRequest` returning to the response being written. `Shutdown` closes the listener, flips the closing flag, drains the tracker, and returns `ctx.Err()` if the deadline hit first. `Serve` returns `ErrServerClosed` after `Shutdown`.
5. Make the loop set `Connection: close` on a response written during shutdown, and exit after it.
6. Run the differential test and fix each disagreement in `ReadRequest`. Then run the fuzzer for a minute or two and fix what it finds.
7. Read `conn.serve`, `response.finishRequest` and `Server.Shutdown` in `$(go env GOROOT)/src/net/http/server.go`. Write down, anywhere you like, three things they handle that your server doesn't.

## Example

`Expect: 100-continue` as curl shows it (the `>` lines are sent, `<` received; other headers trimmed):

```bash
$ printf 'hello' | curl -sv -H 'Expect: 100-continue' -T - \
    localhost:8080/v1/kv/greeting 2>&1 | grep '^[<>] [A-Z]'
> PUT /v1/kv/greeting HTTP/1.1
> Expect: 100-continue
> Transfer-Encoding: chunked
< HTTP/1.1 100 Continue
< HTTP/1.1 204 No Content
```

The fuzzer finding something:

```bash
$ go test ./httpraw -run '^$' -fuzz FuzzLH_3_ReadRequest \
    -fuzztime 60s
fuzz: elapsed: 0s, gathering baseline coverage: 0/5 completed
fuzz: elapsed: 3s, execs: 141213 (47071/sec), new interesting: 23
--- FAIL: FuzzLH_3_ReadRequest (3.41s)
    lh_3_conformance_test.go:94: header value with CR/LF accepted: "b\r"

    Failing input written to testdata/fuzz/FuzzLH_3_ReadRequest/7a1c...
    To re-run:
    go test -run=FuzzLH_3_ReadRequest/7a1c... ./httpraw
FAIL
```

Here the parser was given the header line `A: b\r\r\n`. It stripped the final `\r\n` but kept the stray `\r` before it as part of the value. After the fix, the saved input runs with every `go test` and the fuzzer reports only `PASS` lines until `-fuzztime` runs out.

## Done when

- [ ] Nine requests parsed by both `ReadRequest` and `net/http.ReadRequest` agree on method, escaped path, query and every header, and on which ones to reject
- [ ] The fuzzer runs for a minute without a failure, and any input it did find is fixed and saved under `testdata/fuzz`
- [ ] 50 clients each doing 20 `PUT` then `GET` pairs over keep-alive connections all read back what they wrote
- [ ] `net/http`'s client follows a 302 through your server, and an upload with `Expect: 100-continue` arrives in full
- [ ] During `Shutdown`, an idle keep-alive connection closes, an in-flight request finishes with `Connection: close`, `Serve` returns `ErrServerClosed`, and a handler that never returns makes `Shutdown` give up with `context.DeadlineExceeded`
- [ ] A header value containing `\r\n` never produces a new header line on the wire; a header name with a space is not sent
- [ ] Two different `Content-Length` headers, or a `1, 2` list, are 400 and the handler never runs

## Reference

The exact contract the tests hold you to.

```go
// httpraw/server.go additions
var ErrServerClosed = errors.New("httpraw: server closed")

// Shutdown has the same contract as wire.Server.Shutdown (3.5): stop
// accepting, close idle keep-alive connections, let in-flight requests
// finish (their responses carry Connection: close), force-close at the
// context deadline and return ctx.Err(). After Shutdown, Serve returns
// ErrServerClosed. Build it on conntrack.Tracker.
func (s *Server) Shutdown(ctx context.Context) error

// Hardening:
//   - Expect: 100-continue: send HTTP/1.1 100 Continue\r\n\r\n before
//     reading the body.
//   - Never let a header value containing CR or LF, or an invalid
//     header name, reach the wire.
//   - Two Content-Length headers with different values, or a
//     comma-separated list with different values, is 400 (request
//     smuggling bait).
//   - Request.Path stays percent-encoded; net/http compares with
//     URL.EscapedPath().
```

Decisions the tests rely on:

- A connection waiting for its next request is idle; one between `ReadRequest` returning and its response being written is busy.
- `Shutdown` with a handler that never returns gives up at the context deadline and returns `context.DeadlineExceeded`.

## The tests check

- Differential parsing: 9 requests go through both your ReadRequest and net/http's; where both accept, method, escaped path, query and headers must agree
- FuzzLH_3_ReadRequest: accepted requests never have empty Method/Proto/Path, stray whitespace in the request line, headers Get can't find, or CR/LF in header values
- 50 clients x 20 PUT/GET pairs through keep-alive connections
- net/http's client follows a 302 through your server; Expect: 100-continue gets `HTTP/1.1 100 Continue` before the body is read
- Shutdown: an idle keep-alive connection closes, an in-flight request finishes with Connection: close, Serve returns ErrServerClosed; a stuck handler makes Shutdown return context.DeadlineExceeded at its deadline
- A header value containing CR/LF never injects a new header on the wire; a header name with a space is not sent
- Two different Content-Length headers, or a `1, 2` list, are 400 without calling the handler

## Try it

```bash
./burrow watch
go test ./httpraw -run '^$' -fuzz FuzzLH_3_ReadRequest -fuzztime 60s
printf 'POST / HTTP/1.1\r\nContent-Length: 5\r\n'\
'Content-Length: 6\r\n\r\nhello!' | nc localhost 8080   # 400
go doc net/http.Server.Shutdown
# then search for conn.serve:
less "$(go env GOROOT)/src/net/http/server.go"
```

## In the wild

h2spec and the HTTP/1.1 conformance suites that nginx and Envoy run

## New in Go

differential testing · running the fuzzer and testdata/fuzz corpora · reading the standard library with go doc and GOROOT · header injection and request smuggling · 1xx interim responses · atomic.Bool

Standard library: `net/http (tests only)`, `testing`, `sync/atomic`
