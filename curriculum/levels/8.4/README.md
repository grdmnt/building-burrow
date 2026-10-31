# 8.4 · Resume

> A client that drops off for five seconds should miss nothing and see nothing twice.

## Your goal

Let a watch client reconnect and pick up exactly where it left off. The broker keeps the last `history` events in memory. A client that comes back says "the last ID I saw was 41" with the standard SSE header `Last-Event-ID`, the server replays 42 onwards from memory, then hands over to live events. The hard part is the handover: an event published between "finish replaying" and "start listening" must not be lost, and an event that appears in both must not be sent twice. If the server can't replay from the client's ID, because the client was away so long that the ID has fallen out of the history or because the server restarted since, it says so with a `reset` event, and the client knows it has to start over.

## What you're using from before

**Watch API (8.3 · Event Stream).** The `GET /v1/watch` handler and the `Broker` it subscribes to. This level changes both: `NewBroker` grows a `history` size (its options, `WithStartID` included, stay) and a `SubscribeFrom` method, and the handler reads `Last-Event-ID` and picks `SubscribeFrom` over `Subscribe` when it is present. The event loop, flushing and heartbeats stay as they are. Plain `Subscribe(prefix)` keeps working for callers that want live events only:

```go
after, _ := strconv.ParseUint(r.Header.Get("Last-Event-ID"), 10, 64)
ch, stop, err := s.Broker.SubscribeFrom(prefix, after)
if errors.Is(err, watch.ErrCannotResume) {
	// write `event: reset` then fall back to a live Subscribe
}
```

8.3 also made `burrowd`'s broker start at a boot-time ID, so IDs from before a restart are always below the restarted server's. That is what lets this level spot a client from before a restart.

## What's new here

**Ring buffers.** A fixed-size slice plus a "next write" index is a ring buffer: the newest event overwrites the oldest once the slice is full, and no memory is ever allocated after start-up.

```go
type ring struct {
	buf   []Event // len(buf) == history
	head  int     // index the next event is written to
	full  bool    // head has wrapped around at least once
	floor uint64  // start ID, then the last ID overwritten
}

func (r *ring) push(ev Event) {
	if r.full {
		r.floor = r.buf[r.head].ID // the oldest is about to go
	}
	r.buf[r.head] = ev
	r.head = (r.head + 1) % len(r.buf)
	if r.head == 0 {
		r.full = true
	}
}
```

IDs only go up, but they can skip: a caller may choose its own (chapter 10 uses log indexes), and `WithStartID` starts them high. So don't compute where ID `x` lives from `x`. Walk the ring from the oldest slot (`head` when full, 0 otherwise) and take the events with ID greater than the one asked for. Work the wraparound case (buffer full, head back at 0) out on paper before coding it; that is where it goes wrong.

**Telling the client to start over.** The broker can replay from `afterID` only if it still has every event after it. That holds when `floor <= afterID <= last`, where `floor` starts at the `WithStartID` value (0 without it) and then tracks the last overwritten event, and `last` is the newest ID published. Outside that window, `SubscribeFrom` returns `ErrCannotResume` and the handler sends `reset`. There are two ways to fall outside:

- **Below `floor`.** Events after `afterID` were overwritten, or were never in this process at all. With 8.3's boot-time start ID, a client from before a restart always lands here, even before the new process has published anything.
- **Above `last`.** This broker never published that ID. That is how a client notices a restart of a server whose IDs start again at 1 (a broker without `WithStartID`, as in most tests): its last ID is newer than anything the new process has sent.

```text
before the restart:  client saw 41, server reached 50
after, no start ID:  server at 7     41 > 7: reset
after, start ID S:   server at S+3  41 < S: reset
```

`afterID` 0 is never a reset: it means "everything you still have".

**A race-free handover.** Replay-then-subscribe loses the event published in between. Subscribe-then-replay can deliver one event twice. The fix is to do both steps under the broker's lock, in this order: subscribe (so nothing published from now on is missed), copy the events after `afterID` out of the ring (so the replay is a snapshot taken at the same instant). Then release the lock, send the snapshot down the channel, and switch to live events, skipping any live event whose ID is at or below the last one replayed.

```go
b.mu.Lock()
sub := b.addSubscriber(prefix)     // live events start queueing here
replay := b.ring.after(afterID)    // snapshot under the same lock
b.mu.Unlock()
```

**Idempotent replay.** "Idempotent" means doing it twice has the same effect as doing it once. The client may reconnect with an ID it has already fully consumed, or the snapshot and the live channel may overlap by an event or two. Delivery is made idempotent by one rule: never send an ID lower than or equal to the last one sent. Keep `lastSent` per subscriber and compare before every send. With that rule, overlaps are harmless and the client sees a contiguous sequence.

## What to do

1. Change `NewBroker(buffer, history int, opts ...BrokerOption)`. Add the ring to the broker, with `floor` set to the start ID, and push every published event into it inside `Publish`, under the same lock that settles the ID.
2. Add `ErrCannotResume` and `SubscribeFrom(prefix, afterID)`.
   - `afterID` 0 means "everything you still have".
   - If `afterID` is not 0 and is below `floor` or above the last published ID, return `ErrCannotResume` without subscribing.
   - Otherwise, under the lock: register the subscriber, snapshot the matching events with ID greater than `afterID`. Then start a goroutine (or do it inline before returning) that sends the snapshot to the channel, then forwards live events, dropping any with ID at or below the last one sent.
   - The subscriber's channel is still the one the caller ranges over; the replay and the live events arrive on it in one ordered sequence.
3. Keep `Subscribe(prefix)` as `SubscribeFrom(prefix, <current latest ID>)` or as a separate live-only path; either is fine as long as it never replays.
4. In the handler: parse `Last-Event-ID` with `strconv.ParseUint`. Absent or 0 means a fresh live stream. On `ErrCannotResume`, write `event: reset` with an empty `data:` line, flush, then continue as a live stream from now.
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

The same `reset` arrives when the server restarted since the client's last event: its old ID is below the new process's boot-time start ID.

## Done when

- [ ] A writer runs continuously while a client reconnects 100 times with its last ID; the IDs it collects are contiguous: no gaps, no duplicates
- [ ] An `afterID` older than the buffer gives `ErrCannotResume` from the broker and an `event: reset` on the wire, followed by live events
- [ ] An `afterID` newer than the newest published ID (a new broker without `WithStartID` standing in for a restarted server) gives `ErrCannotResume` and `reset`
- [ ] A broker made `WithStartID(1000)` that has published nothing yet gives `ErrCannotResume` for `afterID` 41
- [ ] The ring buffer is correct after wrapping around: with `history` 8 and 20 events published, `SubscribeFrom(prefix, 12)` replays exactly 13 to 20 and `SubscribeFrom(prefix, 11)` is `ErrCannotResume`
- [ ] Caller-chosen IDs with gaps (10, 20, 30) replay correctly: `SubscribeFrom(prefix, 15)` gives 20 and 30
- [ ] `go test -race ./watch ./api` is clean

## Reference

```go
package watch

var ErrCannotResume = errors.New("watch: cannot resume from that id")

func NewBroker(buffer, history int, opts ...BrokerOption) *Broker

// SubscribeFrom replays every retained event with ID > afterID, then
// continues live, with no gaps and no duplicates. ErrCannotResume if
// afterID is not 0 and is below the ring's floor (overwritten, or
// before WithStartID) or above the newest published ID.
func (b *Broker) SubscribeFrom(prefix string, afterID uint64) (
	<-chan Event, func(), error)

// GET /v1/watch honours Last-Event-ID. ErrCannotResume ->
// `event: reset` first, then live.
```

- Ring buffer with a head index, filled by `Publish` under the ID lock
- `SubscribeFrom`: subscribe, replay, skip duplicates, all starting under one lock
- `Last-Event-ID` in the handler; `reset` when the ID is older than the buffer or newer than the newest published ID
- The ring's floor starts at the `WithStartID` value and then is the ID of the last event overwritten; IDs may skip, so the ring is walked, not indexed by ID

## The tests check

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/8.4/files/`, matching the earlier chapters.

Pass when: kill the client, write 5 keys, reconnect with Last-Event-ID: the 5 missed events arrive first, then live ones, with no gaps or duplicates; an ID older than the buffer, or newer than anything the server has published, yields a reset event.

The suite will cover:

- No gaps/duplicates across 100 reconnects under a concurrent writer (IDs are contiguous)
- ErrCannotResume / reset event for an ID below the floor (overwritten, or below WithStartID on a fresh broker)
- ErrCannotResume / reset event for an ID above the newest published ID (a new broker standing in for a restarted server)
- Ring buffer wraparound correctness, including the floor
- Replay with caller-chosen IDs that skip

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
