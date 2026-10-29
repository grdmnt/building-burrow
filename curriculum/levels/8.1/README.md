# 8.1 · Lease Lock

Three chapter 1 primitives, over HTTP, make a distributed lock. `CompareAndSwapWithTTL` with `ifVersion` 0 creates the lock with its lease in one call, `Touch` keeps it alive without changing the version, and that version is the fencing token.

## Build

```go
package lock

type Lock struct { /* yours */ }
func Acquire(ctx context.Context, c *burrow.Client, key string, ttl time.Duration) (*Lock, error) // blocks, polling with backoff, until held or ctx done
func TryAcquire(ctx context.Context, c *burrow.Client, key string, ttl time.Duration) (*Lock, bool, error)
func (l *Lock) Token() uint64                 // the entry's version: strictly increasing across holders of the same key
func (l *Lock) Done() <-chan struct{}          // closed if the lease is lost (keepalive failed past ttl/2)
func (l *Lock) Release(ctx context.Context) error // CompareAndDelete with the token; ErrLost if someone else holds it
```

- Acquire/TryAcquire via CAS if_version 0 + ttl
- A keepalive goroutine touching at ttl/3 and closing Done on failure
- Release via CompareAndDelete; fencing token

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/8.1/files/`, matching the earlier chapters.

## Pass when

Two clients contend for one key on a fake clock: exactly one holds at a time, a holder whose process stops touching loses the lock after ttl, the next holder's token is larger, and Release by a stale holder fails with ErrLost.

The suite will cover:

- Mutual exclusion across 10 goroutines x 20 acquisitions on one key (a shared counter incremented without CAS stays consistent)
- A holder that stops its keepalive (test hook) sees Done closed and another acquirer succeeds after the fake clock passes ttl
- Tokens strictly increase across successive holders
- Release by a stale holder is ErrLost and doesn't delete the new holder's lock
- Acquire respects ctx (cancel while waiting returns promptly)

## Uses

- HTTP client lib (built in 7.5 · Client Library)
- CAS (built in 1.3 · Versions)
- Expiry (built in 1.4 · Decay)

## In the wild

etcd's concurrency.Mutex and Lease API; Redis Redlock (and the argument against it); Consul sessions

## New in Go

composition of primitives · keepalive goroutines · fencing tokens · fake clocks across client and server

Standard library: `context`, `time`, `sync`
