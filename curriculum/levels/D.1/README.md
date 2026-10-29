# D.1 · Address Decoder

Spread keys across nodes so each one owns a slice of the keyspace, and forward requests to the owner through your own client.

## Build

```go
type Ring struct{...}; func New(replicas int) *Ring; func (r *Ring) Add(node string); Remove(node string); Owners(key string, n int) []string
```

- Ring
- Forwarding
- Loop guard

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/D.1/files/`, matching the earlier chapters.

## Pass when

10,000 keys land within ±15% of even across 3 nodes; removing a node moves about a third; a GET to any node returns the right value.

The suite will cover:

- Distribution
- Minimal movement
- Forwarding through any node
- No forwarding loops

## Uses

- devcluster (built in 9.1 · Multi-core)
- HTTP client lib (built in 7.5 · Client Library)

## In the wild

Dynamo, Cassandra's token ring, Memcached's ketama

## New in Go

consistent hashing · slices.BinarySearch · proxying

Standard library: `hash/fnv`, `slices`, `net/http`
