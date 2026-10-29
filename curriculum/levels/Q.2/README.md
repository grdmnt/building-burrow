# Q.2 · Peek-Lock

Receive without losing messages: a lock on a message is a lease, and you built leases in chapter 1.

## Build

```go
func (b *Broker) ReceivePeekLock(ctx, queue string) (Message, LockToken, time.Time, error)
func (b *Broker) Complete(ctx, token LockToken) error // ErrLockLost after expiry
func (b *Broker) Abandon / RenewLock / DeadLetter(ctx, token, reason, description string) error
```

- Lock state as an expiring entry
- Settlement outcomes
- Expiry sweep redelivers and bumps DeliveryCount
- DLQ after MaxDeliveryCount

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/Q.2/files/`, matching the earlier chapters.

## Pass when

Receive and walk away: once the lock expires the message comes back with DeliveryCount 2; completing with an expired token fails with ErrLockLost; a consumer that always abandons sends its message to the DLQ after 10 deliveries. All on a FakeClock.

The suite will cover:

- Lock expiry and redelivery
- ErrLockLost
- DLQ with reason
- RenewLock extends

## Uses

- Queue broker (built in Q.1 · Mailbox)
- Expiry (built in 1.4 · Decay)
- Janitor (built in 1.5 · Janitor)

## In the wild

Azure Service Bus peek-lock and dead-letter queues; SQS visibility timeout

## New in Go

state machines · lease reuse

Standard library: `crypto/rand`, `time`, `sync`
