# 8.1 · Lease Lock

> A lock nobody can hold forever. If the holder dies, the lease runs out and the next one in line gets it.

## Your goal

Write a `lock` package so that several processes can agree that only one of them is doing a job at a time. A *distributed lock* is a lock that lives in Burrow instead of in one program's memory, so programs on different machines can share it. The lock is a key with a *lease*: a TTL that the holder keeps extending while it is alive. If the holder crashes, it stops extending, the key expires, and someone else can take the lock. The version the store assigned when the lock was created is handed out as a *fencing token*, a number a careful service can use to reject work from a holder whose lease has already run out.

Nothing new has to be added to the store or the server. Everything here is built on calls you already have, through the HTTP client.

## What you're using from before

**HTTP client lib (7.5 · Client Library).** `burrow.Client` is a `store.Store` that talks to the server over HTTP. The lock never touches the store directly; every operation below is a method on `*burrow.Client` and takes a `ctx`:

```go
c, err := burrow.New("http://127.0.0.1:8080", burrow.Options{})
l, err := lock.Acquire(ctx, c, "jobs/nightly", 10*time.Second)
```

**CAS (1.3 · Versions).** `CompareAndSwap` with `ifVersion` 0 means "create this key only if it does not exist", and every entry carries a `Version`. Acquiring is one create-if-absent call; the `Version` of the entry it returns is the token. Releasing is `CompareAndDelete(ctx, key, token)`, which only deletes if the version still matches, so a holder whose lease ran out and was replaced cannot delete the new holder's lock:

```go
// held by nobody: creates it, e.Version is the token
e, err := c.CompareAndSwapWithTTL(ctx, key, 0, nil, ttl)
// held by someone: errors.Is(err, store.ErrVersionMismatch)
```

**Expiry (1.4 · Decay).** `CompareAndSwapWithTTL` is the CAS above plus an expiry in one atomic call, so there is no moment where the key exists without a lease. `Touch(ctx, key, ttl)` pushes the expiry out to now+ttl without changing the value or the version. That is what the keepalive calls:

```go
_, err := c.Touch(ctx, key, ttl) // store.ErrNotFound: the lease is gone
```

## What's new here

**Composing primitives.** Nothing in this level is a new store operation. A lock is a *recipe* over three calls you already have: create-if-absent-with-TTL to acquire, Touch to keep, CompareAndDelete to release. The work is in getting the recipe right under failure: what happens when the holder stops, when a Touch fails, when two releasers race.

**A keepalive goroutine.** The lease is `ttl` long, so the holder must call Touch well before that, at `ttl/3`. That loop runs in its own goroutine, started in `Acquire`, with the same shape as the janitor in 1.5: a ticker, a `stop` channel, a `select`.

```go
t := time.NewTicker(ttl / 3)
defer t.Stop()
for {
	select {
	case <-stop:
		return
	case <-t.C:
		// Touch; count failures;
		// close Done once the lease is surely gone
	}
}
```

The goroutine owns `Done`: it closes that channel when Touch has failed twice in a row and more than `ttl/2` has passed since the last success. Closing a channel wakes every reader at once, so the holder can watch `<-l.Done()` in a `select` next to its real work and stop as soon as the lease is lost.

**Fencing tokens.** A lock with a lease has a gap: the holder may believe it still holds the lock after the lease expired (a GC pause, a network blip). Anything it does in that gap is unsafe unless the service it talks to can tell old holders from new ones. That is the token's job. It is the `Version` the store assigned when the lock key was created, and a stale holder's token no longer matches what the store holds, which is why `Release` uses `CompareAndDelete` with it rather than a plain `Delete`. Read Kleppmann's *How to do distributed locking* once you have it working; it explains the gap better than this paragraph can.

**One fake clock on both sides.** The lock talks to the server over HTTP, so a test runs the real `api.Server` on a `Memory` store built with `store.WithClock(fake.Now)` and points `burrow.Client` at it. Advancing the fake clock makes the lease expire on the server without any waiting:

```go
fake := testkit.NewFakeClock()
st := store.NewMemory(store.WithClock(fake.Now))
srv := httptest.NewServer((&api.Server{Store: st}).Handler())
c, _ := burrow.New(srv.URL, burrow.Options{})
// ... Acquire, then fake.Advance(ttl) and another Acquire succeeds
```

Your keepalive ticker runs on real time, so leave yourself an unexported way to stop it in a test (a field or option), otherwise it keeps touching and the lease never expires.

## What to do

1. Create `lock/lock.go`, `package lock`, with the `Lock` type and `ErrLost`.
2. Write `TryAcquire`: one `CompareAndSwapWithTTL` with `ifVersion` 0. Success means you hold it: remember the key, the client, the `ttl` and the returned `Version`, start the keepalive goroutine, return `(l, true, nil)`. `store.ErrVersionMismatch` means someone else holds it: return `(nil, false, nil)`. Any other error comes back as-is.
3. Write `Acquire` as a loop over `TryAcquire`: on "held by someone else", sleep with a small backoff and try again; stop and return `ctx.Err()` as soon as `ctx` is done.
   - Sleep with `select` over `time.After` and `ctx.Done()`, not `time.Sleep`, so cancel returns promptly.
4. Write the keepalive goroutine: `Touch` every `ttl/3`. On success, note the time. After two consecutive failures with more than `ttl/2` since the last success, close `Done` and exit. Exit on `Release` too.
5. `Token()` returns the version from the acquiring CAS. `Done()` returns the channel the keepalive closes.
6. Write `Release`: stop the keepalive, then `CompareAndDelete(ctx, key, token)`. `store.ErrNotFound` or `store.ErrVersionMismatch` mean the lease was lost and possibly reacquired by someone else: return `ErrLost`. Make `Release` safe to call twice.
7. Keep every blocking call on the `ctx` you were given, and keep the keepalive on its own context that `Release` cancels.

## Example

Two programs contend for the same key. The second one blocks until the first releases:

```go
l1, _ := lock.Acquire(ctx, c, "jobs/nightly", 10*time.Second)
fmt.Println("p1 holds")                         // p1 holds

_, held, _ := lock.TryAcquire(ctx, c, "jobs/nightly", 10*time.Second)
fmt.Println("p2 got it:", held)                 // p2 got it: false

go func() {
	time.Sleep(time.Second)
	l1.Release(ctx)
}()
l2, _ := lock.Acquire(ctx, c, "jobs/nightly", 10*time.Second)
fmt.Println("p2 holds")                         // p2 holds, ~1s later

err := l1.Release(ctx)
fmt.Println(errors.Is(err, lock.ErrLost))       // true
fmt.Println(l2.Release(ctx))                    // <nil>
```

And the shape of a holder's main loop, which stops as soon as the lease is lost:

```go
for {
	select {
	case <-l.Done():
		return errors.New("lost the lock, stopping")
	case job := <-jobs:
		do(job, l.Token())
	}
}
```

## Done when

- [ ] 10 goroutines each acquiring a key 20 times never overlap: a plain counter they increment while holding the lock ends at 200
- [ ] A holder whose keepalive is stopped (your test hook) sees `Done` closed, and after the fake clock passes `ttl` another `Acquire` succeeds
- [ ] Tokens of successive holders of one key strictly increase
- [ ] `Release` by a holder whose lease was lost returns `ErrLost` and does not delete the current holder's lock
- [ ] `Acquire` returns `ctx.Err()` promptly when the context is cancelled while waiting

## Reference

```go
package lock

var ErrLost = errors.New("lock: lost")

type Lock struct { /* yours */ }

// Acquire blocks, polling with backoff, until the lock is held or ctx
// is done.
func Acquire(ctx context.Context, c *burrow.Client, key string,
	ttl time.Duration) (*Lock, error)

// TryAcquire makes one attempt. (nil, false, nil) means someone holds
// it.
func TryAcquire(ctx context.Context, c *burrow.Client, key string,
	ttl time.Duration) (*Lock, bool, error)

// Token is the entry's version: strictly increasing across holders of
// the same key.
func (l *Lock) Token() uint64

// Done is closed if the lease is lost (keepalive failed past ttl/2).
func (l *Lock) Done() <-chan struct{}

// Release is CompareAndDelete with the token; ErrLost if someone else
// holds the key.
func (l *Lock) Release(ctx context.Context) error
```

- Acquire and TryAcquire use `CompareAndSwapWithTTL` with `ifVersion` 0 and the lease `ttl`. The JSON API's `PutRequest` carries both `if_version` and `ttl`, so this is one HTTP call.
- A keepalive goroutine touches at `ttl/3`. After two consecutive failures past `ttl/2` it assumes the lease is gone and closes `Done`.
- Release uses `CompareAndDelete` with the token. The token is the fencing token.

## The tests check

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/8.1/files/`, matching the earlier chapters.

Pass when: two clients contend for one key on a fake clock: exactly one holds at a time, a holder whose process stops touching loses the lock after ttl, the next holder's token is larger, and Release by a stale holder fails with ErrLost.

The suite will cover:

- Mutual exclusion across 10 goroutines x 20 acquisitions on one key (a shared counter incremented without CAS stays consistent)
- A holder that stops its keepalive (test hook) sees Done closed and another acquirer succeeds after the fake clock passes ttl
- Tokens strictly increase across successive holders
- Release by a stale holder is ErrLost and doesn't delete the new holder's lock
- Acquire respects ctx (cancel while waiting returns promptly)

## Try it

```bash
./burrow watch
go run ./cmd/burrowd serve -http 127.0.0.1:8080 &
# in a scratch main: Acquire in two goroutines with a 5s ttl, print
# who holds and when; kill the holder's process and watch the other
# take over
```

## In the wild

etcd's concurrency.Mutex and Lease API; Redis Redlock (and the argument against it); Consul sessions.

## New in Go

composition of primitives · keepalive goroutines · fencing tokens · fake clocks across client and server

Standard library: `context`, `time`, `sync`
