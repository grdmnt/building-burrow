# 8.3 · Event Stream

Push changes to HTTP clients as server-sent events, and stop when they go away.

## Build

```go
// GET /v1/watch?prefix=user:   text/event-stream; each event: `id: <n>\nevent: put|delete|expire\ndata: <KV JSON>\n\n`; a `: heartbeat` comment every 15s
// Subscriber count visible in expvar `watchers`.
```

- The SSE handler with http.NewResponseController Flush
- Heartbeats
- Unsubscribe on r.Context().Done()

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/8.3/files/`, matching the earlier chapters.

## Pass when

curl -N prints an event within a second of a PUT; closing curl drops the watcher count; a 15 s silence carries a heartbeat.

The suite will cover:

- An httptest client receives put, delete and expire events for its prefix only
- Disconnect drops the subscriber (expvar watchers back to 0)
- Heartbeat line appears with a shortened interval option

## Try it

```bash
curl -N 'localhost:8080/v1/watch?prefix=user:'   # then PUT /v1/kv/user:1 from another terminal
```

## Uses

- Broker (built in 8.2 · Fan-out)
- JSON API (built in 7.2 · JSON In and Out)

## In the wild

SSE as used by OpenAI/Anthropic streaming APIs and Kubernetes watch (chunked JSON); WebSockets when you need duplex

## New in Go

server-sent events · flushing · request context cancellation

Standard library: `net/http`, `fmt`, `time`, `encoding/json`
