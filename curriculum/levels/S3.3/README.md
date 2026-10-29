# S3.3 · Dead Letters

Poison and expired messages go somewhere you can inspect, instead of looping forever.

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/S3.3/files/`, matching the earlier chapters.

## Build

- Each queue gets a sub-queue at `orders/$deadletterqueue`
- Past `MaxDeliveryCount` (default 10) a message moves there with reason `MaxDeliveryCountExceeded`
- Consumers can call `DeadLetter(token, reason, description)` themselves
- Per-message `TimeToLive`: expired messages are dropped, or dead-lettered with `TTLExpiredException` when the queue opts in
- `burrowctl bus resubmit orders` moves dead letters back

## Pass when

A consumer that always abandons puts its message in the DLQ after 10 deliveries, with the right reason. An expired message lands there only on queues with dead-lettering on expiry turned on.

## New in Go

sub-queues · settlement outcomes · error metadata

Standard library: `time` · `errors` · `strings`
