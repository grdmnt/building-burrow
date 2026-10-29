# 10.3 · Apply Loop

A state machine fed by committed entries: apply in order, wake the waiter for each index, and publish watch events numbered by log index so resume works on any node.

## Build

```go
type KV struct { /* yours */ }
func NewKV(n *Node, st store.Store, b *watch.Broker) *KV
func (kv *KV) Set(ctx, key, value) (store.Entry, error) // proposes a command, waits for apply, returns the applied entry; same for SetWithTTL, Delete, CompareAndSwap, CompareAndDelete, Touch
// Reads (Get/Keys) are local for now; 10.5 makes them linearizable.
var _ store.Store = (*KV)(nil) // the fourth Store; storetest.Run passes through Raft
```

- Command encoding (reuse the durable change-record format)
- Apply loop with map[index]chan result
- Broker publish with Event.ID = index
- Leadership-change errors for waiters

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/10.3/files/`, matching the earlier chapters.

## Pass when

storetest.Run, RunCAS and RunTTL pass through a 3-node in-memory cluster; a watcher on n2 resuming from ID 40 sees entries 41.. exactly as applied on n1.

The suite will cover:

- Conformance through Raft
- Waiter gets an error when the term changes before commit
- Watch IDs equal indexes on every node; resume on another node has no gaps

## Uses

- Replicated log (built in 10.2 · Log Shipping)
- Memory store (built in 1.1 · Register File)
- Broker (built in 8.2 · Fan-out)

## In the wild

etcd's mvcc apply path; the FSM in hashicorp/raft

## New in Go

apply loops · waiting on results by index · linearizable writes

Standard library: `context`, `sync`
