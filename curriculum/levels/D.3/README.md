# D.3 · The Other Road

> You built two roads to the same place. Drive the second one into the same wall, and read the skid marks.

## Your goal

Chapter 11 proved the Raft cluster keeps its promises: under partitions, delays and a dead leader, the checker found no violations. Here you run the same scenario against the Dynamo-style store from D.2 and watch it fail. Nothing new gets built; the work is a backend switch, a run, and a write-up that explains the failures in terms of what the quorum rule does and doesn't guarantee.

This is the level where *consistency models* stop being vocabulary. You will see a lost write and a lock held by two clients at once, named by operation, and be able to say exactly why.

## What you're using from before

**Quorum replication (D.2 · Redundancy).** The three-replica, W=2, R=2, last-write-wins store. Here it is the backend under test. The scenario runner starts three nodes serving the HTTP API from a `dynamo.Store` instead of the Raft state machine:

```go
// in the node's startup, chosen by a flag
var engine store.Store
switch backend {
case "raft":   engine = raftStore
case "dynamo": engine = dynamo.New(ring, peers, local)
}
api := api.Server{Store: engine}
```

**Checker (11.3 · Checker).** `Check(ops) []Violation` reads a recorded history and returns every broken rule with the first offending op. Here it runs unchanged over the Dynamo history; the difference is that this time it returns something:

```go
for _, v := range checker.Check(ops) {
    fmt.Printf("%s: client %d %s %s: %s\n",
        v.Rule, v.Op.Client, v.Op.Kind, v.Op.Key, v.Detail)
}
```

**Chaos proxy (11.1 · Fault Injector).** The TCP proxy between nodes with `POST /partition`, `POST /heal` and `POST /delay`. Here the scenario uses it to cut one replica off while clients keep writing on both sides:

```bash
curl -X POST ':9000/partition' -d '{"nodes":["n1"]}'
```

**Burrow v1 (11.4 · Leader Down).** `cmd/scenario`, the runner that starts the proxy and the cluster, runs every probe, kills, partitions and heals on a timeline from one `-seed`, and hands the history to the checker. It is the experiment, unchanged except for one new flag; ten clean seeded runs against Raft are the baseline the Dynamo runs are compared with:

```bash
go run ./cmd/scenario -seed 7                    # raft: no violations
go run ./cmd/scenario -seed 7 -backend dynamo    # same attack
```

## What's new here

**Consistency models.** A consistency model is the promise a store makes about what a read can return. Two matter here.

*Linearizable*: every operation appears to take effect at one instant between its start and its end, and all clients agree on the order. If your write finished at 10:00:01 and my read started at 10:00:02, I see your write. This is what Raft gives, and what the checker's rules test.

*Eventually consistent*: replicas converge once writes stop, but in the meantime a read may return an older value. Dynamo's W + R > N rule narrows the window, and in a healthy cluster it looks linearizable. Under partition and clock skew the gaps show.

**Why LWW loses writes.** Two coordinators, n1 and n3, each accept a write to the same key at nearly the same time. n1's clock runs 2 seconds ahead. n3's write happened second in real time but carries the smaller timestamp, so when the replicas compare, n1's value wins and n3's acknowledged write disappears. The checker's rule for this names the op: a read that returned a value older than a write that had already completed.

**Why a quorum store can't make a lock.** The lease lock from chapter 8 relies on `CompareAndSwap` with `ifVersion` 0 being atomic: exactly one of two concurrent creators wins. In the Dynamo store a CAS is a read followed by a write, each a separate quorum round. Two clients both read "absent", both write their lock, both get W=2 acknowledgements, and both believe they hold it. Fencing tokens need a single, agreed version counter, and a last-write-wins store has none. This is the paragraph in every Dynamo paper about why the metadata still lives in Paxos or Raft.

**Reading a failure.** The checker reports the first offending op per rule. Walk backwards from it in the history: which writes completed before it started, which replica each went through, what the proxy was doing at that moment. The write-up is that walk, in a page.

## What to do

1. Add `-backend raft|dynamo` to `cmd/scenario`, default `raft`. For `dynamo`, start the nodes with the D.2 store behind the HTTP API. Nothing else in the runner changes.
2. Add a partition step to the scenario if it doesn't already have one: isolate one replica with the chaos proxy for a few seconds while every probe keeps running, then heal.
3. Run with the dynamo backend a few seeds. Collect the violations and keep the history files.
4. Make the lock rule fail first: with a partition and the lock probe running, two holders at once should appear. Then look for lost writes from the kv probe; add a few hundred milliseconds of skew to one node's clock (a flag on `burrowd` that offsets `time.Now` for timestamps) if a clean clock hides it.
5. Confirm the raft backend still passes the same seeds with zero violations.
6. Write `docs/consistency.md`: what failed, the op the checker named, the sequence of events behind it, and why W + R > N did not prevent it (clock skew for the lost write, non-atomic read-then-write for the lock). End with a table of what each backend guarantees.

## Example

```bash
$ go run ./cmd/scenario -backend raft -seed 7 -duration 30s
checker: 0 violations
$ go run ./cmd/scenario -backend dynamo -seed 7 -duration 30s
lock-exclusivity: client 4 acquire lock:a: client 2 still holds it
lost-write: client 1 get k17 = "v3": client 6 wrote "v5" 212ms earlier
checker: 2 violations
exit status 1
```

The exact wording is yours; the rule name and the op are what matter.

## Done when

- [ ] `scenario -backend dynamo` under partition reports at least one violation, a lost write or a lock held twice, and names the op
- [ ] `scenario -backend raft` on the same seeds reports none
- [ ] `docs/consistency.md` explains each failure using W + R > N and clock skew
- [ ] `gofmt -l .` and `go vet ./...` print nothing

## Reference

```go
// scenario -backend dynamo
```

- Backend switch in the scenario runner
- Run, collect violations
- Write the comparison

## The tests check

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/D.3/files/`, matching the earlier chapters.

The suite will cover:

- The scenario reports at least one violation under partition for the dynamo backend and none for Raft

## Try it

```bash
./burrow test
go run ./cmd/scenario -backend dynamo -seed 7 -duration 30s
go run ./cmd/scenario -backend raft -seed 7 -duration 30s
```

## In the wild

Jepsen's analyses of Riak and Cassandra

## New in Go

consistency models

Standard library: `everything so far`
