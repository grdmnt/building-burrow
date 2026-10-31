# 11.1 · Fault Injector

> You cannot test a distributed system by hoping the network misbehaves. Build the thing that makes it misbehave on command.

## Your goal

Write `chaos`, a TCP proxy that sits between the nodes of your cluster. Every node talks to its peers through it, and a tiny JSON API lets a test (or you, with `curl`) cut a node off, add latency, drop connections, and put everything back. Every random decision comes from a seeded generator, so a run that found a bug can be replayed exactly with the same seed. This is the instrument chapter 11 uses to break Burrow on purpose.

## What you're using from before

**Accept loop (3.1 · Loopback).** `wire.ServeConns(ln, handle, logger)` accepts connections and runs `handle(conn)` in a goroutine each, returning nil when the listener closes. The proxy is one `ServeConns` per link; the handler dials the real destination and copies bytes both ways:

```go
err := wire.ServeConns(ln, func(conn net.Conn) {
	defer conn.Close()
	p.proxy(link, conn)
}, logger)
```

**devcluster (9.1 · Multi-core).** `devcluster` starts the nodes. For this level it gains a mode where each node listens on a port 100 above its usual one, while every `-peers` list still names the usual ports, which are now the proxy's. All peer traffic flows through `chaos`.

**JSON API (7.2 · JSON In and Out).** The admin API is four `POST`s and a `GET`, and it handles JSON the way 7.2 taught: `json.NewDecoder` with `DisallowUnknownFields` and a size limit on the way in, the `Content-Type` header, the status and `json.NewEncoder` on the way out, and errors shaped like `api.ErrorResponse`. `chaos` is its own small program, so it does this with `encoding/json` directly, through two tiny helpers of its own:

```go
func readJSON(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
```

With those, each handler is a few lines:

```go
mux.HandleFunc("POST /partition", func(w http.ResponseWriter,
	r *http.Request) {
	var req struct {
		Nodes []string `json:"nodes"`
	}
	if err := readJSON(w, r, &req); err != nil {
		writeJSON(w, 400, api.ErrorResponse{
			Code: "invalid_request", Error: err.Error()})
		return
	}
	p.partition(req.Nodes)
	writeJSON(w, 200, p.state())
})
```

## What's new here

**Bidirectional copying.** A proxy connection is two streams: client to server and server to client. `io.Copy(dst, src)` moves one stream until `src` hits EOF, and it blocks, so each direction gets its own goroutine:

```go
go func() {
	io.Copy(upstream, client)
	closeWrite(upstream) // half-close, explained next
}()
io.Copy(client, upstream)
closeWrite(client)
```

When both copies have returned, close both connections. The delay rule is applied inside the copy: instead of `io.Copy`, read a chunk, sleep, write the chunk. The drop rule is decided once per connection, right after accept.

**Half-close.** TCP lets one side say "I have nothing more to send" while still reading. That is `CloseWrite` on a `*net.TCPConn`, and it is what 3.1's echo test called half-close from the client side. A proxy has to pass it through: when the client finishes sending, the server must see EOF on its read, but still be able to send its reply back. If you `Close()` instead of `CloseWrite()` the reply is lost. `net.Conn` does not have `CloseWrite`, so assert the concrete type:

```go
func closeWrite(conn net.Conn) {
	if tc, ok := conn.(*net.TCPConn); ok {
		tc.CloseWrite()
	}
}
```

**Seeded randomness.** `math/rand/v2`'s package-level functions are seeded from the OS and cannot be replayed. For a replayable run make your own generator from a seed:

```go
r := rand.New(rand.NewPCG(seed, 0))
if r.Float64() < rule.dropRate { /* drop */ }
```

A `*rand.Rand` is not safe for concurrent use, so guard the one generator with a mutex; every draw in the proxy goes through it. With a fixed seed and the same sequence of connections, every decision is the same on every run, which is what makes the failing run from a chaos test reproducible.

**Rules per link.** A link is one proxied port with a node name. The proxy keeps the current rules (partitioned nodes, delay, drop rate) behind a mutex, and each copy goroutine reads them per chunk. Partition means: close existing connections on every link touching a named node, and refuse (accept then close) new ones in both directions. Heal clears everything.

## What to do

1. Create `cmd/chaos` with flags `-seed`, `-links` and `-admin`. Parse each link as `name:listen->target`.
2. For each link, listen and `ServeConns`. The handler dials the target, runs the two copy goroutines with half-close, and closes both ends when done.
3. Write the chunked copy with the delay applied per chunk: sleep `ms` plus a draw from `[0, jitter_ms)` from the seeded generator. Decide the drop once per new connection, right after accept: with probability `rate`, close it.
4. Keep the rule set behind a mutex, with a counter of connections per link; `GET /state` returns it all.
5. Write `readJSON` and `writeJSON` in `cmd/chaos` and serve the admin API on `-admin` with them: `POST /partition`, `/heal`, `/delay`, `/drop`, `GET /state`.
6. Give `devcluster` a `-via-chaos` mode (node `i` listens on `base-port+100+i`, `-peers` names `base-port+i`) and try the partition scenario by hand.

## Example

```bash
$ go run ./cmd/chaos -seed 42 -admin :9000 \
    -links n1:7001->7101,n2:7002->7102,n3:7003->7103 &
$ go run ./cmd/devcluster -via-chaos &
$ curl -s -X POST localhost:9000/partition -d '{"nodes":["n1"]}'
{"partitioned":["n1"],"delay_ms":0,"jitter_ms":0,"drop_rate":0,
 "links":{"n1":0,"n2":2,"n3":2}}
$ curl -s localhost:8082/v1/cluster | jq -r .leader
n2
$ curl -s -X POST localhost:9000/heal
{"partitioned":[],"delay_ms":0,"jitter_ms":0,"drop_rate":0,
 "links":{"n1":2,"n2":2,"n3":2}}
```

After the partition `n1` cannot reach anyone and nobody can reach it; `n2` and `n3` elect a leader between them. After `heal`, `n1` reconnects and follows.

## Done when

- [ ] `POST /partition` for a node blocks both directions and `POST /heal` restores them
- [ ] `POST /delay` adds latency within `ms` and `ms + jitter_ms` per chunk
- [ ] Two runs with the same seed and the same connection sequence make byte-identical decisions
- [ ] Half-close passes through: a client that calls `CloseWrite` still receives the server's reply
- [ ] A partitioned devcluster node is left behind while the other two elect a leader, and heal reunites them

## Reference

The exact contract the tests will hold you to.

```go
// POST /partition {"nodes":["n1"]}   POST /heal
// POST /delay {"ms":200,"jitter_ms":50}   POST /drop {"rate":0.1}
// GET /state
// chaos -seed 42 -admin :9000 -links n1:7001->7101,...
//   n1:7001->7101: listen on 7001, forward to 7101, call the link n1.
// devcluster -via-chaos: node i listens on base-port+100+i and every
// -peers list names base-port+i, the ports chaos listens on.
```

What this level builds:

- Bidirectional copy with half-close
- Rule evaluation per link
- JSON admin API with two small `encoding/json` helpers of its own
- Seeded rand

Decisions the tests rely on:

- A partition applies to every link touching a named node, in both directions: existing connections are closed and new ones are accepted then closed.
- Delay is per chunk: `ms` plus a seeded draw from `[0, jitter_ms)`.
- Drop is per new connection: with probability `rate` the connection is closed right after accept.
- Every random draw comes from one generator seeded by `-seed`.
- Every admin endpoint returns the same JSON `GET /state` does; bad JSON is 400 `invalid_request`.

## The tests check

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/11.1/files/`, matching the earlier chapters.

The suite will cover:

- Partition blocks both directions; heal restores
- Delay adds latency within bounds
- Seeded runs are byte-identical in their decisions

## Try it

```bash
./burrow watch
curl -X POST localhost:9000/partition -d '{"nodes":["n1"]}'
curl -X POST localhost:9000/heal
```

## In the wild

Toxiproxy, Jepsen's nemesis, tc netem

## New in Go

bidirectional copying · half-close · seeded randomness

Standard library: `net`, `io`, `math/rand/v2`, `net/http`, `encoding/json`
