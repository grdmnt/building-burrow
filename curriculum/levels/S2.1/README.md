# S2.1 · Token Bucket

Rate-limit clients by IP with a token bucket middleware.

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/S2.1/files/`, matching the earlier chapters.

## Build

- A token bucket per client IP: refill rate and burst size
- `429 Too Many Requests` with a `Retry-After` header
- Stale buckets cleaned up in the background

## Pass when

Twenty quick curls show 200s then 429s with `Retry-After`. A test with an injected clock proves tokens refill at the right rate.

## New in Go

time arithmetic · maps of state · middleware · cleanup goroutines

Standard library: `net` · `net/http` · `sync` · `time` · `strconv`
