# Q.1 · Mailbox

> A mailbox holds a letter until someone collects it. Yours has to hold it through a power cut.

## Your goal

Build a *queue broker*: a server that accepts messages on named queues, keeps them in order, and hands each one to whoever asks for it next. Producers call `Send`, consumers call `Receive`, and the broker survives a crash without losing anything it acknowledged. The message shape is the one Azure Service Bus uses, field for field, so what you learn here maps onto a real product.

Everything the broker needs already exists: the record codec carries its frames, the write-ahead log makes it durable, and the accept loop, shutdown and client shape are the ones `burrowd` uses. This level is mostly wiring, plus one new idea: the broker, not the sender, numbers the messages.

The `Receive` here is *receive-and-delete*: the message is gone the moment it is handed out. If the consumer crashes a millisecond later, the message is lost. That is called at-most-once delivery, and it is fine for this level.

## What you're using from before

**Record codec (2.1 · Record).** `WriteRecord` and `ReadRecord` move one length-prefixed blob over a connection. The KV frames of 2.2 are one vocabulary of ops laid inside those records; the queue gets a second vocabulary, laid inside the same records:

```go
// one byte says what the record is, the rest is JSON
payload := append([]byte{byte(QSend)}, body...)
err := wire.WriteRecord(conn, payload)
```

**WAL (4.1 · Write-Ahead Log).** `wal.Open`, `Append` (fsynced before it returns) and `Records()` (an iterator over everything in the file). Here every `Send` is appended before the broker replies, and on startup the broker replays the log to rebuild each queue:

```go
log, err := wal.Open(filepath.Join(dir, "orders.wal"))
if err := log.Append(rec); err != nil { return 0, err }   // then reply
for rec, err := range log.Records() { /* rebuild */ }
```

**Accept loop (3.1 · Loopback).** `wire.ServeConns(ln, handle, logger)` runs `handle(conn)` in a goroutine per connection and returns nil when the listener closes. The broker's server is one more caller:

```go
return wire.ServeConns(ln, s.handleConn, s.Logger)
```

**Shutdown (3.5 · Power Down).** `conntrack.Tracker` and the `Shutdown(ctx)` shape: stop accepting, let in-flight requests finish, close idle connections, force-close at the deadline. Give the broker's server the same `Shutdown(ctx)` and wire it into `burrowd serve`'s signal handling so Ctrl-C drains both listeners.

**Frame client (3.3 · Remote Store).** `client.Client` is one connection, a mutex so calls don't interleave, and server errors mapped back to Go errors. The queue client is the same shape with two methods, and `burrowctl`'s `run(args, stdout, stderr) int` layout is the one the queue CLI copies.

## What's new here

**Designing a struct to match someone else's.** `Message` mirrors Service Bus's message: `MessageID` and `CorrelationID` are strings the sender chooses (an order id, a request id), `Subject` is a short label, `ContentType` says how to read `Body` (`application/json`, `text/plain`), `ApplicationProperties` is a free-form bag, and `SequenceNumber` and `EnqueuedTime` are set by the broker, never by the sender. Keep `Body` as `[]byte`; the broker doesn't care what is in it.

The struct goes over the wire as JSON, and `encoding/json` has rules worth knowing before you rely on them:

```go
type Message struct {
    MessageID string         `json:"message_id"`
    Body      []byte         `json:"body"`     // base64 on the wire
    Props     map[string]any `json:"props"`    // numbers become float64
    Enqueued  time.Time      `json:"enqueued"`  // RFC 3339 text
}
```

`[]byte` becomes base64 automatically. `time.Time` becomes `2026-10-31T09:00:00Z`. A `map[string]any` round-trips, but every number in it comes back as `float64`, so a sender's `int` 3 reads as `3.0`. Fields that start with a lowercase letter are not encoded at all. Struct tags like `json:"message_id"` pick the wire name; without them the Go field name is used.

**A monotonic counter the broker owns.** Each queue has a `uint64` that only goes up. `Send` takes the next value under the queue's mutex, stamps it on the message, and returns it. After a restart the counter must continue from where it was, not from zero, so replay sets it to the highest sequence number seen in the log. No sender ever supplies one.

**Reusing the WAL for two kinds of record.** The log needs to remember sends *and* receives. If only sends were logged, every message ever received would come back after a restart. So a `Receive` appends a small "taken" record (the sequence number) before replying, and replay does both: add each sent message to the queue, then drop each one a taken record names. What is left is exactly what a consumer has not yet collected. One log per queue, named after it, keeps replay simple.

**The second frame vocabulary.** The record payload starts with one op byte and the rest is JSON. Pick numbers that don't collide with the KV ops (those are 0x01 to 0x09 and 0x80 to 0x86):

| Op | Direction | JSON |
|---|---|---|
| `QSend` | request | `{"queue": "orders", "message": {...}}` |
| `QReceive` | request | `{"queue": "orders"}` |
| `QSent` | reply | `{"sequence": 17}` |
| `QMessage` | reply | the message |
| `QEmpty` | reply | nothing to receive right now |
| `QError` | reply | `{"error": "..."}` |

## What to do

1. Make a `queue` package with `Message` and `Broker` as in the Reference. `New(dir string)` opens one `wal.Log` per queue under `dir`, replaying any that exist.
2. `Send`: validate the queue name (same rules as keys), lock the queue, assign the next sequence number and `EnqueuedTime`, append the message as a JSON record, then return the sequence number.
3. `Receive`: lock the queue, take the oldest message, append a taken record with its sequence number, then return it. On an empty queue, return an error right away (waiting comes in a later level).
4. Replay on `New`: for each log, add every sent message to the queue in order, remove every one named by a taken record, and set the counter to the largest sequence number seen.
5. A `queue.Server` on `wire.ServeConns` that decodes the op byte and JSON, calls the broker and writes the reply, with the same `Shutdown(ctx)` as the frame server.
6. A `queue.Client` with `Dial`, `Send`, `Receive` and `Close`.
7. `burrowd serve -queue-addr 127.0.0.1:7090 -queue-dir ./data/queues` starts the broker beside the KV listener, printing `queue on <addr>` as a startup line.
8. A `cmd/burrowq` CLI: `burrowq send <queue> <body>` prints `sent <sequence>`; `burrowq receive <queue>` prints the body. Errors to stderr, exit 1; usage mistakes exit 2.

## Example

```bash
$ go run ./cmd/burrowd serve -queue-addr 127.0.0.1:7090 -queue-dir ./q &
$ burrowq send orders '{"id": 1}'
sent 1
$ burrowq send orders '{"id": 2}'
sent 2
$ burrowq receive orders
{"id": 1}
$ kill -9 %1
$ go run ./cmd/burrowd serve -queue-addr ... -queue-dir ./q &
$ burrowq receive orders
{"id": 2}
$ burrowq send orders '{"id": 3}'
sent 3
```

Message 1 was taken before the crash and stays gone; message 2 was not and comes back; the counter continues at 3. In Go:

```go
seq, err := b.Send(ctx, "orders", queue.Message{
    MessageID:   "order-1",
    ContentType: "application/json",
    Body:        []byte(`{"id": 1}`),
})
// seq == 1
m, err := b.Receive(ctx, "orders")
// m.SequenceNumber == 1, m.EnqueuedTime set by the broker
```

## Done when

- [ ] Send 1,000 messages, `kill -9` the broker, restart it: `Receive` returns all 1,000 exactly once, in order
- [ ] Sequence numbers only go up, across a restart too
- [ ] Messages received before the crash do not come back after it
- [ ] `burrowq send` and `burrowq receive` round-trip a body through the running broker
- [ ] `gofmt -l .` and `go vet ./...` print nothing

## Reference

```go
package queue

type Message struct {
    MessageID, ContentType, CorrelationID, Subject string
    Body                  []byte
    ApplicationProperties map[string]any
    SequenceNumber        uint64    // set by the broker
    EnqueuedTime          time.Time // set by the broker
}

func (b *Broker) Send(ctx, queue string, m Message) (uint64, error)
// receive-and-delete
func (b *Broker) Receive(ctx, queue string) (Message, error)
```

- Message encoding (JSON body inside a Record)
- Queue frames on wire.ServeConns
- WAL before ack; sequence numbers rebuilt on replay
- CLI

## The tests check

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/Q.1/files/`, matching the earlier chapters.

The suite will cover:

- Order and exactly-once after kill -9
- Sequence numbers monotonic across restart
- CLI round trip

## Try it

```bash
./burrow test
go run ./cmd/burrowd serve -queue-addr 127.0.0.1:7090 -queue-dir ./q &
go run ./cmd/burrowq send orders hello
go run ./cmd/burrowq receive orders
```

## In the wild

Azure Service Bus queues, SQS, RabbitMQ classic queues

## New in Go

struct design · JSON struct tags and encoding rules · monotonic counters · reusing the WAL

Standard library: `encoding/json`, `sync`, `time`
