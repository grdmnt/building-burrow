# 10.4 · Follow the Leader

Expose cluster state over the API, redirect writes from followers, and teach the client to find and follow the leader.

## Build

```go
// GET /v1/cluster -> {"id","term","role","leader","commit_index","applied_index","peers":[...]}
// Writes on a follower: 307 Temporary Redirect, Location on the leader, X-Burrow-Leader: n2; 503 leader_unknown during elections.
// burrow.Client: follows at most 3 redirects, caches the leader URL, retries on 503 leader_unknown with backoff.
```

- Cluster status endpoint
- Redirect on followers
- Client leader cache and retry
- burrowd serve uses raft.KV when -peers is set

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/10.4/files/`, matching the earlier chapters.

## Pass when

burrowctl -http against any node writes successfully; kill the leader and the next write succeeds within 3 s through the new leader; /v1/cluster agrees on every node.

The suite will cover:

- Redirect and header on a follower
- Client writes through a follower URL
- Leader failover with devcluster: write succeeds within 3 s
- Status JSON decodes into a typed struct

## Uses

- Raft KV (built in 10.3 · Apply Loop)
- JSON API (built in 7.2 · JSON In and Out)
- HTTP client lib (built in 7.5 · Client Library)
- devcluster (built in 9.1 · Multi-core)

## In the wild

etcd gateway and clientv3 endpoint switching; Redis Cluster MOVED

## New in Go

http.Client redirect policy · JSON status types · retry on specific codes

Standard library: `net/http`, `encoding/json`
