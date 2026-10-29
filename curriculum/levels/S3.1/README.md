# S3.1 · Mailbox

A durable queue broker speaking your frame protocol, carrying the same message shape Service Bus uses.

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/S3.1/files/`, matching the earlier chapters.

## Build

- A `Message` with `MessageID`, `Body`, `ContentType`, `CorrelationID`, `Subject` and `ApplicationProperties`, plus broker-set `SequenceNumber` and `EnqueuedTime`
- Named queues; `Send` and `Receive` in receive-and-delete mode
- Every send goes through your Chapter 3 WAL before it's acknowledged
- `burrowctl bus send orders <body>` and `burrowctl bus receive orders`

## Pass when

Send 1,000 messages, `kill -9` the broker, restart: you receive all 1,000 exactly once, in order, with sequence numbers that only go up.

## New in Go

struct design · monotonic counters · reusing your WAL

Standard library: `encoding/json` · `sync` · `time`
