# 11.2 · History Recorder

Record what clients saw. Four probes run at once: plain reads and writes, CAS counters, lease locks appending fencing tokens, and a watch consumer.

## Build

```go
type Op struct { Client int; Kind string; Key string; Value string; Token uint64; Start, End time.Time; OK bool; Err string; Version uint64 }
// Probes: kv (random put/get), counter (CAS increments), lock (acquire, append token to a shared log key, release), watch (records every event ID seen).
```

- Probe goroutines on burrow.Client
- JSON-lines writer
- Seeded workload

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/11.2/files/`, matching the earlier chapters.

## Pass when

A 30 s run against devcluster produces a history that the 11.3 checker accepts, with every probe type represented.

The suite will cover:

- Every op has start < end and a kind
- Watch probe records contiguous IDs
- Seeded runs produce the same op sequence

## Uses

- HTTP client lib (built in 7.5 · Client Library)
- Lease lock (built in 8.1 · Lease Lock)
- Watch client (built in 8.5 · Watch Client)

## In the wild

Jepsen's generators; k6 and vegeta for plain load

## New in Go

concurrent clients · JSON lines · time measurement

Standard library: `encoding/json`, `time`, `sync`, `math/rand/v2`
