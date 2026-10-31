# 8.2 · Fan-out

> One writer, many listeners, and none of the listeners may slow the writer down.

## Your goal

Write a `watch` package with a `Broker`: an in-process publish/subscribe hub. A writer calls `Publish` once; every subscriber whose prefix matches gets the event on its own channel. Then write `Wrap`, which takes any `store.Store` and returns one that publishes an event after every successful write, so callers of the store don't have to remember to. Expiry becomes an event too, through the `OnEvict` hook.

The one rule that makes this hard: a subscriber that stops reading must not stall the writer. The broker drops it instead. This is the engine behind the HTTP watch endpoint in the next level.

Every event carries an `ID`, and IDs only go up. Usually the broker picks it (one more than the last). A caller may also bring its own, as long as it is above the last one; chapter 10 uses that to number events by Raft log index.

## What you're using from before

**Memory store (1.1 · Register File).** `store.Store` is the interface every engine satisfies. `Wrap` takes one and returns a `*watch.Store` that is also a `store.Store`, with the same methods, plus publishing. The caller can't tell the difference:

```go
b := watch.NewBroker(64)
var st store.Store = watch.Wrap(store.NewMemory(), b)
st.Set(ctx, "user:1", []byte("ann")) // publishes {Type: "put", ...}
```

**Janitor (1.5 · Janitor).** `store.WithOnEvict(fn)` is the option that makes `Memory` call `fn(entry)` exactly once for each key it removes because it expired. Pass the wrapper's `OnEvict` method and expiries flow into the broker as `expire` events. The wrapper needs the store and the store needs the wrapper's method, so wire it with a closure:

```go
var ws *watch.Store
mem := store.NewMemory(
	store.WithClock(clock),
	store.WithOnEvict(func(e store.Entry) { ws.OnEvict(e) }),
)
ws = watch.Wrap(mem, b)
```

## What's new here

**Buffered channels.** `make(chan Event)` is a hand-off: a send blocks until someone receives. `make(chan Event, 64)` has room for 64 events, so a send returns at once while there is space. Each subscriber gets its own buffered channel; the buffer absorbs short bursts where the subscriber is a bit behind.

**`select` with `default`.** A `select` with a `default` case never blocks. If the send can happen right now it happens, otherwise `default` runs. That is the whole trick for "never stall the writer":

```go
select {
case sub.ch <- ev:
	// delivered
default:
	// buffer full: this subscriber is too slow, drop it
}
```

A dropped subscriber has its channel closed and is removed. Closing tells the reader "you fell behind" the moment it next reads, which is the only honest thing to do. Only the broker closes subscriber channels, because closing a channel twice panics and so does sending on a closed one.

**Returning an unsubscribe function.** `Subscribe` returns the channel, `last` (the ID of the last event published before the subscription began) and a `func()`. The caller `defer`s it. There is no handle type to learn and no ID to pass back. Make it safe to call twice with `sync.Once` from 1.5, so a subscriber that was dropped by the broker and then unsubscribes itself does no harm. Read `last` under the same lock that adds the subscriber; then every event on the channel has an ID above it, which 8.3 uses to tell a client where its stream starts:

```go
ch, last, stop := b.Subscribe("user:")
defer stop()
fmt.Println("from after", last)
for ev := range ch { // ends when the broker closes ch
	fmt.Println(ev.ID, ev.Type, ev.Entry.Key)
}
```

**A decorator over an interface.** Embed the `store.Store` interface in a struct and the struct gets every method of the interface for free, forwarded to the embedded value. Then write only the methods you want to change. `Wrap` overrides the six writes, publishes after each one succeeds, and leaves `Get`, `Keys` and `Touch` alone:

```go
type Store struct {
	store.Store // every method forwards here unless overridden
	b *Broker
}

func (s *Store) Set(ctx context.Context, key string, v []byte) (
	store.Entry, error) {
	e, err := s.Store.Set(ctx, key, v)
	if err == nil {
		s.b.Publish(Event{Type: "put", Entry: e})
	}
	return e, err
}
```

This is the same embedding move as the `ResponseWriter` wrapper in 7.3, applied to your own interface. `Publish` can only fail for an ID the caller chose, and the wrapper never chooses one, so it ignores the error.

**IDs that only go up.** The broker keeps `last`, the ID of the last event it published. `Publish` with `ev.ID == 0` sets `ev.ID = last+1`. `Publish` with `ev.ID != 0` keeps the caller's ID, but only if it is greater than `last`; otherwise it returns `ErrStaleID` and delivers nothing. Either way `last` becomes the new ID. Subscribers can then rely on one thing: IDs in their channel strictly increase.

`WithStartID(id)` is an option to `NewBroker` that sets `last` to `id` before anything is published, so the first assigned ID is `id+1`. The next level uses it to make a restarted server's IDs larger than any from before the restart:

```go
b := watch.NewBroker(16, watch.WithStartID(1000))
b.Publish(watch.Event{Type: "put"})           // ID 1001
b.Publish(watch.Event{ID: 2000, Type: "put"}) // keeps 2000
err := b.Publish(watch.Event{ID: 1500})       // ErrStaleID, dropped
b.Publish(watch.Event{Type: "put"})           // ID 2001
```

## What to do

1. Create `watch/broker.go`, `package watch`, with `Event`, `Broker`, `NewBroker`, `BrokerOption`, `WithStartID` and `ErrStaleID`.
2. Keep a mutex, `last` (the ID of the last published event, 0 or the `WithStartID` value to begin with), and the set of subscribers. Each subscriber holds its prefix, its channel and a `sync.Once` for unsubscribing.
3. Write `Subscribe(prefix)`: make a channel with `buffer` room, add the subscriber and read `last` under the lock, return the channel, `last` and a stop function that removes it and closes the channel, once.
4. Write `Publish(ev)`: under the lock, settle the ID. If `ev.ID` is 0, set it to `last+1`. If it is set and not above `last`, return `ErrStaleID` and deliver nothing. Set `last = ev.ID`. Then for each subscriber whose prefix matches `ev.Entry.Key` (`strings.HasPrefix`), try a non-blocking send. On a full buffer, drop that subscriber: remove it and close its channel.
   - Settling the ID and sending under the same lock is what keeps IDs increasing in every channel.
5. Create `watch/store.go` with `Store` embedding `store.Store`, and `Wrap`.
6. Override `Set`, `SetWithTTL`, `CompareAndSwap`, `CompareAndSwapWithTTL` to publish `put` with the returned entry, and `Delete`, `CompareAndDelete` to publish `delete` with `Entry{Key: key}`. Publish only when the call succeeded.
7. Write `(s *Store) OnEvict(e store.Entry)`: publish `expire` with `e`. It is called from the janitor's goroutine or a reader, so it must not need any lock a store call holds (the broker's own lock is fine).
8. Run `go test -race ./watch`. Then run `storetest.Run` through `Wrap` to prove the wrapper changed nothing about the store.

## Example

```go
b := watch.NewBroker(16)
ch, _, stop := b.Subscribe("user:")
defer stop()

st := watch.Wrap(store.NewMemory(), b)
st.Set(ctx, "user:1", []byte("ann"))
st.Set(ctx, "cfg:x", []byte("1"))   // not user:, never delivered
st.Delete(ctx, "user:1")

ev := <-ch
fmt.Println(ev.ID, ev.Type, ev.Entry.Key, string(ev.Entry.Value))
// 1 put user:1 ann
ev = <-ch
fmt.Println(ev.ID, ev.Type, ev.Entry.Key)
// 3 delete user:1
```

The IDs skip 2 because the `cfg:x` write took it. IDs are global to the broker, not per subscriber.

A subscriber that never reads is dropped after `buffer` events, and 1,000 writes take no longer than with no subscribers at all:

```go
dead, _, _ := b.Subscribe("")    // never read
for i := range 1000 {
	st.Set(ctx, fmt.Sprint("k", i), nil)  // never blocks
}
_, ok := <-dead
fmt.Println(ok)                  // false: the broker closed it
```

## Done when

- [ ] Three fast subscribers each see every matching event, in order, with increasing IDs
- [ ] `Publish` with ID 0 assigns one more than the last ID; a caller's ID above the last is kept; a caller's ID at or below the last returns `ErrStaleID` and reaches nobody
- [ ] With `WithStartID(1000)` the first assigned ID is 1001
- [ ] `Subscribe` returns the last published ID, and every event on its channel has a larger one, even with writers publishing concurrently
- [ ] A subscriber that never reads is dropped (its channel is closed) and 1,000 writes are not measurably slower than with no subscriber
- [ ] A key with a TTL on a fake clock produces an `expire` event carrying the entry after the clock passes its expiry
- [ ] Calling the stop function twice is fine; publishing after every subscriber has left is fine
- [ ] `storetest.Run` passes on a store wrapped with `Wrap`
- [ ] `go test -race ./watch` is clean

## Reference

```go
package watch

type Event struct {
	ID    uint64
	Type  string // put | delete | expire
	Entry store.Entry
}

var ErrStaleID = errors.New("watch: event id not above the last")

type Broker struct { /* yours */ }
type BrokerOption func(*Broker)

func NewBroker(buffer int, opts ...BrokerOption) *Broker

// WithStartID makes the first assigned ID id+1.
func WithStartID(id uint64) BrokerOption

// Publish assigns last+1 when ev.ID is 0. A non-zero ev.ID is kept if
// it is above the last published ID; otherwise ErrStaleID and the
// event is dropped.
func (b *Broker) Publish(ev Event) error

// Subscribe returns a channel of events whose key starts with prefix,
// the last ID published before it subscribed (every event on the
// channel has a larger ID), and a stop function. The channel is closed
// and the subscriber removed if it falls `buffer` events behind.
func (b *Broker) Subscribe(prefix string) (ch <-chan Event,
	last uint64, stop func())

// Wrap decorates any store.Store so every successful mutation
// publishes. Pass its OnEvict method as store.WithOnEvict so expiries
// publish too.
func Wrap(st store.Store, b *Broker) *Store
func (s *Store) OnEvict(e store.Entry)
```

- `Broker` with buffered subscriber channels and select/default
- IDs that strictly increase: assigned by the broker, or the caller's if above the last; `WithStartID` sets where assigned IDs begin
- `Wrap` decorating a Store, overriding the six mutating methods
- Expire events via `OnEvict`, closing the loop with 1.5
- Only the broker closes subscriber channels; unsubscribe is safe to call twice

## The tests check

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/8.2/files/`, matching the earlier chapters.

Pass when: with one subscriber that never reads, 1,000 writes take no longer than with none and that subscriber is dropped; fast subscribers see every event in order with increasing IDs, including expiries.

The suite will cover:

- Ordering and completeness for 3 fast subscribers
- Subscribe's `last` equals the last published ID, and every event delivered on the channel is above it, under concurrent publishers
- ID rules: 0 is assigned last+1, a caller's larger ID is kept, a caller's ID at or below the last is ErrStaleID and delivered to nobody; WithStartID(1000) gives 1001 first
- A stuck subscriber is dropped and writers are not delayed (timed)
- Expiry on a FakeClock produces an expire event with the entry
- Unsubscribe twice is safe; publishing after all unsubscribed is fine
- storetest.Run passes through Wrap

## Try it

```bash
./burrow watch
go test -race -run . ./watch
# a scratch main: subscribe to "" and print events while a goroutine
# writes keys with a 1s TTL; expire events show up a second later
```

## In the wild

NATS core pub/sub semantics; Go's own event bus idiom; Redis keyspace notifications.

## New in Go

buffered channels · select with default · unsubscribe functions · decorator over an interface

Standard library: `sync`, `context`, `strings`, `errors`
