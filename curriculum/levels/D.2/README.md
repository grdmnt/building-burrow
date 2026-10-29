# D.2 · Redundancy

Store each key on three nodes and survive losing one, with the quorum helper you wrote for Raft.

## Build

```go
func (d *Store) Set(...) // fan out with quorum.Call[ack]; Get with quorum.Call[store.Entry]
```

- Replica selection
- W/R quorums
- LWW with timestamps
- Read repair

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/D.2/files/`, matching the earlier chapters.

## Pass when

With one node down, reads and writes still succeed; after it restarts, read repair brings its copy up to date; goroutine counts match before and after.

The suite will cover:

- Availability with one node down
- Read repair
- No leaks

## Uses

- Hash ring (built in D.1 · Address Decoder)
- Quorum (built in 9.3 · Quorum)
- RPC (built in 9.2 · Interconnect)

## In the wild

Dynamo, Riak, Cassandra

## New in Go

fan-out/fan-in reuse · versioning

Standard library: `context`, `sync`, `time`
