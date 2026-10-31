# 10.6 · Non-volatile Raft

> Consensus that forgets after a reboot is theatre. Put the term, the vote and the log on disk, in that order, before you open your mouth.

## Your goal

Make the cluster survive a restart of every node at once with no acknowledged write lost. Raft needs three things on disk: the **current term**, **who this node voted for** in it, and the **log entries**. Everything else (commit index, who the leader is) can be relearned. The rule that makes it safe is about *when*: a node writes the term and vote before it replies to a `RequestVote`, and writes entries before it replies to an `AppendEntries`. Forget that order and a node can vote twice in one term after a crash, which is how you get two leaders.

Nothing here is a new file format. Your WAL from chapter 4 becomes Raft's log, its `Truncate` resolves conflicts, its CRC guards consensus state, and its group commit absorbs a burst of writes with one fsync. The snapshotter bounds the log.

## What you're using from before

**Raft KV (10.3 · Apply Loop).** The `Node` and its `Run` loop are what gain persistence. Three moments in the loop now touch disk: adopting a new term, granting a vote, and accepting entries. Each one writes before the reply goes out:

```go
case requestVote:
	resp := n.handleRequestVote(m.from, m.req) // may change term, vote
	if n.stateDirty {
		n.persistState() // state record on disk first...
	}
	m.reply <- resp // ...and only then the answer
```

The apply loop's `applied` index becomes the snapshot's high-water mark.

**CRC guard (4.3 · Parity Bits).** `wal.Open` verifies a CRC on every record, repairs a torn tail, and refuses a corrupt middle. `Len()` and `Truncate(n)` cut the log at a record boundary. On an `AppendEntries` conflict at log index `i`, the node truncates its WAL to the record count that keeps everything before `i`:

```go
// recs[i] is the WAL record number holding log index i
if err := n.wal.Truncate(recs[conflict]); err != nil {
	return err
}
```

**Group commit (4.5 · Overclock).** `Append` returns once its record is fsynced, and concurrent `Append`s share one fsync. That only helps if appends are concurrent. If the `Run` goroutine calls `Append` itself, every write queues behind one fsync at a time. So the goroutine that called `Propose` does the `Append` after the loop has assigned the index, and tells the loop when it is on disk:

```go
// in Propose, after the loop replied with idx and the encoded record
if err := n.wal.Append(rec); err != nil {
	return 0, err
}
n.inbox <- persisted{index: idx} // leader's own matchIndex moves here
```

A hundred handlers doing this at once is the burst 4.5's writer goroutine turns into a single `fsync`.

**Snapshotter (4.4 · Compaction).** `Memory.Snapshot()` returns a consistent copy of every live entry, and 4.4's write-tmp, fsync, rename, fsync-the-directory sequence makes `snapshot.db` appear atomically. The Raft snapshot adds one thing to the front: the index and term of the last entry it includes.

```go
entries := mem.Snapshot() // the *store.Memory under raft.KV (4.4)
// write lastIndex, lastTerm, then the 4.4 format (gob + crc32)
// to snapshot.tmp; fsync; rename to snapshot.db; fsync the dir
```

## What's new here

**Durability ordering.** The rule is "persist, then reply". Why it is not negotiable:

- Vote for `n2` in term 7, crash, restart with term 6 on disk, get asked by `n3` for term 7, vote again. Two votes from one node, two leaders in term 7.
- Reply "success" to an `AppendEntries`, crash before fsync. The leader counts your copy toward a majority, commits, acknowledges the client. Your copy is gone. If the leader dies next, the entry may be lost from the cluster entirely.

So `raft.log` holds two kinds of record: a **state record** (current term, voted for) and an **entry record** (index, term, command). Every time the term or vote changes, append a state record *before* sending the reply or the vote request that depends on it. The latest state record wins at replay. Every batch of accepted entries is appended *before* the success reply.

```go
// what replay of raft.log does
for rec, err := range l.Records() { // l is the *wal.Log
	switch kind(rec) {
	case recState:
		term, votedFor = decodeState(rec)
	case recEntry:
		entries = append(entries, decodeEntry(rec))
	}
}
```

**Terms never go backwards.** After a restart the node starts from the term on disk, never 0. The tests restart all three nodes and check the term after is at least the term before.

**Conflict truncation.** When the log matching check in 10.2 finds a conflict at index `i`, the in-memory log drops `i` onward and so must the WAL. Keep an index from log index to WAL record number (the WAL mixes state records in, so record 40 is not index 40). After `Truncate`, append a fresh state record so the current term and vote are still the last word in the file.

**Snapshots bounded by followers.** The log cannot grow forever. After `applied` passes a threshold, snapshot the store (`Memory.Snapshot()`, plus the index and term of the last applied entry) and rewrite the log without the entries the snapshot covers. But only drop entries that *every* follower already has, `min(matchIndex)`: a slow follower still needs them, and this curriculum does not build `InstallSnapshot`. Rewriting the log is the 4.4 idiom again: build `raft.log.tmp` with a state record, a record saying "entries before index T live in the snapshot", and the kept entries; fsync; rename over `raft.log`; reopen.

**Restart recovery.** `Open` the snapshot first (refuse on a bad checksum), `Restore` its entries into the store, set `applied` to its index. Then open the WAL and replay: state records set the term and vote, entry records rebuild the in-memory log from the snapshot's index onward. `commitIndex` starts at the snapshot's index and climbs again as the leader's heartbeats say so; entries after it are re-applied as they re-commit, which is safe because the apply loop only applies past `applied`.

**Reuse across layers.** Nothing in `wal` knows about Raft and nothing in `raft` knows about files beyond the two names. Each layer was built and tested on its own, and the durability guarantee of this level is the sum of guarantees you already proved.

## What to do

1. Add `-data <dir>` wiring for the cluster: `raft.log` and `snapshot.db` in that directory, opened by `New` or a new `Open`.
2. Define the two record kinds and their encoding; write a round-trip test.
3. Append a state record on every term change and every vote, before the reply or request that depends on it. Append entry records before every success reply to `AppendEntries`, and let `Propose`'s caller append the leader's own entry.
4. Keep the log-index-to-record-number index and use `wal.Log.Truncate` on conflict, followed by a fresh state record.
5. Snapshot when the log passes a threshold: `Memory.Snapshot()` with the applied index and term in front, written with the 4.4 tmp/rename sequence; then rewrite the log keeping entries from `min(matchIndex)` onward.
6. Implement recovery: snapshot, then replay; start with the persisted term.
7. Measure a write storm with group commit: 100 clients writing through `burrow.Client` on loopback should sustain at least 2,000 writes per second.
8. Try a full restart by hand with `devcluster -data ./data`, as in the example. Without `-data` each run gets fresh temp directories and there is nothing to recover.

## Example

Run devcluster with `-data` so every node keeps its directory (`./data/node1` and so on) and a second run finds the first run's files.

```bash
$ go run ./cmd/devcluster -data ./data &
$ for i in $(seq 1 100); do
    go run ./cmd/burrowctl -http http://127.0.0.1:8081 set k$i v$i; done
$ kill -9 $(pgrep burrowd)        # all three, no warning
$ kill %1                         # and devcluster itself
$ go run ./cmd/devcluster -data ./data &
$ go run ./cmd/burrowctl -http http://127.0.0.1:8081 get k100
v100
$ curl -s localhost:8081/v1/cluster | jq .term
3
```

The term after the restart is higher than before (a new election ran), never lower, and every acknowledged key is back.

## Done when

- [ ] Restarting all three nodes at once keeps every acknowledged write
- [ ] A node restarted in the middle of an election does not vote twice in the same term
- [ ] A conflict resolved with `wal.Log.Truncate` leaves the follower's log identical to the leader's
- [ ] Compaction never drops entries a live follower has not received (`min(matchIndex)`)
- [ ] Terms never go backwards across a restart
- [ ] A write storm sustains at least 2,000 writes per second on loopback

## Reference

The exact contract the tests will hold you to.

```go
// burrowd -data <dir>: raft state at <dir>/raft.log (term/vote records
// and entries), KV snapshots at <dir>/snapshot.db.
// Compaction keeps entries from min(matchIndex) onward; the log is
// truncated with wal.Log.Truncate on conflict.
// snapshot.db is the 4.4 format (gob then crc32) with the index and
// term of the last included entry encoded before the entries.
```

What this level builds:

- Persist currentTerm/votedFor/entries via WAL before replying
- Truncate on AppendEntries conflict
- Snapshot and log trim bounded by followers
- Restart recovery

Decisions the tests rely on:

- A state record (term, votedFor) is on disk before any reply or request that depends on it; an entry is on disk before the success reply that acknowledges it.
- After a restart the node's term is the last persisted term, never lower.
- A snapshot never includes entries past `applied`, and compaction never removes entries at or after `min(matchIndex)` over live followers.
- `snapshot.tmp` is never trusted; a snapshot with a bad checksum stops startup.

## The tests check

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/10.6/files/`, matching the earlier chapters.

The suite will cover:

- Full restart keeps every acknowledged write
- Vote persisted: a node restarted mid-election doesn't vote twice
- Conflict truncation via WAL leaves the log identical to the leader's
- Compaction never trims past a live follower's matchIndex

## Try it

```bash
./burrow watch
go run ./cmd/devcluster -data ./data
go test -bench=Storm -run='^$' ./raft
```

## In the wild

etcd's wal + snap packages; hashicorp/raft-boltdb

## New in Go

durability ordering · reuse across layers

Standard library: `os`
