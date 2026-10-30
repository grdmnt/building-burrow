# 10.2 · Log Shipping

> A leader is only useful if what it decides survives it. Now the leader keeps a log and ships it to everyone.

## Your goal

Give the leader from 10.1 a **replicated log**: a numbered list of commands that the leader appends to and copies to its followers. An entry is **committed** once a majority of nodes have it on their logs, and a committed entry can never be lost or reordered, even if the leader dies a moment later. That guarantee is the whole reason to run Raft, and it is what 10.3 will build the key-value store on.

This is the hardest level in the curriculum. Figure 2 of the Raft paper is the spec; this page translates it into steps.

## What you're using from before

**Leader election (10.1 · Election).** The `Node`, its `Run` loop with the inbox, and the `Transport`. This level adds a second RPC to the transport, `AppendEntries`, and more message types to the inbox. The heartbeat you already send is an `AppendEntries` with no entries, so the heartbeat timer case in your `select` becomes the replication tick:

```go
case <-heartbeatTimer.C:
	n.replicate() // AppendEntries to every peer, empty or not
```

`Propose` is a new way into the loop, the same shape as `Status()`: a message with a reply channel.

```go
type propose struct {
	cmd   []byte
	reply chan proposeResult // index, or ErrNotLeader naming the leader
}
```

**Quorum (9.3 · Quorum).** `quorum.Call[T]` fans out one function per peer and returns as soon as `k` succeed. One replication round is one call: each `fn` sends `AppendEntries` to a peer and returns the index that peer now matches. As soon as a majority has an entry it is committed, which is exactly when `Call` returns, so `k` is again the majority minus your own copy:

```go
res, err := quorum.Call(ctx, need, fns) // []Result[uint64]
```

Send the results back to the loop as one message; the loop updates `matchIndex` for the peers that answered and advances `commitIndex`. Peers whose call was cancelled by the early return simply get the next round.

## What's new here

**The log.** Each node keeps a slice of entries. An entry has an `Index` (its position, starting at 1), the `Term` of the leader that created it, and the `Command` bytes. The leader appends a new entry at the next index and tries to copy it to everyone; followers only ever take entries from the leader.

```go
type Entry struct {
	Index   uint64
	Term    uint64
	Command []byte
}
```

**Log matching.** Raft's trick for keeping logs identical is that every `AppendEntries` request names the entry just before the ones it carries: `PrevLogIndex` and `PrevLogTerm`. The follower checks "do I have an entry at that index with that term?" If yes, it appends the new entries (deleting anything of its own that conflicts at those positions). If no, it refuses, and the leader tries again one index earlier. Because the check is recursive, a match at one position means both logs are identical up to it. That is the log matching property, and it is why a follower that was cut off for a while catches up by itself.

```go
// follower side, in plain terms
if req.PrevLogIndex > 0 {
	e, ok := n.entryAt(req.PrevLogIndex)
	if !ok || e.Term != req.PrevLogTerm {
		return AppendEntriesResponse{Term: n.term, Success: false}
	}
}
for i, e := range req.Entries {
	if have, ok := n.entryAt(e.Index); ok && have.Term == e.Term {
		continue // agrees: never delete it
	}
	n.truncateFrom(e.Index) // first conflict: drop it and all after
	n.log = append(n.log, req.Entries[i:]...)
	break
}
```

**Per-follower bookkeeping.** The leader keeps two numbers for each peer. `nextIndex` is the index it will send next (optimistically the end of its own log; one lower after every refusal). `matchIndex` is the highest index the peer has confirmed. The leader's own `matchIndex` is the end of its log.

**Majority math.** An index is committed when a majority of `matchIndex` values are at or above it. For 3 nodes, sort the three `matchIndex` values (your own included) and take the middle one; for `n` nodes, sort ascending and take the value at position `(n-1)/2`. A majority of nodes are at or above it. One subtlety, Figure 8 in the paper: the leader may only commit an entry **from its own term** this way. Entries from older terms commit as a side effect, once a newer entry after them commits. Skip this rule and a lost-and-recovered leader can overwrite an entry a client was already told was committed.

```go
sorted := slices.Sorted(maps.Values(matchIndex)) // includes self
candidate := sorted[(len(sorted)-1)/2]
if candidate > n.commitIndex && n.termAt(candidate) == n.term {
	n.commitIndex = candidate
}
```

(`slices.Sorted` and `maps.Values` are Go 1.23 and work on any map of ordered values.)

**Delivering commits.** A node delivers entries up to `commitIndex` on `Committed()`, in index order, each exactly once. Followers learn the leader's `commitIndex` from the `LeaderCommit` field of every `AppendEntries`, so they deliver too, a little behind.

**Invariant tests.** The tests for this level do not check one output. They run the cluster through a scenario and after every step assert properties that must always hold: no two leaders in one term, every pair of logs agrees on every index they both have, nothing committed ever changes. Write a `checkInvariants(t, nodes)` helper and call it in a loop while the scenario runs. Finding a violation on step 37 of run 82 is normal for this level; log every RPC with its term and indexes so you can read what happened.

## What to do

1. Add `Entry`, the `AppendEntriesRequest` and `AppendEntriesResponse` types from Figure 2, and `AppendEntries` to `Transport` (both the RPC-backed and in-memory versions).
2. Give `Node` a log, `commitIndex`, and per-peer `nextIndex` and `matchIndex` maps that the leader initialises when it wins.
3. Write `Propose`: send a message to the loop; the loop appends the entry with the current term and replies with its index, or with `ErrNotLeader` carrying the leader's ID when this node is not the leader.
4. On every heartbeat tick, the leader sends each peer the entries from its `nextIndex` onward, with `PrevLogIndex`/`PrevLogTerm` set to the entry before. Use `quorum.Call` for the round and feed the results back to the loop as one message.
5. Handle the follower side: step down on a higher term, reset the election timer, run the log matching check, truncate conflicts, append, and move your own `commitIndex` up to `min(LeaderCommit, last index)`.
6. Handle the leader side of each reply: on success set `matchIndex` and `nextIndex`; on refusal lower `nextIndex` by one and let the next round retry. That is slow for a follower that is far behind, but correct; make it smarter later if you want.
7. Recompute `commitIndex` after every successful round using the majority rule, only through entries of the current term.
8. Deliver newly committed entries on `Committed()` in order, from the loop or a small helper goroutine it owns.
9. Write your invariant checker and run the partition scenario under `-count=100`.

## Example

Three nodes; propose on the leader, watch commits arrive everywhere:

```go
idx, err := leader.Propose(ctx, []byte("set a 1"))
// idx == 1, err == nil
e := <-follower.Committed()
fmt.Println(e.Index, e.Term, string(e.Command))
```

```
1 1 set a 1
```

Propose on a follower instead and you get told who to talk to:

```go
_, err = follower.Propose(ctx, []byte("set b 2"))
var nl *raft.ErrNotLeader
errors.As(err, &nl) // nl.Leader == "n2"
```

Partition `n3` (make its transport drop everything), propose 100 entries, heal it. Within a few heartbeats `n3`'s log is identical to the other two, and every node has delivered indexes 1 to 101 on `Committed()` in order.

## Done when

- [ ] After a follower is partitioned, 100 entries are proposed, and it is healed, all three logs are identical
- [ ] An entry from an older term is never committed on its own, only when a current-term entry after it commits (Figure 8)
- [ ] Heartbeats keep followers from starting elections while the leader is alive
- [ ] `Propose` on a follower returns `ErrNotLeader` naming the leader
- [ ] `Committed()` delivers every committed entry exactly once, in index order, on every node

## Reference

The exact contract the tests will hold you to.

```go
// Propose returns ErrNotLeader with the leader's ID on a follower.
func (n *Node) Propose(ctx context.Context, cmd []byte) (uint64, error)
// Committed entries are delivered on n.Committed(), in index order.
func (n *Node) Committed() <-chan Entry

type Entry struct {
	Index   uint64
	Term    uint64
	Command []byte
}

type ErrNotLeader struct{ Leader string } // "" during an election
func (e *ErrNotLeader) Error() string     // returned as *ErrNotLeader

// Transport gains AppendEntries(ctx, peer, req) (resp, error), with the
// Figure 2 fields: term, leaderId, prevLogIndex, prevLogTerm, entries,
// leaderCommit in; term, success out.
```

What this level builds:

- In-memory log with truncate-on-conflict semantics
- AppendEntries request/response
- nextIndex/matchIndex and commitIndex
- Catch-up after partition

Decisions the tests rely on:

- Indexes start at 1. `PrevLogIndex` 0 means "the start of the log" and always matches.
- A follower truncates its log from the first conflicting index and appends the leader's entries; it never deletes entries that agree.
- The leader advances `commitIndex` only to indexes whose term is the current term; older entries commit by being before such an index.
- `Committed()` is in index order and never repeats an index.

## The tests check

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/10.2/files/`, matching the earlier chapters.

The suite will cover:

- Logs identical after heal
- Figure 8 scenario does not commit a stale entry
- Heartbeats keep followers from electing
- Propose on a follower returns ErrNotLeader naming the leader

## Try it

```bash
./burrow watch
go test -race -count=100 -run Partition ./raft
```

## In the wild

etcd/raft's log.go; Kafka ISR replication is the non-consensus cousin

## New in Go

log matching · majority math · invariant tests · slices.Sorted and maps.Values

Standard library: `sync`, `context`, `slices`, `maps`
