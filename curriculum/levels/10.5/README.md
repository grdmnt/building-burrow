# 10.5 · Read Index

Linearizable reads without writing to the log: confirm leadership with a quorum, then wait for the apply index to catch up.

## Build

```go
// GET /v1/kv/{key}[?consistency=linearizable|local]; default linearizable: served by the leader after a read-index check.
func (kv *KV) Get(ctx, key) (store.Entry, error) // linearizable
```

- ReadIndex: note commitIndex, one heartbeat round via quorum.Call, wait for applied >= it
- Batch concurrent reads into one round
- ?consistency=local

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/10.5/files/`, matching the earlier chapters.

## Pass when

A test that writes on n1 and immediately reads on n2 never sees stale data in default mode; local mode can.

The suite will cover:

- No stale reads across 1,000 write-then-read pairs through different nodes
- local mode observes staleness at least once under an injected delay
- Read index round shares one heartbeat for 100 concurrent reads

## Uses

- Leader-aware API (built in 10.4 · Follow the Leader)
- Quorum (built in 9.3 · Quorum)

## In the wild

etcd's ReadIndex and serializable reads; Raft paper section 8

## New in Go

batching requests · quorum reuse

Standard library: `context`, `sync`
