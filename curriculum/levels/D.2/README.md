# D.2 · Redundancy

> One copy is a prototype. Three copies is a product.

## Your goal

After D.1 each key lives on exactly one node. Lose that node and its keys are gone. Here you store every key on three nodes, the first three owners on the ring, and keep serving while one of them is down. A write is acknowledged once two of the three replicas have it; a read asks all three, waits for two answers, and returns the newest copy. Any two writers and any two readers overlap on at least one node, so a read always sees the latest acknowledged write. That overlap rule is called a *quorum*.

The result is a Dynamo-style store: a `Store` that satisfies the same interface as every other engine, built from the ring, your RPC layer and the quorum helper you already have.

## What you're using from before

**Hash ring (D.1 · Address Decoder).** `Owners(key, n)` returns `n` distinct nodes clockwise from the key's point. Here you ask for three, and those are the key's replicas:

```go
replicas := d.ring.Owners(key, 3)   // ["n2", "n3", "n1"]
```

**Quorum (9.3 · Quorum).** `quorum.Call[T]` runs N functions at once, returns the first K successes, cancels the rest and leaks nothing. You wrote it for Raft votes; here it does every write and every read. A write is `Call[ack]` with K=2 over three replica writes, a read is `Call[store.Entry]` with K=2 over three replica reads:

```go
fns := make([]func(ctx context.Context) (store.Entry, error), 0, 3)
for _, node := range replicas {
    conn := d.peers[node]
    fns = append(fns, func(ctx context.Context) (store.Entry, error) {
        return d.replicaGet(ctx, conn, key)
    })
}
results, err := quorum.Call(ctx, 2, fns) // 2 of 3, or ErrNoQuorum
```

**RPC (9.2 · Interconnect).** `rpc.Conn.Call(ctx, method, payload)` sends one request on a shared connection and returns the reply. Here each replica exposes two new methods, one that stores a copy and one that returns it, and `replicaGet` above is one `Call`:

```go
reply, err := conn.Call(ctx, MethodReplicaGet, []byte(key))
```

Use the Frame codec from 2.2 for the payloads: `Key`, `Value` and `Version` are already there, and `Version` can carry the timestamp.

## What's new here

**N, W and R.** N is how many copies of a key exist (3). W is how many replicas must acknowledge a write before you tell the caller it succeeded (2). R is how many replicas must answer a read before you pick a result (2). As long as W + R > N, the set of nodes that took the write and the set you read from share at least one node, so one of the R answers is the newest write. With one node down, two are still up, and 2 is enough for both W and R: the cluster keeps working. With two nodes down it correctly refuses.

**Fan-out/fan-in, reused.** You built `quorum.Call` for one purpose and now use it for two more. The shape is the same every time: a slice of closures, one per replica; `Call` fans them out into goroutines and fans the results back in; it returns as soon as K succeed, and the context it hands each closure is cancelled when `Call` returns, so the slow third replica stops on its own. The one new habit: build the closure list in a loop and capture the loop variable, which is safe since Go 1.22 because each iteration gets its own variable.

**Versioning by timestamp: last-write-wins.** Three copies can disagree. Which is right? The simplest answer stamps each write with the coordinator's wall-clock time and keeps the copy with the biggest stamp. On a read you compare the R answers and return the one with the highest timestamp. That is last-write-wins (LWW), and it is what Cassandra does by default.

It is also wrong when clocks disagree. If n1's clock is 2 seconds ahead of n2's, a write that n2 coordinated *after* one n1 coordinated can lose, because its stamp is smaller. Note that in a comment next to your comparison; the next level makes it fail on purpose. For now, LWW is the rule.

**Read repair.** A read that got two answers may find they differ, or find the third replica (if it answered late or not at all) is behind. After returning the winner to the caller, write the winner back to any replica that was stale, in the background. That is how a node that was down catches up without a separate sync job: every read of a key it missed heals it.

Read repair starts a goroutine after the caller has their answer. Give it a context of its own with a short timeout, not the caller's (which may be done), and make sure `Close` waits for in-flight repairs, or the no-leak check fails.

## What to do

1. Make a `dynamo` package with a `Store` that takes the ring, a map of node id to `*rpc.Conn`, and this node's local `store.Store`. It satisfies `store.Store`.
2. Add two RPC methods to the node's handler: replica set (store this key, value and timestamp locally) and replica get (return the local copy, or not found). When the replica is this node, call the local store directly instead of going over RPC.
3. `Set`: stamp the write with `time.Now()`, pick the three owners, fan out replica sets with `quorum.Call` and K=2. Return once two acknowledge. `ErrNoQuorum` becomes the caller's error.
4. `Get`: fan out replica gets with K=2. Of the answers, return the entry with the highest timestamp; not found only if every answer says not found.
5. After a `Get`, compare what came back; any replica that returned an older copy or nothing gets the winner written to it in a background goroutine with its own short context.
6. `Delete` and the rest of the interface: a delete is a write of a tombstone (an entry marked deleted, with a timestamp) so it can win against older values; keep it simple and document what you chose.
7. Make `Close` cancel and wait for background repairs.
8. Test with three nodes and a `FakeClock` or real time: stop one node, write and read, restart it, read the key through it and see the repaired copy.

## Example

```go
d := dynamo.New(ring, peers, local)     // this node's view

d.Set(ctx, "user:42", []byte("gerard")) // n2 and n3 ack, n1 is down: ok
e, _ := d.Get(ctx, "user:42")           // 2 of 3 answer: "gerard"

// n1 comes back with no copy of user:42
e, _ = d.Get(ctx, "user:42")           // n2 and n3 answer; still ok
// a moment later, read repair has written "gerard" to n1
```

With devcluster and one node killed:

```bash
$ kill -9 $(pgrep -f 'burrowd.*-id n1')
$ curl -X PUT localhost:8082/v1/kv/a -d 1   # W=2 of the remaining two
$ curl localhost:8083/v1/kv/a
1
```

## Done when

- [ ] With one of three nodes down, `Set` and `Get` still succeed
- [ ] With two nodes down, `Set` and `Get` fail with `quorum.ErrNoQuorum` instead of hanging
- [ ] A read returns the copy with the newest timestamp when replicas disagree
- [ ] After a node restarts, reading a key it missed brings its copy up to date
- [ ] Goroutine counts match before and after a run of writes, reads and repairs
- [ ] `gofmt -l .` and `go vet ./...` print nothing

## Reference

```go
// package dynamo (name is yours); Store satisfies store.Store
func (d *Store) Set(...) // fan out with quorum.Call[ack]
func (d *Store) Get(...) // fan out with quorum.Call[store.Entry]
```

- Replica selection
- W/R quorums
- LWW with timestamps
- Read repair

Decisions the suite will rely on:

- N is 3, W is 2 and R is 2.
- A write returns as soon as W replicas succeed; the rest are cancelled.
- Last-write-wins: the entry with the highest timestamp is the read result.

## The tests check

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/D.2/files/`, matching the earlier chapters.

The suite will cover:

- Availability with one node down
- Read repair
- No leaks

## Try it

```bash
./burrow test
go run ./cmd/devcluster &
kill -9 $(pgrep -f 'burrowd.*-id n1')
curl -X PUT localhost:8082/v1/kv/a -d 1; curl localhost:8083/v1/kv/a
```

## In the wild

Dynamo, Riak, Cassandra

## New in Go

fan-out/fan-in reuse · N/W/R quorums · last-write-wins versioning · read repair

Standard library: `context`, `sync`, `time`
