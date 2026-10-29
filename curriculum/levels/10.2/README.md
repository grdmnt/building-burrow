# 10.2 · Log Shipping

The leader replicates a log; entries commit once a majority has them. This is the hardest level in the curriculum; Figure 2 of the Raft paper is the spec.

## Build

```go
func (n *Node) Propose(ctx context.Context, cmd []byte) (index uint64, err error) // ErrNotLeader with the leader's ID
// Committed entries are delivered on n.Committed() <-chan Entry (consumed by 10.3).
```

- In-memory log with truncate-on-conflict semantics
- AppendEntries request/response
- nextIndex/matchIndex and commitIndex
- Catch-up after partition

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/10.2/files/`, matching the earlier chapters.

## Pass when

Partition a follower, write 100 entries, heal it: it catches up and all three logs are identical; entries from older terms commit only via a current-term entry.

The suite will cover:

- Logs identical after heal
- Figure 8 scenario does not commit a stale entry
- Heartbeats keep followers from electing
- Propose on a follower returns ErrNotLeader naming the leader

## Uses

- Leader election (built in 10.1 · Election)
- Quorum (built in 9.3 · Quorum)

## In the wild

etcd/raft's log.go; Kafka ISR replication is the non-consensus cousin

## New in Go

log matching · majority math · invariant tests

Standard library: `sync`, `context`
