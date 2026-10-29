# 5.5 · Gauges

See inside the running server.

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/5.5/files/`, matching the earlier chapters.

## Build

- `/healthz` and `/readyz`
- expvar counters: requests, errors, keys, open connections
- `net/http/pprof` on a separate admin port
- A log level you can change at runtime

## Pass when

`curl :7071/debug/vars` shows your counters climbing under load, and `go tool pprof http://localhost:7071/debug/pprof/profile?seconds=10` opens a profile.

## New in Go

expvar · pprof endpoints · liveness vs readiness · admin listeners

Standard library: `expvar` · `net/http/pprof` · `log/slog`
