# Q.3 · Competing Consumers

Many workers drain one queue quickly without processing anything twice.

## Build

```go
func (b *Broker) Receive(ctx, queue string, max int) ([]Message, error) // blocks until at least one or ctx done
// client: Receiver with Prefetch option
```

- sync.Cond per queue
- Batch receive
- Prefetch
- Fairness test

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/Q.3/files/`, matching the earlier chapters.

## Pass when

Three consumers drain 10,000 messages: every message is completed once and each consumer handles at least 25%; with a large prefetch and slow consumers, buffered locks expire and those messages are redelivered.

The suite will cover:

- Exactly-once completion across consumers
- Fairness
- Prefetch lock expiry

## Uses

- Peek-lock (built in Q.2 · Peek-Lock)
- Deadline-aware client (built in 3.4 · Deadlines)

## In the wild

AMQP 1.0 link credit; Kafka consumer groups

## New in Go

sync.Cond · flow control

Standard library: `sync`, `context`, `time`
