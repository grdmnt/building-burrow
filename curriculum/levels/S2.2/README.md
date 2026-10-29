# S2.2 · LRU Cache

A read-through cache in front of a slow store.

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/S2.2/files/`, matching the earlier chapters.

## Build

- An LRU cache bounded by total bytes, built on `container/list` and a map
- Invalidate on Set and Delete
- Hit and miss counters in expvar

## Pass when

Tests prove eviction order. With a store that takes 50 ms per read, cache hits return in under a millisecond, and the counters show the hit rate.

## New in Go

linked lists · cache invalidation · generics (optional)

Standard library: `container/list` · `sync` · `expvar`
