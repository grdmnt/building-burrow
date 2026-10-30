# 10.4 · Follow the Leader

> Clients don't care who the leader is. They want to send a write to any node and have it land.

## Your goal

Put the cluster behind the HTTP API. A `GET /v1/cluster` endpoint tells you what a node knows (its term, role, leader, how far its log has got). A write sent to a follower answers with a redirect to the leader instead of failing. And `burrow.Client` learns to follow that redirect, remember where the leader is, and wait out an election when nobody is leader yet. After this level `burrowctl -http` works against any node of a running cluster.

## What you're using from before

**Raft KV (10.3 · Apply Loop).** `raft.KV` is a `store.Store`, so `api.Server{Store: kv}` works unchanged for the happy path. What is new is two of its errors: `ErrNotLeader` (with the leader's ID) and the case where there is no leader yet. The handlers map them to HTTP:

```go
var nl *raft.ErrNotLeader
if errors.As(err, &nl) {
	if nl.Leader == "" {
		writeJSON(w, 503, ErrorResponse{Code: "leader_unknown", ...})
		return
	}
	w.Header().Set("X-Burrow-Leader", nl.Leader)
	http.Redirect(w, r, leaderURL(nl.Leader)+r.URL.RequestURI(),
		http.StatusTemporaryRedirect)
	return
}
```

`Node.Status()` gives the cluster endpoint its numbers.

**JSON API (7.2 · JSON In and Out).** `writeJSON(w, status, v)` and `ErrorResponse` are the shape of every response. `/v1/cluster` is one more `writeJSON` of a typed struct, and `leader_unknown` is one more `Code`.

**HTTP client lib (7.5 · Client Library).** `burrow.Client` already builds requests with `http.NewRequestWithContext`, decodes `ErrorResponse`, and retries with `Backoff`. This level teaches it two more things: a redirect policy on its `http.Client`, and that a 503 with code `leader_unknown` is retryable while a 412 never is.

**devcluster (9.1 · Multi-core).** `go run ./cmd/devcluster` starts three nodes with `-peers` set. With this level those nodes form a Raft cluster, and the failover test runs against it: write, kill the leader's process, write again.

## What's new here

**http.Client redirect policy.** By default `http.Client` follows up to 10 redirects on its own. For a 307 or 308 it re-sends the body, but only if it can get the body again: `http.NewRequest` sets `req.GetBody` when the body is a `*bytes.Reader`, `*bytes.Buffer` or `*strings.Reader`, and leaves it nil for anything else, in which case the redirect is followed with no body. So build request bodies from `bytes.NewReader`. The `CheckRedirect` hook decides whether to follow each hop:

```go
client := &http.Client{
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return errors.New("burrow: too many redirects")
		}
		return nil
	},
}
```

`via` is the list of requests so far, so `len(via)` is the hop count. Returning `http.ErrUseLastResponse` instead stops following and hands you the 307 itself, which is useful when you want to read `Location` and `X-Burrow-Leader` yourself and update the leader cache.

**A leader cache.** Every response from the cluster may carry `X-Burrow-Leader`. The client keeps the leader's base URL and sends the next write straight there, so only the first write after a leadership change pays for a redirect. Guard the cached value with a mutex; the client is used from many goroutines.

**JSON status types.** `/v1/cluster` returns a struct the client decodes into a typed value, not a `map[string]any`. Field names on the wire are `snake_case` through struct tags:

```go
type ClusterStatus struct {
	ID           string   `json:"id"`
	Term         uint64   `json:"term"`
	Role         string   `json:"role"`
	Leader       string   `json:"leader"`
	CommitIndex  uint64   `json:"commit_index"`
	AppliedIndex uint64   `json:"applied_index"`
	Peers        []string `json:"peers"`
}
```

**Retry on specific codes.** 7.5's rule was: retry only what cannot double-apply. A 503 `leader_unknown` means the node did not propose anything, so retrying a write is safe. The client checks the status *and* the code, then waits `Backoff(attempt)` and tries again, up to `MaxRetries`. Any other 5xx on a write stays unretried.

## What to do

1. Add `GET /v1/cluster` to `api.Server`, built from `Node.Status()` plus the KV's applied index, and give `api.Server` a way to reach the node (a field or a small interface).
2. Give the server a map from peer ID to HTTP base URL (`-peer-http`), so it can build a `Location` for the leader.
3. On every write handler, map `ErrNotLeader` with a known leader to 307 + `Location` + `X-Burrow-Leader`, and with no leader to 503 `leader_unknown`. Add `X-Burrow-Leader` on successful writes too.
4. In `burrow.Client`, set `CheckRedirect` to allow at most 3 hops, build bodies with `bytes.NewReader`, cache the leader from `X-Burrow-Leader` on every response, and send writes to the cached leader when there is one.
5. Treat 503 `leader_unknown` as retryable with backoff; keep 412 and other errors as they were.
6. In `burrowd serve`, when `-peers` is set, build the `raft.Node`, `raft.KV`, and the RPC server for Raft, and serve the API from the KV instead of the plain store.
7. Test failover with devcluster: write through a follower, kill the leader's process, write again.

## Example

```bash
$ go run ./cmd/devcluster &
$ curl -s localhost:8082/v1/cluster | jq -c .
{"id":"n2","term":1,"role":"follower","leader":"n1","commit_index":3,
 "applied_index":3,"peers":["n1","n2","n3"]}
$ curl -si -X PUT localhost:8082/v1/kv/a -d 'hello' | head -3
HTTP/1.1 307 Temporary Redirect
Location: http://127.0.0.1:8081/v1/kv/a
X-Burrow-Leader: n1
$ go run ./cmd/burrowctl -http http://127.0.0.1:8082 set a hello
OK 1
```

`burrowctl` pointed at a follower still succeeds: the client followed the redirect. Kill `n1` and run the `set` again; the first attempt may get a 503 `leader_unknown` during the election, the retry lands on the new leader within a few seconds.

## Done when

- [ ] A write on a follower answers 307 with `Location` on the leader and `X-Burrow-Leader`
- [ ] `burrow.Client` pointed at a follower URL writes successfully
- [ ] With devcluster, killing the leader and writing again succeeds within 3 seconds through the new leader
- [ ] `/v1/cluster` decodes into the typed struct and agrees on every node

## Reference

The exact contract the tests will hold you to.

```go
// GET /v1/cluster -> {"id","term","role","leader","commit_index",
//                     "applied_index","peers":[...]}
// Writes on a follower: 307 Temporary Redirect, Location on the leader,
// X-Burrow-Leader: n2; 503 leader_unknown during elections.
// burrow.Client: follows at most 3 redirects, caches the leader URL,
// retries on 503 leader_unknown with backoff.
// burrowd serve gains -peer-http n1=http://127.0.0.1:8081,n2=...: the
// HTTP base URL of each peer, used to build Location.
```

What this level builds:

- Cluster status endpoint
- Redirect on followers
- Client leader cache and retry
- burrowd serve uses raft.KV when -peers is set

Decisions the tests rely on:

- `role` is `leader`, `follower` or `candidate`; `leader` is `""` when unknown; `peers` lists every node ID including this one.
- A redirect keeps the method, path, query and body (307, never 302).
- `503 leader_unknown` is retryable for every operation; `412 version_mismatch` is never retried.
- `X-Burrow-Leader` is set on every successful write and every redirect.

## The tests check

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/10.4/files/`, matching the earlier chapters.

The suite will cover:

- Redirect and header on a follower
- Client writes through a follower URL
- Leader failover with devcluster: write succeeds within 3 s
- Status JSON decodes into a typed struct

## Try it

```bash
./burrow watch
go run ./cmd/devcluster
curl -s localhost:8082/v1/cluster | jq
go run ./cmd/burrowctl -http http://127.0.0.1:8082 set a hello
```

## In the wild

etcd gateway and clientv3 endpoint switching; Redis Cluster MOVED

## New in Go

http.Client redirect policy · JSON status types · retry on specific codes

Standard library: `net/http`, `encoding/json`
