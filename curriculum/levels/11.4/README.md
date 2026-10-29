# 11.4 · Leader Down

Final boss. Start the cluster behind the chaos proxy, run every probe, kill the leader mid-storm, partition a follower, heal, restart, check. Ten seeded runs must pass.

## Build

```go
// scenario -seed N -duration 60s: exit 0 when the checker reports no violations.
```

- The scenario runner
- Replayable seeds
- A README for Burrow

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/11.4/files/`, matching the earlier chapters.

## Pass when

10 consecutive seeded runs pass the checker with zero violations, using only the Go standard library.

The suite will cover:

- The scenario harness itself: a sabotaged build fails, the real build passes 10 seeds

## Try it

```bash
go run ./cmd/scenario -seed 1 -duration 60s && echo PASS
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

everything so far

Standard library: `everything so far`
