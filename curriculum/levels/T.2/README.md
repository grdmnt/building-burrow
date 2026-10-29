# T.2 · Read-Through Cache

A read-through cache in front of a slow store: a generic LRU bounded by bytes, invalidated on writes, with single-flight so a stampede of identical misses costs one read.

## Build

```go
type LRU[K comparable, V any] struct{...}
func NewLRU[K comparable, V any](maxBytes int, size func(V) int) *LRU[K, V]
func Wrap(st store.Store, lru *LRU[string, store.Entry]) store.Store
```

- Generic LRU
- Invalidate on every mutating method
- Single-flight map[string]*call
- Load test through RateLimit + cache

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/T.2/files/`, matching the earlier chapters.

## Pass when

Tests prove eviction order; with a 50 ms store, 100 concurrent GETs for one cold key cause exactly 1 store read and all 100 get the value; the load test through the rate limiter shows 429s for the abuser and a 95% hit rate for the rest.

The suite will cover:

- Eviction order
- Invalidate before serving stale
- Single flight: exactly one read, shared error not cached
- Two instantiations compile

## Uses

- Memory store (built in 1.1 · Register File)
- Rate limiter (built in T.1 · Token Bucket)

## In the wild

golang.org/x/sync/singleflight, hashicorp/golang-lru, groupcache

## New in Go

generics · container/list · WaitGroup as a latch · cache invalidation

Standard library: `container/list`, `sync`, `expvar`
