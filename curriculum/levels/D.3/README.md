# D.3 · The Other Road

The other road. Point the chaos scenario and the checker at the Dynamo-style cluster and read the failure.

## Build

```go
// scenario -backend dynamo
```

- Backend switch in the scenario runner
- Run, collect violations
- Write the comparison

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/D.3/files/`, matching the earlier chapters.

## Pass when

The checker fails the Dynamo backend on lost writes or lock exclusivity under partition, names the op, and your write-up explains it in terms of W+R>N and clock skew.

The suite will cover:

- The scenario reports at least one violation under partition for the dynamo backend and none for Raft

## Uses

- Quorum replication (built in D.2 · Redundancy)
- Checker (built in 11.3 · Checker)
- Chaos proxy (built in 11.1 · Fault Injector)

## In the wild

Jepsen's analyses of Riak and Cassandra

## New in Go

consistency models

Standard library: `everything so far`
