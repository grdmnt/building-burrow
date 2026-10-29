# 6.5 · Redundancy

Store each key on three nodes and survive losing one.

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/6.5/files/`, matching the earlier chapters.

## Build

- N=3 replicas chosen from the ring
- Writes fan out in parallel and succeed at W=2 acks
- Reads ask R=2 and return the newest version
- Cancel the slow replica once quorum is met; repair stale copies on read

## Pass when

With one node down, reads and writes still succeed. After it restarts, read repair brings its copy up to date. Goroutine counts match before and after a test.

## New in Go

fan-out and fan-in · first k of n · context cancellation · versioning · read repair

Standard library: `context` · `sync` · `time` · `runtime`
