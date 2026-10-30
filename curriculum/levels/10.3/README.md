# 10.3 · Apply Loop

> A replicated log is just bytes in order. A state machine that eats those bytes is what turns three logs into one database.

## Your goal

Build `raft.KV`, one more `store.Store`: writes go through `Propose`, wait until the entry is committed and applied, and only then return. Every node runs the same **apply loop**: read committed entries in order, run each command against its local `store.Memory`, publish a watch event numbered by the entry's index. Because every node applies the same commands in the same order, every node ends up with the same data, and watch IDs mean the same thing on every node, so a watcher can resume on any of them.

## What you're using from before

**Replicated log (10.2 · Log Shipping).** `n.Propose(ctx, cmd)` gets a command into the log and returns its index; `n.Committed()` delivers committed entries in order. `KV.Set` encodes the operation, proposes it, then waits for the apply loop to reach that index:

```go
idx, err := kv.node.Propose(ctx, encodeCommand(cmd))
// ... register a waiter for idx, then block on it
```

The apply loop is one goroutine ranging over the channel:

```go
for e := range kv.node.Committed() {
	res := kv.apply(e) // run the command, build the result
	kv.wake(e.Index, res)
}
```

**Memory store (1.1 · Register File).** The `store.Store` methods, with CAS and TTL from 1.3 and 1.4. `NewKV` takes any `store.Store`, and the apply loop calls its methods in log order. Nothing else writes to that store, which is what makes versions identical everywhere:

```go
case opSet:
	entry, err = kv.st.Set(ctx, c.Key, c.Value)
case opCAS:
	entry, err = kv.st.CompareAndSwap(ctx, c.Key, c.IfVersion, c.Value)
```

**Broker (8.2 · Fan-out).** `b.Publish(watch.Event{...})` fans an event out to every subscriber whose prefix matches. The apply loop publishes after every successful mutation, with `ID` set to the log index rather than a counter of its own:

```go
kv.broker.Publish(watch.Event{ID: e.Index, Type: "put", Entry: entry})
```

## What's new here

**State machine replication.** The idea behind the whole chapter: if every node starts from the same empty store and applies the same commands in the same order, every node holds the same data. The log provides the order; the apply loop provides the "same commands". Any decision a command needs (does the CAS match? what is the new version?) is made during apply, on every node, never on the leader before proposing. If the leader decided and shipped the result, a second proposal in flight at the same time could be decided against stale state.

The one thing that must not be decided locally is time. A `SetWithTTL` with `ttl: 10s` applied two seconds late on a follower would expire two seconds later there. So the leader turns the TTL into an absolute `ExpiresAt` before proposing, and the apply loop computes the remaining TTL from that instant.

**Encoding a command.** A command is a small binary record: an op byte (set, set with TTL, delete, compare-and-swap, compare-and-delete, touch), the key, the value, and the fields the op needs (`IfVersion`, `ExpiresAt`). Use the same style as the 4.2 change records, big-endian lengths and `encoding/binary`, so decoding is a mirror of encoding and never guesses.

**Apply loops.** The apply loop is an actor like `Run` in 10.1: one goroutine, one input channel (`Committed()`), and the store as the state it owns. It tracks `applied`, the last index it has run, and never skips or repeats an index. Reads that are served locally (`Get`, `Keys`, for now) just call the store; they see whatever has been applied so far.

**Waiting on results by index.** The handler that proposed index 42 needs the result of applying index 42. Keep `map[uint64]chan result`: the handler makes a buffered channel, registers it under its index, and waits; the apply loop looks the index up, sends the result, and deletes the entry. Two goroutines touch the map, so guard it with a `sync.Mutex`. This map belongs to `KV`, not to the Raft state the `Run` goroutine owns, so the "no mutex" rule of 10.1 does not apply to it.

There is a race to get right: if the handler registers *after* `Propose` returns, the apply loop can get to index 42 first, find no waiter, and move on, leaving the handler waiting forever. Hold the lock across the call:

```go
kv.mu.Lock()
idx, err := kv.node.Propose(ctx, cmd)
if err == nil {
	kv.waiters[idx] = ch
}
kv.mu.Unlock()
```

The apply loop takes the same lock to look a waiter up, so it cannot reach 42 before the waiter is in the map. This is check-then-act from 1.2 again. It is safe only because the `Run` loop never blocks on delivering to `Committed()` (10.2 delivers from a helper goroutine or a buffer). If it did, `Propose` would wait on `Run`, `Run` on the apply loop, and the apply loop on this lock.

**Linearizable writes.** A write is linearizable when, once the client has its answer, every later operation anywhere in the cluster sees it. Waiting for apply gives you that for writes: `Set` returns only after the entry is committed (a majority has it) and applied on this node, so the returned `store.Entry` with its `Version` is exactly what the log says.

**Leadership-change errors.** A handler is waiting on index 42. The leader loses an election; the new leader overwrites index 42 with its own entry. The waiter must not get that entry's result. Store the term alongside the index when registering; when the apply loop reaches 42 and the entry's term differs, fail the waiter with `ErrLeadershipLost`. Do the same for every waiter when `Propose` later reports the node is no longer leader.

## What to do

1. Create `raft.KV` with `NewKV(n *Node, st store.Store, b *watch.Broker) *KV` and the compile-time assertion that it is a `store.Store`.
2. Define the command encoding: an op byte per `store.Store` mutation, key, value, `IfVersion`, absolute `ExpiresAt`. Write `encode` and `decode` and a round-trip test for them.
3. Write the apply loop: `for e := range n.Committed()`, decode, call the store method, publish a `watch.Event` with `ID: e.Index` on success, wake the waiter.
4. Write the waiter map and the register-under-lock sequence; `Set`, `SetWithTTL`, `Delete`, `CompareAndSwap`, `CompareAndSwapWithTTL`, `CompareAndDelete` and `Touch` all go through it.
5. Return `ErrNotLeader` from writes on a follower (pass through what `Propose` says) and `ErrLeadershipLost` to waiters whose index was overwritten by a different term.
6. Serve `Get` and `Keys` from the local store for now.
7. Run `storetest.Run`, `RunCAS` and `RunTTL` against a 3-node in-memory cluster, with the leader's `KV` as the store under test.

## Example

A 3-node in-memory cluster, each node with its own `store.Memory` and `watch.Broker`:

```go
e, err := kv1.Set(ctx, "user:1", []byte("ann"))
fmt.Println(e.Version, err)
e, err = kv1.CompareAndSwap(ctx, "user:1", 1, []byte("anna"))
fmt.Println(e.Version, err)
```

```
1 <nil>
2 <nil>
```

Subscribe on node 2, resuming after ID 40, and the events that arrive are indexes 41, 42, 43... with the same entries node 1 applied:

```go
ch, stop, _ := b2.SubscribeFrom("user:", 40)
for ev := range ch {
	fmt.Println(ev.ID, ev.Type, ev.Entry.Key, ev.Entry.Version)
}
```

```
41 put user:1 1
42 put user:1 2
43 delete user:1 0
```

## Done when

- [ ] `storetest.Run`, `RunCAS` and `RunTTL` pass through a 3-node in-memory cluster
- [ ] A waiter whose entry is replaced by a different term gets `ErrLeadershipLost`, not a wrong result
- [ ] Watch event IDs equal log indexes on every node; a watcher resuming from ID 40 on another node sees exactly the events the leader published from 41 onward
- [ ] Writes on a follower return `ErrNotLeader`

## Reference

The exact contract the tests will hold you to.

```go
type KV struct { /* yours */ }
func NewKV(n *Node, st store.Store, b *watch.Broker) *KV
// Set proposes a command, waits for apply, returns the applied entry;
// same for SetWithTTL, Delete, CompareAndSwap, CompareAndSwapWithTTL,
// CompareAndDelete, Touch.
func (kv *KV) Set(ctx context.Context, key string,
	value []byte) (store.Entry, error)
// Reads (Get/Keys) are served from the local store.
// storetest.Run passes through Raft.
var _ store.Store = (*KV)(nil)

var ErrLeadershipLost = errors.New("raft: leadership lost")
```

What this level builds:

- Command encoding (reuse the durable change-record style)
- Apply loop with map[index]chan result
- Broker publish with Event.ID = index
- Leadership-change errors for waiters

Decisions the tests rely on:

- Every decision a command needs (CAS match, new version) is made in the apply loop, on every node; only `ExpiresAt` is computed by the leader and carried in the command.
- A watch event's `ID` is the log index of the entry that caused it, so IDs are identical on every node. An entry that changes nothing (a failed CAS, a delete of a missing key) publishes nothing.
- Writes return the `store.Entry` as applied, including `Version`.

## The tests check

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/10.3/files/`, matching the earlier chapters.

The suite will cover:

- Conformance through Raft
- Waiter gets an error when the term changes before commit
- Watch IDs equal indexes on every node; resume on another node has no gaps

## Try it

```bash
./burrow watch
go test -race -run 'Conformance|Resume' ./raft
```

## In the wild

etcd's mvcc apply path; the FSM in hashicorp/raft

## New in Go

apply loops · waiting on results by index · linearizable writes

Standard library: `context`, `sync`, `encoding/binary`
