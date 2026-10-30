# 10.5 · Read Index

> A read that might be stale is a guess. Make the default read a promise, without paying for a log entry.

## Your goal

Make `Get` **linearizable**: after a write returns anywhere in the cluster, a read anywhere in the cluster sees it. Right now `Get` reads the local store, which on a follower may lag, and on a leader that has just been deposed may be wrong without knowing it. The slow fix is to push every read through the log like a write. Raft's cheaper answer is the **read index**: the leader confirms it is still the leader with one heartbeat round, waits until its apply loop has caught up to what was committed at that moment, and then reads locally. Clients that are happy with a possibly stale answer can ask for `?consistency=local`.

## What you're using from before

**Leader-aware API (10.4 · Follow the Leader).** Followers already redirect writes to the leader. A default `GET` now redirects the same way (only the leader can run a read-index check), while `?consistency=local` is served wherever it lands. The handler reads the query parameter and picks the method:

```go
switch r.URL.Query().Get("consistency") {
case "", "linearizable":
	e, err = kv.Get(ctx, key)      // read-index check, leader only
case "local":
	e, err = kv.LocalGet(ctx, key) // whatever this node has applied
default:
	// 400 invalid_request
}
```

`burrow.Client` follows the redirect and the leader cache like it does for writes.

**Quorum (9.3 · Quorum).** The confirmation round is one `quorum.Call` of empty `AppendEntries` (heartbeats) to every peer with `k` = majority minus self, the same call 10.2's heartbeat tick already makes. If `Call` returns an error, a majority did not answer, and this node must assume it is no longer leader.

## What's new here

**The read index, in plain terms.** When a read arrives at the leader:

1. Note `commitIndex` right now. Call it `readIndex`.
2. Send one heartbeat round and wait for a majority to acknowledge. Now you know you were still the leader at the time of step 1, so everything up to `readIndex` really is committed and nothing newer can have been committed by anyone else.
3. Wait until your apply loop's `applied` reaches `readIndex`.
4. Read the local store and answer.

Step 2 is the part that costs a network round trip; step 3 usually costs nothing because the leader is normally caught up. There is one precondition from the paper (section 8): a brand-new leader does not know which of its older entries are committed until it has committed an entry from its own term. So a leader proposes an empty entry the moment it wins, and reads wait until that entry is applied.

**Batching requests.** A hundred reads arriving in the same millisecond do not need a hundred heartbeat rounds. One round confirms leadership for all of them: every read that noted its `readIndex` before the round went out is covered by it. So the `Run` loop collects read requests into a pending list and, on the next heartbeat tick (or straight away if none is in flight), sends one round and then releases every waiter whose `readIndex` the round confirmed. Each read waits on its own reply channel, like `Status()` does:

```go
type readIndex struct{ reply chan readIndexResult }

// in the loop
case m := <-n.inbox: // a readIndex message
	n.pendingReads = append(n.pendingReads, pendingRead{
		index: n.commitIndex, reply: m.reply,
	})
	n.kickHeartbeat() // make the next round happen now, not in 50 ms
```

When the round's result comes back as one message, the loop walks `pendingReads`, replies to each with its index (or with an error if the round failed), and empties the slice. The `KV` side then waits for `applied >= index` before reading.

**Quorum reuse.** Nothing about `quorum.Call` is specific to votes or log entries; the same function with the same `k` now serves a third purpose. That is what a generic function buys you.

**Waiting for applied.** The apply loop from 10.3 tracks `applied`. Reads need to block until it reaches a value. A `sync.Cond` works, but a simpler pattern is a channel per waiting index, or the apply loop broadcasting its progress by closing a channel it swaps out on each step. Pick one and keep it inside `KV`.

## What to do

1. Have a newly elected leader `Propose` an empty command on winning, so a current-term entry commits early.
2. Add a `readIndex` message type and a `pendingReads` list to the `Run` loop; a read notes `commitIndex` and waits for the next heartbeat round.
3. On the round's result, release every pending read with its index, or with an error when the round did not reach a majority (a deposed leader fails the read instead of answering).
4. In `KV.Get`, ask the loop for a read index, wait for `applied` to reach it, then read the local store. Return `ErrNotLeader` on a follower so the API redirects.
5. Add `KV.LocalGet` (or a flag on `Get`) that reads the local store with no check.
6. Wire `?consistency=` in the API: default and `linearizable` go through `KV.Get` (redirecting on followers), `local` goes through the local read, anything else is 400 `invalid_request`.
7. Write the stale-read test: write on `n1`, read on `n2` immediately, 1,000 times, with an injected apply delay on `n2`. Default mode never sees the old value; `local` mode sees it at least once.

## Example

```bash
$ go run ./cmd/burrowctl -http http://127.0.0.1:8082 set counter 41
OK 1
$ go run ./cmd/burrowctl -http http://127.0.0.1:8083 get counter
41
$ curl -s 'localhost:8083/v1/kv/counter?consistency=local'
41
```

The second command went to a follower, was redirected to the leader, waited one heartbeat round, and answered. The `curl` with `local` was answered by the follower from whatever it had applied; a moment after the write it could have printed the previous value.

## Done when

- [ ] 1,000 write-then-read pairs through different nodes never return a stale value in default mode
- [ ] `?consistency=local` returns a stale value at least once under an injected apply delay
- [ ] 100 concurrent linearizable reads share one heartbeat round
- [ ] A leader that cannot reach a majority fails the read instead of answering from its local store

## Reference

The exact contract the tests will hold you to.

```go
// GET /v1/kv/{key}[?consistency=linearizable|local]; default
// linearizable: served by the leader after a read-index check.
// Get is linearizable.
func (kv *KV) Get(ctx context.Context, key string) (store.Entry, error)
```

What this level builds:

- ReadIndex: note commitIndex, one heartbeat round via quorum.Call, wait for applied >= it
- Batch concurrent reads into one round
- ?consistency=local

Decisions the tests rely on:

- A default `GET` on a follower is redirected like a write (307 with `Location` and `X-Burrow-Leader`); a `local` read is served by any node.
- A read-index round that does not reach a majority returns an error; the read is never served from a possibly deposed leader.
- A new leader proposes an empty entry when it wins and linearizable reads wait for it to apply.

## The tests check

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/10.5/files/`, matching the earlier chapters.

The suite will cover:

- No stale reads across 1,000 write-then-read pairs through different nodes
- local mode observes staleness at least once under an injected delay
- Read index round shares one heartbeat for 100 concurrent reads

## Try it

```bash
./burrow watch
go run ./cmd/devcluster
curl -s 'localhost:8083/v1/kv/counter?consistency=local'
```

## In the wild

etcd's ReadIndex and serializable reads; Raft paper section 8

## New in Go

batching requests · quorum reuse

Standard library: `context`, `sync`
