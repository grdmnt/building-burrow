# 8.2 · Fan-out

An in-process broker on channels that tells subscribers when keys change, without letting a slow reader stall writes. Expiry is an event too: the `OnEvict` hook from 1.5 feeds the broker.

## Build

```go
package watch

type Event struct { ID uint64; Type string /* put|delete|expire */; Entry store.Entry }
type Broker struct { /* yours */ }
func NewBroker(buffer int) *Broker
func (b *Broker) Publish(ev Event)
func (b *Broker) Subscribe(prefix string) (<-chan Event, func()) // channel closed and unsubscribed if the subscriber falls `buffer` behind

// Wrap decorates any store.Store so every successful mutation publishes; pass its OnEvict
// method as store.WithOnEvict so expiries publish too.
func Wrap(st store.Store, b *Broker) *Store
func (s *Store) OnEvict(e store.Entry)
```

- `Broker` with buffered subscriber channels and select/default
- `Wrap` decorating a Store
- Expire events via OnEvict

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/8.2/files/`, matching the earlier chapters.

## Pass when

With one subscriber that never reads, 1,000 writes take no longer than with none and that subscriber is dropped; fast subscribers see every event in order with increasing IDs, including expiries.

The suite will cover:

- Ordering and completeness for 3 fast subscribers
- A stuck subscriber is dropped and writers are not delayed (timed)
- Expiry on a FakeClock produces an expire event with the entry
- Unsubscribe twice is safe; publishing after all unsubscribed is fine
- storetest.Run passes through Wrap

## Uses

- Janitor (built in 1.5 · Janitor)
- Memory store (built in 1.1 · Register File)

## In the wild

NATS core pub/sub semantics; Go's own event bus idiom; Redis keyspace notifications

## New in Go

buffered channels · select with default · unsubscribe functions · decorator over an interface · sync.Once

Standard library: `sync`, `context`, `strings`
