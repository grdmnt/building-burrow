# S3.2 · Peek-Lock

Receive without losing messages: lock a message while you work on it, then settle it.

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/S3.2/files/`, matching the earlier chapters.

## Build

- Peek-lock `Receive` returns the message plus a random `LockToken` and `LockedUntil`
- `Complete`, `Abandon` and `RenewLock` by lock token
- Locks expire after the queue's `LockDuration` (Service Bus defaults to 1 minute, max 5) and the message becomes available again
- `DeliveryCount` goes up when a message is abandoned or its lock expires

## Pass when

Receive and walk away: once the lock expires the message comes back with `DeliveryCount` 2. Completing with an expired token fails with `ErrLockLost`. All proven with an injected clock.

## New in Go

at-least-once delivery · expiry scheduling · random tokens · state machines

Standard library: `crypto/rand` · `container/heap` · `time` · `sync`
