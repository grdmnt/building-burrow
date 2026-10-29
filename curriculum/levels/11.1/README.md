# 11.1 · Fault Injector

A TCP proxy between nodes that can delay, drop and partition, controlled by a small JSON API and seeded so a failing run replays exactly.

## Build

```go
// POST /partition {"nodes":["n1"]}  POST /heal  POST /delay {"ms":200,"jitter_ms":50}  POST /drop {"rate":0.1}  GET /state
// chaos -seed 42 -links n1:7001->7101,...
```

- Bidirectional copy with half-close
- Rule evaluation per link
- JSON admin API using the 7.2 helpers
- Seeded rand

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/11.1/files/`, matching the earlier chapters.

## Pass when

curl -X POST :9000/partition isolates n1, the other two elect a leader, and heal reunites the cluster.

The suite will cover:

- Partition blocks both directions; heal restores
- Delay adds latency within bounds
- Seeded runs are byte-identical in their decisions

## Try it

```bash
curl -X POST ':9000/partition' -d '{"nodes":["n1"]}'
curl -X POST ':9000/heal'
```

## Uses

- Accept loop (built in 3.1 · Loopback)
- devcluster (built in 9.1 · Multi-core)
- JSON API (built in 7.2 · JSON In and Out)

## In the wild

Toxiproxy, Jepsen's nemesis, tc netem

## New in Go

bidirectional copying · half-close · seeded randomness

Standard library: `net`, `io`, `math/rand/v2`, `net/http`
