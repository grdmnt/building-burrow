# 8.4 · Resume

Clients that reconnect pick up exactly where they left off. The hard part is the handover from replayed events to live ones.

## Build

```go
func NewBroker(buffer, history int) *Broker
func (b *Broker) SubscribeFrom(prefix string, afterID uint64) (<-chan Event, func(), error) // ErrTooOld if afterID is below the buffer
// Watch API honours Last-Event-ID; too old -> `event: reset` first, then live.
```

- Ring buffer with head index
- SubscribeFrom: subscribe, replay, skip duplicates
- Last-Event-ID in the handler

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/8.4/files/`, matching the earlier chapters.

## Pass when

Kill the client, write 5 keys, reconnect with Last-Event-ID: the 5 missed events arrive first, then live ones, with no gaps or duplicates; an ID older than the buffer yields a reset event.

The suite will cover:

- No gaps/duplicates across 100 reconnects under a concurrent writer (IDs are contiguous)
- ErrTooOld / reset event
- Ring buffer wraparound correctness

## Uses

- Watch API (built in 8.3 · Event Stream)

## In the wild

Kafka consumer offsets; Kubernetes resourceVersion and `410 Gone`; SSE's Last-Event-ID is the standard's own answer

## New in Go

ring buffers · race-free handover · idempotent replay

Standard library: `sync`, `strconv`, `net/http`
