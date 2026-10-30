# D.1 · Address Decoder

> A memory controller doesn't search every chip for an address. It decodes the address and goes straight to the chip that owns it.

## Your goal

Until now every node held every key. Here you split the keyspace: each node owns a slice of it, and a request that lands on the wrong node is forwarded to the owner. The piece that decides who owns what is a *hash ring*: hash the key to a number, and the node whose point on the ring comes next owns it. That gives you a cluster that holds more data than one machine can, which is what the D chapter is about.

Two things have to be true. Keys must spread evenly over the nodes, and adding or removing a node must move only the keys that have to move, not reshuffle everything. Plain `hash(key) % nodes` fails the second test badly: change the node count and almost every key gets a new owner.

## What you're using from before

**devcluster (9.1 · Multi-core).** Starts three `burrowd` nodes on one machine with `-id`, `-peers` and `-http`, and prints `cluster ready`. Here it starts the three nodes that share the ring. Each node needs to know every other node's HTTP address so it can forward; the `-peers` list already carries the ids, so either add the HTTP addresses there or add a flag, and have devcluster pass it:

```bash
go run ./cmd/devcluster         # n1, n2, n3; each builds the same ring
```

**HTTP client lib (7.5 · Client Library).** `burrow.Client` talks to a node's HTTP API with retries and deadlines. Here the node that isn't the owner uses it to forward the request to the node that is:

```go
owner := c.ring.Owners(key, 1)[0]
cl := c.clients[owner]        // one burrow.Client per peer
e, err := cl.Get(ctx, key)    // ask the owner for the caller
```

## What's new here

**Consistent hashing.** Picture the whole range of a 64-bit hash as a circle. Hash each node's name onto the circle; that point is the node. Hash a key onto the circle; walk clockwise from it, and the first node point you hit is the owner. Walking past the top wraps around to the bottom. Now add a node: it claims only the keys between the previous point and its own, and nothing else moves. Remove a node: only its keys move, to the next point clockwise. That is the whole trick.

**Virtual nodes.** Three points on a circle rarely split it into thirds. So put each node on the ring many times, say 100 points each from hashing `n1#0`, `n1#1`, ... `n1#99`. With 300 points the slices average out and the spread becomes even. The `replicas` argument to `New` is how many points each node gets.

**Hashing with `hash/fnv`.** FNV is a fast, non-cryptographic hash in the standard library. It implements `hash.Hash64`, so you write bytes in and read a number out:

```go
h := fnv.New64a()
h.Write([]byte("user:42"))
point := h.Sum64()                 // a uint64 somewhere on the ring
```

**Finding the owner with `slices.BinarySearch`.** Keep the ring as a sorted slice of points. `slices.BinarySearch(points, x)` returns the index of the first element at or after `x` and whether it was an exact match. If the index is `len(points)`, you walked off the end: wrap to index 0. To pair each point with its node name, keep a struct per point and use `slices.SortFunc` and `slices.BinarySearchFunc` with `cmp.Compare`:

```go
type point struct{ hash uint64; node string }
byHash := func(p point, h uint64) int { return cmp.Compare(p.hash, h) }
i, _ := slices.BinarySearchFunc(r.points, h, byHash)
if i == len(r.points) { i = 0 }
```

`Owners(key, n)` keeps walking clockwise, skipping points that belong to a node it has already collected, until it has `n` distinct nodes. With `n` 1 that is the owner; `n` exists because a key will soon live on more than one node.

**Proxying.** An HTTP handler that calls another server and relays the answer. The node receiving `PUT /v1/kv/{key}` checks the ring; if the owner is someone else, it calls the owner with `burrow.Client` and writes the result as its own response, including the status and `ETag`. The caller never learns it talked to the wrong node.

**The loop guard.** If two nodes disagree about who owns a key (one has a stale ring), A forwards to B, B forwards back to A, forever. Mark a forwarded request with a header, `X-Burrow-Forwarded: <node id>`. A node that receives a request with that header already set handles it locally if it can, and otherwise answers an error instead of forwarding again. One hop, never two.

## What to do

1. Make a `ring` package with `New`, `Add`, `Remove` and `Owners` as in the Reference.
   - `Add` hashes `replicas` points for the node and inserts them, keeping the slice sorted.
   - `Remove` drops that node's points.
   - `Owners(key, n)` returns `n` distinct nodes clockwise from `hash(key)`, fewer if the ring has fewer nodes, nil for an empty ring.
2. Give each node a ring built from the peer list at startup, and one `burrow.Client` per peer.
3. In the HTTP API, before each key operation (`GET`, `PUT`, `DELETE`, touch), look up the owner. If it is this node, serve from the local store as before. If not, forward with the client and relay the reply.
4. Set `X-Burrow-Forwarded` on every forwarded request. On receiving a request that already has it, never forward again.
5. Teach devcluster to pass each node the HTTP addresses of its peers.
6. Write a test that puts 10,000 keys through `Owners` on a three-node ring and counts them per node, and one that removes a node and counts how many keys changed owner.

## Example

```go
r := ring.New(100)
r.Add("n1"); r.Add("n2"); r.Add("n3")
r.Owners("user:42", 1)      // ["n2"]
r.Owners("user:42", 3)      // ["n2", "n3", "n1"], clockwise, no repeats

counts := map[string]int{}
for i := range 10000 {
    counts[r.Owners(fmt.Sprintf("k%d", i), 1)[0]]++
}
// counts ≈ {"n1": 3310, "n2": 3402, "n3": 3288}
// each within 15% of 3333

r.Remove("n2")
r.Owners("user:42", 1)      // ["n3"]: only n2's keys moved
```

And over the wire, with devcluster running on ports 8081 to 8083:

```bash
$ curl -X PUT localhost:8081/v1/kv/user:42 -d 'gerard'   # n1 forwards
$ curl localhost:8083/v1/kv/user:42                       # n3 forwards
gerard
```

## Done when

- [ ] 10,000 keys land within 15% of an even split across three nodes
- [ ] Removing one of three nodes moves about a third of the keys and no more
- [ ] A `GET` sent to any node returns the right value, whichever node owns the key
- [ ] A request never bounces between two nodes: one forward at most
- [ ] `gofmt -l .` and `go vet ./...` print nothing

## Reference

```go
package ring

type Ring struct{ /* yours */ }

func New(replicas int) *Ring                       // points per node
func (r *Ring) Add(node string)
func (r *Ring) Remove(node string)
func (r *Ring) Owners(key string, n int) []string  // n distinct nodes
```

- Ring
- Forwarding
- Loop guard

## The tests check

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/D.1/files/`, matching the earlier chapters.

The suite will cover:

- Distribution
- Minimal movement
- Forwarding through any node
- No forwarding loops

## Try it

```bash
./burrow test
go test ./ring -run Distribution -v
go run ./cmd/devcluster &
curl -X PUT localhost:8081/v1/kv/a -d 1; curl localhost:8083/v1/kv/a
```

## In the wild

Dynamo, Cassandra's token ring, Memcached's ketama

## New in Go

consistent hashing · virtual nodes · hash/fnv · slices.BinarySearch · proxying

Standard library: `hash/fnv`, `slices`, `cmp`, `net/http`
