# S1.1 · Fan-out

An in-process broker that tells subscribers when keys change, without letting slow readers stall writes.

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/S1.1/files/`, matching the earlier chapters.

## Build

- The store publishes an event on every Set and Delete
- `Subscribe(prefix) (<-chan Event, func())` returns a channel and an unsubscribe function
- Buffered subscriber channels; a subscriber that falls too far behind gets disconnected

## Pass when

With one subscriber that never reads, 1,000 writes take no longer than with none, and that subscriber is dropped. Fast subscribers see every event in order.

## New in Go

buffered channels · select with default · unsubscribe functions · sync.Once

Standard library: `sync` · `context` · `strings`
