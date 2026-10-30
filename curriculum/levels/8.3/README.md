# 8.3 · Event Stream

Push changes to HTTP clients as server-sent events, and stop when they go away.

## Build

```go
// GET /v1/watch?prefix=user:   text/event-stream; each event: `id: <n>\nevent: put|delete|expire\ndata: <KV JSON>\n\n`; a `: heartbeat` comment every 15s
// Subscriber count visible in expvar `watchers`.
// The handler flushes after every event with http.NewResponseController(w).Flush(), which
// reaches the connection through 7.3's statusRecorder because that wrapper has Unwrap().
// A Flush error (or a false from a comma-ok w.(http.Flusher) on a writer that can't flush)
// means the client is gone: unsubscribe and return.
```

- The SSE handler with http.NewResponseController Flush
- Heartbeats
- Unsubscribe on r.Context().Done()

If the first event never shows up in `curl -N`, the usual cause is a `ResponseWriter` wrapper in the chain that hides `Flush`. `http.NewResponseController` follows `Unwrap()` down to the real writer; a wrapper without `Unwrap` makes it return `http.ErrNotSupported`, and the older `w.(http.Flusher)` comma-ok assertion simply reports `ok == false`. Both are the symptom of the trap described in 7.3. Also set `Content-Type: text/event-stream`, `Cache-Control: no-cache` and write the headers before the first flush; 7.4's write timeout must not apply to this handler, so use `SetWriteDeadline` through the same controller to extend it per event.

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/8.3/files/`, matching the earlier chapters.

## Pass when

curl -N prints an event within a second of a PUT; closing curl drops the watcher count; a 15 s silence carries a heartbeat.

The suite will cover:

- An httptest client receives put, delete and expire events for its prefix only
- Disconnect drops the subscriber (expvar watchers back to 0)
- Heartbeat line appears with a shortened interval option
- Events arrive through the full middleware chain from 7.3 (Unwrap in place), flushed one at a time, not only when the handler returns
- A ResponseWriter wrapper without Unwrap in front of the handler makes the handler end the stream with a logged error rather than hang

## Try it

```bash
curl -N 'localhost:8080/v1/watch?prefix=user:'   # then PUT /v1/kv/user:1 from another terminal
```

## Uses

- Broker (built in 8.2 · Fan-out)
- JSON API (built in 7.2 · JSON In and Out)
- Middleware chain (built in 7.3 · Signal Chain)

## In the wild

SSE as used by OpenAI/Anthropic streaming APIs and Kubernetes watch (chunked JSON); WebSockets when you need duplex

## New in Go

server-sent events · flushing through wrappers (Unwrap, NewResponseController, comma-ok Flusher) · per-event write deadlines · request context cancellation

Standard library: `net/http`, `fmt`, `time`, `encoding/json`
