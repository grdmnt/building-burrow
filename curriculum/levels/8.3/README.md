# 8.3 · Event Stream

> Stop making clients ask. Keep the connection open and tell them when something changes.

## Your goal

Add `GET /v1/watch?prefix=` to the HTTP API. A client opens it once and the response never ends: each time a key under the prefix is written, deleted or expires, the server writes one more event down the same response and flushes it. The format is *server-sent events* (SSE), a plain-text streaming format every browser and `curl -N` understands. When the client goes away, the handler must notice, unsubscribe from the broker and return, so a closed tab doesn't leave a subscriber behind forever.

## What you're using from before

**Broker (8.2 · Fan-out).** `Subscribe(prefix)` gives a channel of events and a stop function. The handler subscribes once per request, ranges over the channel, and calls stop on the way out. A closed channel means the broker dropped this subscriber for being slow, and the handler ends the stream:

```go
ch, stop := s.Broker.Subscribe(r.URL.Query().Get("prefix"))
defer stop()
```

**JSON API (7.2 · JSON In and Out).** The `data:` line of each event is a `KV` encoded with `json.Marshal`, the same type `GET /v1/kv/{key}` returns with `Accept: application/json`, so clients already know the shape. Errors on this endpoint (a wrong method, a bad prefix) are `ErrorResponse` like everywhere else.

```go
kv := toKV(ev.Entry) // the same conversion 7.2's GET handler uses
body, _ := json.Marshal(kv)
fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.ID, ev.Type, body)
```

**Middleware chain (7.3 · Signal Chain).** Every request, this one included, runs inside `Chain(mux, Recover(), RequestID(), AccessLog(l))`. `AccessLog` wraps the `ResponseWriter` in a `statusRecorder`. That wrapper has an `Unwrap()` method, and this level is the reason: without it, nothing you write would reach the client until the handler returned.

## What's new here

**Server-sent events.** SSE is text over a normal HTTP response with `Content-Type: text/event-stream`. An event is a few `name: value` lines followed by a blank line. A line starting with `:` is a comment, which is how a server keeps the connection warm without sending an event:

```text
id: 7
event: put
data: {"key":"user:1","value":"YW5u","version":1}

: heartbeat

id: 8
event: delete
data: {"key":"user:1","value":"","version":0}

```

Set `Cache-Control: no-cache` too, and call `w.WriteHeader(200)` before the first event so the headers go out even if no event arrives for a while.

**Flushing through wrappers.** `net/http` buffers what a handler writes and sends it when the handler returns. A stream needs each event pushed out now. The modern way is `http.NewResponseController(w).Flush()`. The controller looks for a `Flush` method on `w`; when `w` is a wrapper with `Unwrap() http.ResponseWriter`, it follows that chain down to the real connection. That is why 7.3's `statusRecorder` has `Unwrap`.

```go
rc := http.NewResponseController(w)
if err := rc.Flush(); err != nil {
	// http.ErrNotSupported: a wrapper in the chain has no Unwrap and
	// no Flush. Log it and return; this stream can't work.
	return
}
```

The older form is a comma-ok type assertion, `f, ok := w.(http.Flusher)`. It only sees the outermost writer, so a wrapper without a `Flush` method makes `ok` false even though the real connection could flush. Know it because you will meet it in other people's code; use the controller in yours.

**Per-event write deadlines.** 7.4 gave `http.Server` a `WriteTimeout` of 30 s. That clock starts when the request is read, so a stream would be cut off after 30 s no matter what. The same controller can move the deadline: before each write, `rc.SetWriteDeadline(time.Now().Add(d))` gives this event its own budget without lifting the timeout for every other handler. A write or flush that fails is the sign the client is gone.

**Request context cancellation.** `r.Context()` is cancelled when the client disconnects. `<-r.Context().Done()` in the same `select` as the event channel is how the handler finds out while it is waiting for the next event, rather than only at the next failed write:

```go
for {
	select {
	case <-r.Context().Done():
		return // client left
	case ev, ok := <-ch:
		if !ok {
			return // broker dropped us
		}
		// write the event and flush
	case <-heartbeat.C:
		// write ": heartbeat\n\n" and flush
	}
}
```

## What to do

1. Give `api.Server` a `Broker *watch.Broker` field and register `GET /v1/watch` on the mux.
2. In the handler: read `prefix`, subscribe, `defer stop()`.
3. Write the three headers (`Content-Type: text/event-stream`, `Cache-Control: no-cache`, status 200) and flush once, so the client sees the stream has opened.
   - If `Flush` returns an error, log it with the request's logger and return. The stream cannot work through a writer that can't flush.
4. Loop with a `select` over the event channel, a heartbeat ticker and `r.Context().Done()`.
   - For an event: set a write deadline, write `id:`, `event:`, `data:` and a blank line, flush. Any write or flush error ends the handler.
   - For a heartbeat tick: write `: heartbeat` and a blank line, flush. Default 15 s; make the interval a field on `api.Server` so a test can shorten it.
   - For a done context or a closed channel: return.
5. Add an expvar `watchers` gauge to the admin counters from 7.6. Increment on subscribe, decrement on the way out, so you can see live watchers.
6. Make sure `Timeout` from 7.4 does not wrap this route with a per-request deadline, or every stream ends after the budget.

## Example

Terminal one:

```bash
$ curl -N 'localhost:8080/v1/watch?prefix=user:'
id: 1
event: put
data: {"key":"user:1","value":"YW5u","version":1}

: heartbeat

id: 2
event: delete
data: {"key":"user:1","value":"","version":0}

```

Terminal two, during that:

```bash
$ curl -s -X PUT localhost:8080/v1/kv/user:1 -d ann
$ sleep 20
$ curl -s -X DELETE localhost:8080/v1/kv/user:1
$ curl -s localhost:7071/debug/vars | jq .watchers
1
```

Press Ctrl-C in terminal one and `watchers` drops to 0 within a second.

## Done when

- [ ] A client with `prefix=user:` receives `put`, `delete` and `expire` events for `user:` keys only, each with `id`, `event` and a JSON `data` line
- [ ] Closing the client connection removes the subscriber: `watchers` is back to 0
- [ ] With the heartbeat interval shortened, a `: heartbeat` comment arrives during silence
- [ ] Through the full 7.3 chain, each event reaches the client on its own, not only when the handler returns
- [ ] A `ResponseWriter` wrapper without `Unwrap` in front of the handler makes it log an error and end the stream instead of hanging

## Reference

```go
// GET /v1/watch?prefix=user:
//   200 text/event-stream; Cache-Control: no-cache
//   each event:  id: <n>\nevent: put|delete|expire\ndata: <KV JSON>\n\n
//   a `: heartbeat` comment every 15s of silence
// Subscriber count visible in expvar `watchers`.
```

- The handler flushes after every event with `http.NewResponseController(w).Flush()`, which reaches the connection through 7.3's `statusRecorder` because that wrapper has `Unwrap()`.
- A Flush error (or `ok == false` from a comma-ok `w.(http.Flusher)` on a writer that can't flush) means the stream can't work: unsubscribe, log, return.
- Unsubscribe when `r.Context()` is done.
- 7.4's write timeout must not apply to this handler: extend the deadline per event with `SetWriteDeadline` through the same controller.

## The tests check

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/8.3/files/`, matching the earlier chapters.

Pass when: `curl -N` prints an event within a second of a PUT; closing curl drops the watcher count; a 15 s silence carries a heartbeat.

The suite will cover:

- An httptest client receives put, delete and expire events for its prefix only
- Disconnect drops the subscriber (expvar watchers back to 0)
- Heartbeat line appears with a shortened interval option
- Events arrive through the full middleware chain from 7.3 (Unwrap in place), flushed one at a time, not only when the handler returns
- A ResponseWriter wrapper without Unwrap in front of the handler makes the handler end the stream with a logged error rather than hang

## Try it

```bash
./burrow watch
go run ./cmd/burrowd serve -http 127.0.0.1:8080 -admin 127.0.0.1:7071 &
curl -N 'localhost:8080/v1/watch?prefix=user:'   # then PUT elsewhere
```

## In the wild

SSE as used by OpenAI/Anthropic streaming APIs and Kubernetes watch (chunked JSON); WebSockets when you need duplex.

## New in Go

server-sent events · flushing through wrappers (Unwrap, NewResponseController, comma-ok Flusher) · per-event write deadlines · request context cancellation

Standard library: `net/http`, `fmt`, `time`, `encoding/json`
