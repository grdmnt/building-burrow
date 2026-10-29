# Q.1 · Mailbox

A durable queue broker speaking a second frame vocabulary on the record codec, carrying the same message shape Azure Service Bus uses.

## Build

```go
type Message struct { MessageID, ContentType, CorrelationID, Subject string; Body []byte; ApplicationProperties map[string]any; SequenceNumber uint64; EnqueuedTime time.Time }
func (b *Broker) Send(ctx, queue string, m Message) (uint64, error)
func (b *Broker) Receive(ctx, queue string) (Message, error) // receive-and-delete
```

- Message encoding (JSON body inside a Record)
- Queue frames on wire.ServeConns
- WAL before ack; sequence numbers rebuilt on replay
- CLI

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/Q.1/files/`, matching the earlier chapters.

## Pass when

Send 1,000 messages, kill -9 the broker, restart: you receive all 1,000 exactly once, in order, with sequence numbers that only go up.

The suite will cover:

- Order and exactly-once after kill -9
- Sequence numbers monotonic across restart
- CLI round trip

## Uses

- Record codec (built in 2.1 · Record)
- WAL (built in 4.1 · Write-Ahead Log)
- Accept loop (built in 3.1 · Loopback)
- Shutdown (built in 3.5 · Power Down)
- Frame client (built in 3.3 · Remote Store)

## In the wild

Azure Service Bus queues, SQS, RabbitMQ classic queues

## New in Go

struct design · monotonic counters · reusing the WAL

Standard library: `encoding/json`, `sync`, `time`
