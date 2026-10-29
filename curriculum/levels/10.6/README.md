# 10.6 · Non-volatile Raft

What must be durable and when. Raft's log is your WAL: term and vote before replying, `Truncate` on conflict, the CRC guard protecting consensus state, group commit absorbing a write storm.

## Build

```go
// burrowd -data <dir>: raft state at <dir>/raft.log (term/vote records and entries), KV snapshots at <dir>/snapshot.db.
// Compaction keeps entries from min(matchIndex) onward; the log is truncated with wal.Log.Truncate on conflict.
```

- Persist currentTerm/votedFor/entries via WAL before replying
- Truncate on AppendEntries conflict
- Snapshot and log trim bounded by followers
- Restart recovery

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/10.6/files/`, matching the earlier chapters.

## Pass when

Restart all three nodes at once: no acknowledged write is lost and terms never go backwards; a write storm with group commit sustains at least 2,000 writes/s on loopback.

The suite will cover:

- Full restart keeps every acknowledged write
- Vote persisted: a node restarted mid-election doesn't vote twice
- Conflict truncation via WAL leaves the log identical to the leader's
- Compaction never trims past a live follower's matchIndex

## Uses

- Raft KV (built in 10.3 · Apply Loop)
- CRC guard (built in 4.3 · Parity Bits)
- Group commit (built in 4.5 · Overclock)
- Snapshotter (built in 4.4 · Compaction)

## In the wild

etcd's wal + snap packages; hashicorp/raft-boltdb

## New in Go

durability ordering · reuse across layers

Standard library: `os`
