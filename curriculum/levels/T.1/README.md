# T.1 · Token Bucket

Rate-limit clients by IP with a token bucket middleware, swept by a janitor-style goroutine.

## Build

```go
func RateLimit(rate float64, burst int) Middleware
```

- Bucket math with an injected clock (WithClock)
- 429 + Retry-After
- Sweeper with Close

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/T.1/files/`, matching the earlier chapters.

## Pass when

Twenty quick requests show 200s then 429s with Retry-After, and a FakeClock test proves refill at the right rate.

The suite will cover:

- Burst then 429
- Refill at the configured rate on a FakeClock
- Stale buckets are swept
- Per-IP isolation

## Uses

- Middleware chain (built in 7.3 · Signal Chain)
- Janitor (built in 1.5 · Janitor)

## In the wild

golang.org/x/time/rate; Envoy's local rate limit filter

## New in Go

time arithmetic · maps of state · cleanup goroutines

Standard library: `net`, `net/http`, `sync`, `time`
