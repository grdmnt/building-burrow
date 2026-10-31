# 11.4 · Leader Down

> Final boss. Everything you built, running at once, while you pull the plug on the leader.

## Your goal

Write `scenario`, one program that assembles the whole machine and attacks it: start the cluster behind the chaos proxy, run every probe from the history recorder, and in the middle of the write storm kill the leader, partition a follower, heal it, restart a node. When the clock runs out, feed the history to the checker. Exit 0 only if it found nothing. Ten runs with ten different seeds must all pass, and any run that fails must be replayable from its seed.

Then ship it: a release build with the version stamped in by the linker, built for a machine you do not own, and a README that tells someone else what Burrow is.

## What you're using from before

**Chaos proxy (11.1 · Fault Injector).** The scenario starts `chaos` with its seed and talks to the admin API with a plain `http.Client` that has a short `Timeout`, so a wedged proxy fails the run instead of hanging it. `post` is a three-line helper around it:

```go
hc := &http.Client{Timeout: 2 * time.Second}
post := func(url, body string) (*http.Response, error) {
	return hc.Post(url, "application/json", strings.NewReader(body))
}
post(adminURL+"/partition", `{"nodes":["n3"]}`)
time.Sleep(5 * time.Second)
post(adminURL+"/heal", ``)
```

**Checker (11.3 · Checker).** `checker.Check(ops)` on the history the run produced is the verdict. The scenario prints each violation and sets its exit code from the count.

**devcluster (9.1 · Multi-core).** The node launcher: start `burrowd` processes, prefix their output, wait for `/readyz`. The scenario needs to kill and restart individual nodes, so if that code still lives in `cmd/devcluster/main.go`, move it into a package both commands import, with `Kill(id)` and `Restart(id)`.

**Durable Raft (10.6), Linearizable reads (10.5), Leader-aware API (10.4).** These are the promises under test. The cluster runs with `-data` so a restarted node comes back with its log; reads are linearizable by default so the monotonic-read rule has teeth; the client follows the leader so probes keep running through failover.

**Admin port (7.6 · Gauges), Deadline guard (7.4 · Watchdog), Durable daemon (5.1 · Non-volatile Daemon).** `/readyz` tells the scenario when a restarted node has finished replay. Per-request deadlines keep a probe from hanging on a dead node. And `burrowd serve -data` is the binary under test, with the `version` command from 0.1 that this level finally makes real.

## What's new here

**The scenario as a script.** The runner is a straight-line program with a timeline: start proxy, start cluster, start probes, then at fixed offsets from the seed's random choices, do something nasty, and at the end stop everything in reverse order. `exec.CommandContext` from 9.1 and one `context.WithTimeout` for the whole run keep it from leaking processes when something goes wrong.

**Replayable seeds.** One `-seed` feeds three generators: the chaos proxy's, the history recorder's, and the scenario's own (which node to partition, when to kill). Same seed, same attack at the same moments against the same workload. When a run fails, rerun it with the printed seed and read the RPC logs you added in 10.2.

**-ldflags -X.** `go build -ldflags "-X importpath.name=value"` sets a package-level string variable at link time, no generated file needed. The variable must be a plain `var` of type `string` (not a `const`, not computed). Turn 0.1's version into one:

```go
var version = "0.1.0"
```

```bash
go build \
    -ldflags "-X main.version=1.0.0-$(git rev-parse --short HEAD)" \
    ./cmd/burrowd
```

A plain `go build` still prints `burrowd 0.1.0`, so 0.1's tests keep passing.

**GOOS and GOARCH.** Go cross-compiles by setting two environment variables; no extra toolchain to install:

```bash
GOOS=linux GOARCH=arm64 go build -o burrowd-linux-arm64 ./cmd/burrowd
go tool dist list   # every supported pair
```

Pure-Go code just works. Anything using cgo, and the race detector, needs a real toolchain for the target; that is one reason the standard-library-only rule exists.

**-trimpath and -s -w.** `-trimpath` strips your directory layout from the binary, so paths in panics and `go version -m` output are `burrow/cmd/burrowd/main.go`, not `/home/you/...`. `-ldflags "-s -w"` drops the symbol table and DWARF debug info for a smaller file. Do not use `-s -w` on the build you profile under load: `pprof` needs the symbols.

**go version -m.** Every Go binary carries its build information: module path, dependencies (none here), and the build settings including the `-ldflags` and `GOOS`/`GOARCH` it was built with. `go version -m burrowd` prints it, and it works on a binary for another platform.

**A README.** You have built a replicated key-value store, a client library, and the instrument that proves it works. Write the page that says so: what it is, how to run a cluster, how to use the client and `burrowctl`, and what guarantees the checker verifies.

## What to do

1. Create `cmd/scenario` with `-seed` and `-duration`. From the seed derive the proxy's seed, the recorder's seed, and a `*rand.Rand` for the scenario's own choices.
2. Start `chaos`, then the cluster behind it with `-data` in a temp dir, and wait for every `/readyz`.
3. Start the probes in-process (import the probe functions from 11.2) against the node URLs.
4. On the timeline: find the leader from `/v1/cluster`, kill it mid-storm; partition a random follower, heal it after a few seconds; restart a node and wait for `/readyz`. Keep every probe running throughout.
5. At `-duration`, stop the probes, flush the history, stop the cluster and the proxy, run `checker.Check`, print violations, exit 1 if any.
6. Change `cmd/burrowd`'s version to a `var` and confirm both builds print the right thing.
7. Build for Linux arm64 and Windows amd64 and read `go version -m` on the results.
8. Write the README.

## Example

```bash
$ for s in $(seq 1 10); do
    go run ./cmd/scenario -seed $s -duration 60s || echo "FAIL seed $s"
  done
seed 1: 61,204 ops, leader n2 killed at 18.3s,
        n1 partitioned 31.0s-36.0s, n3 restarted at 44.7s: 0 violations
...
seed 10: 59,871 ops, ...: 0 violations
$ go build -trimpath -ldflags "-X main.version=1.0.0-$(git rev-parse \
    --short HEAD)" -o burrowd ./cmd/burrowd && ./burrowd version
burrowd 1.0.0-3f9a2c1
$ GOOS=linux GOARCH=arm64 go build -ldflags "-X main.version=1.0.0" \
    -o burrowd-linux-arm64 ./cmd/burrowd
$ go version -m burrowd-linux-arm64 | head -6
burrowd-linux-arm64: go1.24.0
        path    burrow/cmd/burrowd
        mod     burrow  (devel)
        build   -ldflags="-X main.version=1.0.0"
        build   GOARCH=arm64
        build   GOOS=linux
```

## Done when

- [ ] 10 consecutive seeded runs pass the checker with zero violations
- [ ] A sabotaged build (11.3's tags) fails the scenario, so the harness itself is proven
- [ ] `go build -ldflags "-X main.version=9.9.9"` makes `burrowd version` print `burrowd 9.9.9`; a plain build still prints `burrowd 0.1.0`
- [ ] `GOOS=linux GOARCH=arm64` and `GOOS=windows GOARCH=amd64` builds succeed, and `go version -m` on them shows the module path and the `-ldflags` setting
- [ ] A README for Burrow exists
- [ ] Still standard library only

## Reference

The exact contract the tests will hold you to.

```go
// scenario -seed N -duration 60s: exit 0 when the checker reports no
// violations.

// cmd/burrowd/main.go: the version string becomes a variable the linker
// can set.
var version = "0.1.0"
// go build \
//   -ldflags "-X main.version=1.0.0-$(git rev-parse --short HEAD)" \
//   ./cmd/burrowd
// `burrowd version` still prints `burrowd 0.1.0` from a plain go build,
// so 0.1's tests keep passing.
```

What this level builds:

- The scenario runner
- Replayable seeds
- A release build: `-ldflags "-X main.version=…"` stamping the version, `-trimpath`, and cross-compiling with `GOOS`/`GOARCH` for a machine you don't have
- A README for Burrow

Decisions the tests rely on:

- The scenario's own random choices, the proxy's and the recorder's all derive from `-seed`, so one number replays a run.
- `scenario` prints the seed and every fault it injected with its time offset, then the violation count.
- `version` is a package-level `var` of type `string` in `package main`, named `version`.

## The tests check

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/11.4/files/`, matching the earlier chapters.

The suite will cover:

- The scenario harness itself: a sabotaged build fails, the real build passes 10 seeds
- `go build -ldflags "-X main.version=9.9.9"` makes `burrowd version` print `burrowd 9.9.9`; a plain build still prints 0.1.0
- `GOOS=linux GOARCH=arm64 go build ./cmd/burrowd` and `GOOS=windows GOARCH=amd64` both succeed from the test's host, and `go version -m` on the result shows the module path and `-ldflags` setting

## Try it

```bash
./burrow watch
go run ./cmd/scenario -seed 1 -duration 60s && echo PASS
go build -trimpath -ldflags "-X main.version=1.0.0-$(git rev-parse \
    --short HEAD)" -o burrowd ./cmd/burrowd && ./burrowd version
GOOS=linux GOARCH=arm64 go build -o burrowd-linux-arm64 ./cmd/burrowd
file burrowd-linux-arm64
go version -m burrowd-linux-arm64   # module, deps, build settings
```

## In the wild

Jepsen; FoundationDB's simulation; TigerBeetle's VOPR

## New in Go

-ldflags -X · GOOS and GOARCH cross-compilation · -trimpath and -s -w · go version -m

Standard library: nothing new; `os/exec`, `context` and `math/rand/v2` from earlier levels do the work
