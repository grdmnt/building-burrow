# S4.1 · Fan-out Topics

One send, many independent copies: topics with subscriptions.

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/S4.1/files/`, matching the earlier chapters.

## Build

- `CreateTopic(name)` and `CreateSubscription(topic, name)`
- A send to a topic copies the message into every subscription
- Each subscription is a full queue: peek-lock, delivery count, DLQ
- Paths like `events/Subscriptions/billing` and `events/Subscriptions/billing/$deadletterqueue`

## Pass when

Three subscriptions each get their own copy. Completing in `billing` doesn't touch `audit`. A subscription nobody reads keeps filling, and its DLQ works independently.

## New in Go

composition · reusing types · copy semantics

Standard library: `sync` · `strings`
