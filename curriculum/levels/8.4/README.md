# 8.4 · Resume

> A client that drops off for five seconds should miss nothing and see nothing twice.

## Your goal

Let a watch client reconnect and pick up exactly where it left off. The broker keeps the last `history` events in memory. A client that comes back says "the last ID I saw was 41" with the standard SSE header `Last-Event-ID`, the server replays 42 onwards from memory, then hands over to live events. The hard part is the handover: an event published between "finish replaying" and "start listening" must not be lost, and an event that appears in both must not be sent twice. If the client was away so long that its ID has fallen out of the history, the server says so with a `reset` event, and the client knows it has to start over.

## What you're using from before

**Watch API (8.3 · Event Stream).** The `GET /v1/watch` handler and the `Broker` it subscribes to. This level changes both: `NewBroker` grows a `history` size and a `SubscribeFrom` method, and the handler reads `Last-Event-ID` and picks `SubscribeFrom` over `Subscribe` when it is present. The event loop, flushing and heartbeats stay as they are. Plain `Subscribe(prefix)` keeps working for callers that want live events only:

```go
after, _ := strconv.ParseUint(r.Header.Get("Last-Event-ID"), 10, 64)
ch, stop, err := s.Broker.SubscribeFrom(prefix, after)
if errors.Is(err, watch.ErrTooOld) {
	// write `event: reset` then fall back to a live Subscribe
}
```

## What's new here

**Ring buffers.** A fixed-size slice plus a "next write" index is a ring buffer: the newest event overwrites the oldest once the slice is full, and no memory is ever allocated after start-up.

```go
type ring struct {
	buf  []Event // len(buf) == history
	head int     // index the next event is written to
	n    uint64  // events written so far
}

func (r *ring) push(ev Event) {
	r.buf[r.head] = ev
	r.head = (r.head + 1) % len(r.buf)
	r.n++
}
```

Because event IDs are issued by the broker in order and every event is pushed, the oldest ID still in the buffer is a small calculation from `n` and `len(buf)`, and an event with ID `x` lives at a predictable index. Work that out on paper before coding it; the wraparound case (buffer full, head back at 0) is where it goes wrong.

**A race-free handover.** Replay-then-subscribe loses the event published in between. Subscribe-then-replay can deliver one event twice. The fix is to do both steps under the broker's lock, in this order: subscribe (so nothing published from now on is missed), copy the events after `afterID` out of the ring (so the replay is a snapshot taken at the same instant). Then release the lock, send the snapshot down the channel, and switch to live events, skipping any live event whose ID is at or below the last one replayed.

```go
b.mu.Lock()
sub := b.addSubscriber(prefix)     // live events start queueing here
replay := b.ring.after(afterID)    // snapshot under the same lock
b.mu.Unlock()
```

**Idempotent replay.** "Idempotent" means doing it twice has the same effect as doing it once. The client may reconnect with an ID it has already fully consumed, or the snapshot and the live channel may overlap by an event or two. Delivery is made idempotent by one rule: never send an ID lower than or equal to the last one sent. Keep `lastSent` per subscriber and compare before every send. With that rule, overlaps are harmless and the client sees a contiguous sequence.

## What to do

1. Change `NewBroker(buffer, history int)`. Add the ring to the broker and push every published event into it inside `Publish`, under the same lock that assigns the ID.
2. Add `ErrTooOld` and `SubscribeFrom(prefix, afterID)`.
   - `afterID` 0 means "everything you still have".
   - If `afterID` is below the oldest ID in the ring (and not 0), return `ErrTooOld` without subscribing.
   - Otherwise, under the lock: register the subscriber, snapshot the matching events with ID greater than `afterID`. Then start a goroutine (or do it inline before returning) that sends the snapshot to the channel, then forwards live events, dropping any with ID at or below the last one sent.
   - The subscriber's channel is still the one the caller ranges over; the replay and the live events arrive on it in one ordered sequence.
3. Keep `Subscribe(prefix)` as `SubscribeFrom(prefix, <current latest ID>)` or as a separate live-only path; either is fine as long as it never replays.
4. In the handler: parse `Last-Event-ID` with `strconv.ParseUint`. Absent or 0 means a fresh live stream. On `ErrTooOld`, write `event: reset` with an empty `data:` line, flush, then continue as a live stream from now.
5. Run the race detector. The handover is exactly the kind of bug `-race` finds and a plain run does not.

## Example

A client saw events up to ID 41, dropped off, and five keys were written while it was away:

```bash
$ curl -N -H 'Last-Event-ID: 41' 'localhost:8080/v1/watch?prefix=user:'
id: 42
event: put
data: {"key":"user:7","value":"...","version":1}

id: 43
event: put
...
id: 46
event: put
data: {"key":"user:11","value":"...","version":1}

id: 47
event: delete
data: {"key":"user:7","value":"","version":0}

```

Events 42 to 46 came from the ring; 47 was written after the reconnect and arrived live. The client cannot tell the difference, which is the point.

Too old to replay, with `history` 1000 and the broker at ID 5000:

```bash
$ curl -N -H 'Last-Event-ID: 3' 'localhost:8080/v1/watch?prefix=user:'
event: reset
data:

id: 5001
event: put
...
```

## Done when

- [ ] A writer runs continuously while a client reconnects 100 times with its last ID; the IDs it collects are contiguous: no gaps, no duplicates
- [ ] An `afterID` older than the buffer gives `ErrTooOld` from the broker and an `event: reset` on the wire, followed by live events
- [ ] The ring buffer is correct after wrapping around: with `history` 8 and 20 events published, `SubscribeFrom(prefix, 12)` replays exactly 13 to 20
- [ ] `go test -race ./watch ./api` is clean

## Reference

```go
package watch

var ErrTooOld = errors.New("watch: last id older than history")

func NewBroker(buffer, history int) *Broker

// SubscribeFrom replays every retained event with ID > afterID, then
// continues live, with no gaps and no duplicates. ErrTooOld if afterID
// is below the buffer.
func (b *Broker) SubscribeFrom(prefix string, afterID uint64) (
	<-chan Event, func(), error)

// GET /v1/watch honours Last-Event-ID. Too old -> `event: reset` first,
// then live.
```

- Ring buffer with a head index, filled by `Publish` under the ID lock
- `SubscribeFrom`: subscribe, replay, skip duplicates, all starting under one lock
- `Last-Event-ID` in the handler; `reset` when the ID is older than the buffer

## The tests check

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/8.4/files/`, matching the earlier chapters.

Pass when: kill the client, write 5 keys, reconnect with Last-Event-ID: the 5 missed events arrive first, then live ones, with no gaps or duplicates; an ID older than the buffer yields a reset event.

The suite will cover:

- No gaps/duplicates across 100 reconnects under a concurrent writer (IDs are contiguous)
- ErrTooOld / reset event
- Ring buffer wraparound correctness

## Try it

```bash
./burrow watch
go test -race -run . ./watch
# note the last id, then Ctrl-C:
curl -N 'localhost:8080/v1/watch?prefix=user:'
# write a few keys, then:
curl -N -H 'Last-Event-ID: <that id>' \
  'localhost:8080/v1/watch?prefix=user:'
```

## In the wild

Kafka consumer offsets; Kubernetes resourceVersion and `410 Gone`; SSE's Last-Event-ID is the standard's own answer.

## New in Go

ring buffers · race-free handover · idempotent replay

Standard library: `sync`, `strconv`, `net/http`
