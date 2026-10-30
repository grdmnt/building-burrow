# 11.4 · Leader Down

Final boss. Start the cluster behind the chaos proxy, run every probe, kill the leader mid-storm, partition a follower, heal, restart, check. Ten seeded runs must pass.

## Build

```go
// scenario -seed N -duration 60s: exit 0 when the checker reports no violations.

// cmd/burrowd/main.go: the version string becomes a variable the linker can set.
var version = "0.1.0" // go build -ldflags "-X main.version=1.0.0-$(git rev-parse --short HEAD)" ./cmd/burrowd
// `burrowd version` still prints `burrowd 0.1.0` from a plain go build, so 0.1's tests keep passing.
```

- The scenario runner
- Replayable seeds
- A release build: `-ldflags "-X main.version=…"` stamping the version, `-trimpath`, and cross-compiling with `GOOS`/`GOARCH` for a machine you don't have
- A README for Burrow

Shipping it, which is the part of Go people underrate: `-X importpath.name=value` sets a string variable at link time, so a version, a commit hash or a build date goes into the binary without a generated file. `GOOS=linux GOARCH=arm64 go build ./cmd/burrowd` cross-compiles from any machine with no toolchain to install; `go tool dist list` prints every pair. The race detector and anything using cgo need a real target toolchain, pure-Go code does not, which is one reason the standard-library-only rule exists. `-trimpath` strips your directory layout from the binary, `-ldflags "-s -w"` drops the symbol table and DWARF for a smaller file (and no useful stack traces in `pprof`, so not for the one you run under load).

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/11.4/files/`, matching the earlier chapters.

## Pass when

10 consecutive seeded runs pass the checker with zero violations, using only the Go standard library.

The suite will cover:

- The scenario harness itself: a sabotaged build fails, the real build passes 10 seeds
- `go build -ldflags "-X main.version=9.9.9"` makes `burrowd version` print `burrowd 9.9.9`; a plain build still prints 0.1.0
- `GOOS=linux GOARCH=arm64 go build ./cmd/burrowd` and `GOOS=windows GOARCH=amd64` both succeed from the test's host, and `go version -m` on the result shows the module path and `-ldflags` setting

## Try it

```bash
go run ./cmd/scenario -seed 1 -duration 60s && echo PASS
go build -trimpath -ldflags "-X main.version=1.0.0-$(git rev-parse --short HEAD)" -o burrowd ./cmd/burrowd && ./burrowd version
GOOS=linux GOARCH=arm64 go build -o burrowd-linux-arm64 ./cmd/burrowd && file burrowd-linux-arm64
go version -m burrowd-linux-arm64   # module, dependencies (none), build settings
```

## Uses

- Chaos proxy (built in 11.1 · Fault Injector)
- Checker (built in 11.3 · Checker)
- devcluster (built in 9.1 · Multi-core)
- Durable Raft (built in 10.6 · Non-volatile Raft)
- Linearizable reads (built in 10.5 · Read Index)
- Leader-aware API (built in 10.4 · Follow the Leader)
- Admin port (built in 7.6 · Gauges)
- Deadline guard (built in 7.4 · Watchdog)
- Durable daemon (built in 5.1 · Non-volatile Daemon)

## In the wild

Jepsen; FoundationDB's simulation; TigerBeetle's VOPR

## New in Go

everything so far · -ldflags -X · GOOS and GOARCH cross-compilation · -trimpath · go version -m

Standard library: `everything so far`
