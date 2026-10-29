# 6.3 · Heartbeat

Every node knows which nodes are alive.

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/6.3/files/`, matching the earlier chapters.

## Build

- A membership list with per-peer state: alive, suspect, dead
- Periodic pings over your RPC
- Suspect after missed pings, dead after a timeout
- `GET /v1/cluster` shows the view

## Pass when

Kill n3: within 5 seconds n1 and n2 mark it suspect, then dead. Restart it and it returns to alive. Every node shows the same view.

## New in Go

failure detection · timers · state machines · jitter

Standard library: `time` · `math/rand/v2` · `sync`
