# 5.3 · Watchdog

Make slow clients and slow disks unable to hang your server.

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/5.3/files/`, matching the earlier chapters.

## Build

- `http.Server` with `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, `IdleTimeout`
- A per-request deadline in middleware
- Store methods return when `ctx` is done
- `server.Shutdown(ctx)` wired to your signal handling from 2.5

## Pass when

A store with an injected 5 second delay makes requests fail with 503 after your 1 second budget, and the store sees its context cancelled. Ctrl-C drains HTTP and TCP together.

## New in Go

context deadlines · cancellation propagation · server timeouts · graceful HTTP shutdown

Standard library: `net/http` · `context` · `time`
