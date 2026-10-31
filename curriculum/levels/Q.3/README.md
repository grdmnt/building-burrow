# Q.3 · Competing Consumers

> Three clerks, one in-tray. Nobody picks up the same letter twice, and nobody sits polling an empty tray.

## Your goal

Several workers share one queue. Two things are missing for that to work well. First, a worker that finds the queue empty has to *wait* for the next message instead of asking again every 100 ms; the broker should wake it the moment something arrives. Second, a worker should be able to take several messages in one round trip, and the client should be able to fetch a few ahead of time so the worker never idles between messages. That read-ahead is called *prefetch*, and the amount of it is a form of *flow control*.

Both arrive as one new method, `ReceiveBatch`. Q.1's `Receive(ctx, queue)` keeps its signature and stays receive-and-delete. The batch receive is peek-lock, so a batch is a batch of locked messages, each with its own token. Nothing is processed twice: every message is completed by exactly one worker.

## What you're using from before

**Peek-lock (Q.2 · Peek-Lock).** Locks, settlement and expiry. Here `ReceiveBatch` locks each message it hands out exactly as `ReceivePeekLock` did, and the prefetch buffer is nothing but locked messages waiting in the client. A prefetched message the worker is slow to reach can have its lock expire in the buffer, and then Q.2's sweep puts it back in the queue and someone else gets it. Put the token on the message so a batch can carry it:

```go
type Message struct {
    // ...
    LockToken     LockToken // set on peek-lock receives
    LockedUntil   time.Time
}
```

**Deadline-aware client (3.4 · Deadlines).** Every client call sets the connection deadline from `ctx` and uses `context.AfterFunc` to break out of a blocked read when the context is cancelled. A blocking `ReceiveBatch` is the longest wait the client has ever made, so this is what keeps it honest: a cancelled context returns promptly, and a timed-out call closes the connection and redials next time. Send the wait budget in the frame too, so the broker can stop waiting on its own before the client gives up:

```go
// request JSON: {"queue": "orders", "max": 10, "wait_ms": 30000}
wctx, cancel := context.WithTimeout(ctx, wait)   // on the server
defer cancel()
msgs, err := b.ReceiveBatch(wctx, q, max)
```

## What's new here

**`sync.Cond`: waiting for a condition.** Channels carry values; a condition variable lets goroutines sleep until some shared state changes. A `sync.Cond` is tied to a lock. `Wait` atomically releases the lock and sleeps; when woken it reacquires the lock before returning. `Broadcast` wakes every waiter, `Signal` wakes one. Because another goroutine may have taken the message between the wake-up and you reacquiring the lock, you always wait in a loop that re-checks the condition:

```go
q.mu.Lock()
for len(q.available) == 0 {
    q.cond.Wait()           // unlocks, sleeps, relocks on wake
}
msgs := q.take(max)
q.mu.Unlock()
```

Every place a message becomes available calls `Broadcast` while holding the lock: `Send`, `Abandon`, and the expiry callback.

**`sync.Cond` doesn't know about contexts.** `Wait` has no timeout and no cancel. The fix is to wake the waiters yourself when the context ends, and have the loop check `ctx.Err()`:

```go
stop := context.AfterFunc(ctx, func() {
    q.mu.Lock()
    q.cond.Broadcast()
    q.mu.Unlock()
})
defer stop()

q.mu.Lock()
for len(q.available) == 0 && ctx.Err() == nil {
    q.cond.Wait()
}
if ctx.Err() != nil { q.mu.Unlock(); return nil, ctx.Err() }
```

Taking the lock inside the callback is what makes it safe: a waiter is either inside `Wait` (and gets woken) or holding the lock (and will see `ctx.Err()` on its next check). Without the lock, the broadcast can land in the gap between the check and the `Wait`, and the waiter sleeps forever.

When would you use a channel instead? When each value has exactly one receiver. Here any of several waiters may take any of several messages, and the condition is "the queue is non-empty", which is what `Cond` is for.

**Batch receive.** `ReceiveBatch(ctx, queue, max)` takes up to `max` available messages in one go, locking each. It blocks only while there are none at all; one message is enough to return. A worker that wants exactly one asks for 1. It is a new method rather than a new signature for `Receive`: Q.1's `Receive` already has callers (`burrowq receive`, the dead-letter reads in Q.2) that expect one message, deleted, no waiting, and changing what an existing method means breaks them silently.

**Prefetch as flow control.** The client's `Receiver` keeps a buffer. With `Prefetch` 20, a worker asking for one message triggers a `ReceiveBatch` with `max` 20, hands back the first and keeps nineteen locked in memory, so the next nineteen calls cost no round trip. The trade is lock time: the twentieth message's lock started ticking when it was fetched, not when the worker got to it. A slow worker with a large prefetch sees locks expire in its own buffer, the broker redelivers those to someone else, and the slow worker's eventual `Complete` fails with `ErrLockLost`. Prefetch is the dial between latency and wasted work.

**Fairness.** `Broadcast` wakes everyone and they race for the lock; whoever wins takes the batch. That is not fair in any strict sense, but with three similar workers and a steady stream it spreads out. The test pins a floor: each of three workers handles at least 25% of 10,000 messages. If one worker is starving, check that `max` is small enough that one wake-up can't drain the queue.

## What to do

1. Give each queue a `sync.Cond` built on its mutex (`sync.NewCond(&q.mu)`).
2. Add `ReceiveBatch` in the batch, blocking, peek-lock form in the Reference: wait on the condition until at least one message is available or `ctx` is done; then lock and return up to `max`. Leave `Receive` and `ReceivePeekLock` as they are.
3. `Broadcast` in `Send`, `Abandon` and the lock-expiry callback, under the queue mutex.
4. Add a `QReceiveBatch` request frame with `queue`, `max` and `wait_ms`, and a reply frame carrying the batch. Q.1's `QReceive` frame does not change. On the server, derive a context with that timeout before calling `ReceiveBatch`, and reply with an empty batch when the wait runs out.
5. In the client, add a `Receiver` type for one queue with a `Prefetch` option. `Receiver.ReceiveBatch(ctx, n)` serves from its buffer first and calls `ReceiveBatch` for `max(n, Prefetch)` when the buffer is empty.
6. Make `Complete` and the other settlements work on tokens from a batch, which they already do if the token is the key.
7. Write the fairness test: 10,000 messages, three goroutines each looping `ReceiveBatch(1)` then `Complete`, a shared `map[uint64]int` counting completions per sequence number, and a count per worker.
8. Write the prefetch test: `Prefetch` 100, a lock duration of 1 s on a `FakeClock`, a worker that advances the clock 2 s before completing each message, and a second worker that picks up the redeliveries.

## Example

```go
r := client.NewReceiver("orders", queue.Prefetch(10))
for {
    msgs, err := r.ReceiveBatch(ctx, 1) // blocks until one is there
    if err != nil { return err }
    m := msgs[0]
    process(m)
    if err := r.Complete(ctx, m.LockToken); err != nil {
        // ErrLockLost: someone else has it now; move on
    }
}
```

Three of those against one queue:

```text
sent 10000 messages
worker 1: 3391 completed
worker 2: 3270 completed
worker 3: 3339 completed
completed once: 10000, twice: 0
```

And with a slow worker and `Prefetch` 100, a lock duration of 1 s:

```text
worker slow: 100 fetched, 1 completed, 99 ErrLockLost
worker fast: 99 redelivered, DeliveryCount 2 on each
```

## Done when

- [ ] Three workers drain 10,000 messages and every message is completed exactly once
- [ ] Each worker handles at least 25% of them
- [ ] A worker calling `ReceiveBatch` on an empty queue wakes as soon as a message is sent, without polling
- [ ] Cancelling the context of a blocked `ReceiveBatch` returns promptly with `ctx.Err()`
- [ ] Q.1's `Receive(ctx, queue)` is unchanged: one message, receive-and-delete, an error right away on an empty queue
- [ ] With a large prefetch and a slow worker, the buffered locks expire and those messages are redelivered to another worker
- [ ] `gofmt -l .` and `go vet ./...` print nothing

## Reference

```go
// peek-lock, up to max messages; blocks until at least one is
// available or ctx is done
func (b *Broker) ReceiveBatch(ctx, queue string, max int) ([]Message,
    error)
// Receive(ctx, queue) from Q.1 and ReceivePeekLock from Q.2: unchanged
// client: Receiver with a Prefetch option and ReceiveBatch(ctx, n)
```

- sync.Cond per queue
- `ReceiveBatch` beside an unchanged `Receive`
- Prefetch
- Fairness test

## The tests check

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/Q.3/files/`, matching the earlier chapters.

The suite will cover:

- Exactly-once completion across consumers
- Fairness
- Prefetch lock expiry
- ReceiveBatch wakes on Send without polling and returns promptly on cancel; Receive keeps its Q.1 behaviour

## Try it

```bash
./burrow test
go test ./queue -run Fairness -race -v
go run ./cmd/burrowq receive -max 10 orders    # waits for the first one
```

## In the wild

AMQP 1.0 link credit; Kafka consumer groups

## New in Go

sync.Cond · Broadcast vs Signal · waking a Cond on context cancel · flow control

Standard library: `sync`, `context`, `time`
