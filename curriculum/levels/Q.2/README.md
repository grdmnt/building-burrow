# Q.2 · Peek-Lock

> Take the letter out and read it, but only tear up the envelope once you're done. Drop it on the way and it goes back in the box.

## Your goal

Q.1's `Receive` deletes a message the moment it hands it out. A consumer that crashes right after loses it. Here you add *peek-lock*: a receive that locks the message for a short time instead of deleting it. The consumer works, then *settles*: `Complete` deletes it, `Abandon` puts it straight back, `DeadLetter` moves it to a holding queue for messages that can't be processed, and `RenewLock` buys more time. If the consumer never settles, the lock runs out and the message becomes available again, with its delivery count bumped. After ten failed deliveries it goes to the dead-letter queue on its own.

This turns at-most-once into *at-least-once* delivery: a message can be delivered twice, but it is never lost. A lock that expires by itself is a *lease*, and you built leases in chapter 1. The lock table here is literally that store.

## What you're using from before

**Queue broker (Q.1 · Mailbox).** The durable FIFO with `Send`, `Receive`, one WAL per queue and the second frame vocabulary. Every new call here is one more op in that vocabulary and one more method on `Broker`, and `Complete` and `DeadLetter` append to the same log that `Receive` did:

```go
if err := q.log.Append(takenRecord(seq)); err != nil { return err }
```

**Expiry (1.4 · Decay).** A `store.Memory` with `SetWithTTL`, `Touch`, `ExpiresAt`, and a clock injected with `WithClock` so tests can move time. Here a lock *is* an entry with a TTL: the key is the lock token, the value names the message, and the TTL is the lock duration:

```go
locks := store.NewMemory(store.WithClock(clock),
    store.WithSweepInterval(50*time.Millisecond),
    store.WithOnEvict(b.lockExpired))
locks.SetWithTTL(ctx, string(token), []byte(seqText), lockDur) // lock
locks.Touch(ctx, string(token), lockDur)                      // renew
_, err := locks.Get(ctx, string(token))   // ErrNotFound: lock lost
```

**Janitor (1.5 · Janitor).** The sweeper that removes expired entries and calls `OnEvict` exactly once per key, outside the lock, plus the `Close` discipline. Here `OnEvict` is the expiry sweep: when a lock entry expires, the callback puts the message back in its queue and bumps `DeliveryCount`. You wrote the hard part in chapter 1; this level just points it at a new table.

## What's new here

**A state machine, written down.** Every message is in exactly one of a few states, and only some moves between them are legal. Draw it before you code it:

```text
                  Receive                 Complete
   available ───────────────► locked ───────────────► gone
       ▲                        │  │
       │     Abandon / expiry   │  │  DeadLetter, or expiry when
       └────────────────────────┘  │  DeliveryCount reaches 10
                                   ▼
                              dead-lettered: <queue>/$DeadLetterQueue
```

In Go a state is a small named type over `int` with `iota` constants, and a transition is a method that checks the current state, errors if the move is illegal, and changes it under the mutex:

```go
type state int

const (
    available state = iota
    locked
    gone
)

func (q *queue) complete(seq uint64) error {
    q.mu.Lock(); defer q.mu.Unlock()
    m, ok := q.inflight[seq]
    if !ok || m.state != locked { return ErrLockLost }
    m.state = gone
    ...
}
```

`switch` on a state with no `default` is the usual way to make the compiler nudge you when a state is unhandled (`go vet` won't, but `staticcheck` will).

**Reusing a lease.** The lock table is a `store.Memory` with its own clock. `ReceivePeekLock` takes the oldest available message, moves it to `locked`, and does `SetWithTTL(token, seq, lockDur)`. Each settlement call starts the same way: `Get(token)`; `ErrNotFound` means the lock already expired and the answer is `ErrLockLost`. `RenewLock` is `Touch`. When the janitor evicts a lock, `OnEvict` fires with the entry, which tells you which message to put back. Because expiry is decided by the injected clock, a test advances a `FakeClock` past the lock duration and waits for `OnEvict` with `testkit.Eventually`, no sleeping.

**Random tokens.** A lock token must be unguessable, so one consumer can't settle another's message by accident. `crypto/rand` gives cryptographic randomness; `math/rand` does not. Go 1.24's `rand.Text()` returns a random string ready to use as a token:

```go
import "crypto/rand"

token := LockToken(rand.Text())   // "6MS7HZP3VQQKDBX5F2D6X7KTB4"
```

**The dead-letter queue is just a queue.** Service Bus names it `<queue>/$DeadLetterQueue`. Dead-lettering appends the message to that queue's log with two extra `ApplicationProperties`, `DeadLetterReason` and `DeadLetterErrorDescription`, and marks it gone in the original. A consumer reads the DLQ with the same `Receive`.

## What to do

1. Add `DeliveryCount int` to `Message`, a `LockToken` type (a string), `var ErrLockLost`, and `MaxDeliveryCount = 10` and a default lock duration (Service Bus uses 60 s) as broker options.
2. Give each queue an in-memory table of in-flight messages keyed by sequence number, with a state per message, and give the broker one `store.Memory` for locks built with `WithClock`, `WithSweepInterval` and `WithOnEvict`.
3. `ReceivePeekLock`: oldest available message, state to `locked`, `DeliveryCount++`, a fresh token, `SetWithTTL` on the lock table. Return the message, the token and the lock's expiry time.
4. `Complete`: look up the token; `ErrLockLost` if missing. Append a taken record, mark the message gone, delete the lock entry.
5. `Abandon`: look up the token; state back to `available` at the front of the queue; delete the lock entry. No log record, the message was never gone.
6. `RenewLock`: `Touch` the lock entry; `ErrLockLost` if it is already gone.
7. `DeadLetter(token, reason, description)`: append to the DLQ's log with the reason and description in `ApplicationProperties`, append a taken record to the original, delete the lock.
8. In the `OnEvict` callback: find the message by the entry's value, and either make it available again or, if `DeliveryCount` has reached `MaxDeliveryCount`, dead-letter it with reason `MaxDeliveryCountExceeded`. Remember `OnEvict` runs outside the store's lock but you still need your own.
9. Make `Broker.Close` close the lock table so the sweeper stops, and add the new ops to the server, the client and `burrowq` (`receive -peek-lock`, `complete`, `abandon`, `dead-letter`).
10. Decide, and write down, whether `DeliveryCount` survives a restart. Keeping it in memory is acceptable here.

## Example

```go
clock := testkit.NewFakeClock()
b := queue.New(dir, queue.WithClock(clock.Now),
    queue.WithLockDuration(30*time.Second))
b.Send(ctx, "orders", queue.Message{Body: []byte("x")})

m, tok, until, _ := b.ReceivePeekLock(ctx, "orders")
// m.DeliveryCount == 1, until == clock.Now() + 30s

clock.Advance(31 * time.Second)              // walk away; lock expires
err := b.Complete(ctx, tok)                  // ErrLockLost

m, tok, _, _ = b.ReceivePeekLock(ctx, "orders")
// the same message, m.DeliveryCount == 2
b.Complete(ctx, tok)                         // nil; now it is gone
```

And a consumer that gives up every time:

```go
for i := 0; i < 10; i++ {
    _, tok, _, _ := b.ReceivePeekLock(ctx, "orders")
    b.Abandon(ctx, tok)
}
// after the 10th delivery the message is in orders/$DeadLetterQueue
m, _ := b.Receive(ctx, "orders/$DeadLetterQueue")
// m.ApplicationProperties["DeadLetterReason"]
//   == "MaxDeliveryCountExceeded"
```

## Done when

- [ ] A received message whose lock expires comes back from the next receive with `DeliveryCount` 2
- [ ] `Complete` with an expired token returns `ErrLockLost` and the message is not lost
- [ ] `RenewLock` pushes the expiry out; a renewed lock does not expire at the original time
- [ ] A message abandoned (or expired) ten times lands in the dead-letter queue with a reason
- [ ] All of it runs on a `FakeClock`, with no real sleeps in the tests
- [ ] `gofmt -l .` and `go vet ./...` print nothing

## Reference

```go
func (b *Broker) ReceivePeekLock(ctx, queue string) (Message, LockToken,
    time.Time, error)
// ErrLockLost after expiry
func (b *Broker) Complete(ctx, token LockToken) error
func (b *Broker) Abandon(ctx, token LockToken) error
func (b *Broker) RenewLock(ctx, token LockToken) error
func (b *Broker) DeadLetter(ctx, token LockToken,
    reason, description string) error
```

- Lock state as an expiring entry
- Settlement outcomes
- Expiry sweep redelivers and bumps DeliveryCount
- DLQ after MaxDeliveryCount

## The tests check

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/Q.2/files/`, matching the earlier chapters.

The suite will cover:

- Lock expiry and redelivery
- ErrLockLost
- DLQ with reason
- RenewLock extends

## Try it

```bash
./burrow test
go run ./cmd/burrowq send orders hello
go run ./cmd/burrowq receive -peek-lock orders   # prints body and token
go run ./cmd/burrowq complete <token>
```

## In the wild

Azure Service Bus peek-lock and dead-letter queues; SQS visibility timeout

## New in Go

state machines · iota enums · lease reuse · crypto/rand tokens

Standard library: `crypto/rand`, `time`, `sync`
