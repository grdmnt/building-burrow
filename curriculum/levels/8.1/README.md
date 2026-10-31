# 8.1 · Lease Lock

> A lock nobody can hold forever. If the holder dies, the lease runs out and the next one in line gets it.

## Your goal

Write a `lock` package so that several processes can agree that only one of them is doing a job at a time. A *distributed lock* is a lock that lives in Burrow instead of in one program's memory, so programs on different machines can share it. The lock is a key with a *lease*: a TTL that the holder keeps extending while it is alive. If the holder crashes, it stops extending, the key expires, and someone else can take the lock.

Every holder also gets a *fencing token*: a number that is larger for every later holder, so a careful service can reject work from a holder whose lease has already run out. The token comes from a second key, `<key>.fence`, that is never deleted.

Nothing new has to be added to the store or the server. Everything here is built on calls you already have, through the HTTP client.

## What you're using from before

**HTTP client lib (7.5 · Client Library).** `burrow.Client` is a `store.Store` that talks to the server over HTTP. The lock never touches the store directly; every operation below is a method on `*burrow.Client` and takes a `ctx`:

```go
c, err := burrow.New("http://127.0.0.1:8080", burrow.Options{})
l, err := lock.Acquire(ctx, c, "jobs/nightly", 10*time.Second)
```

**CAS (1.3 · Versions).** `CompareAndSwap` with `ifVersion` 0 means "create this key only if it does not exist", and every entry carries a `Version`. `Set` on an existing key bumps its version by one. Acquiring creates the lock key holding your owner ID; releasing is `CompareAndDelete` with the version that create returned:

```go
// held by nobody: creates it at Version 1
e, err := c.CompareAndSwapWithTTL(ctx, key, 0, []byte(owner), ttl)
// held by someone: errors.Is(err, store.ErrVersionMismatch)
```

**Expiry (1.4 · Decay).** `CompareAndSwapWithTTL` is the CAS above plus an expiry in one atomic call, so there is no moment where the key exists without a lease. `Touch(ctx, key, ttl)` pushes the expiry out to now+ttl without changing the value or the version. That is what the keepalive calls:

```go
_, err := c.Touch(ctx, key, ttl) // store.ErrNotFound: the lease is gone
```

The rule from 1.3 and 1.4 that matters most here: a key that was deleted or expired starts again at Version 1 when it is created again.

## What's new here

**Composing primitives.** Nothing in this level is a new store operation. A lock is a *recipe* over calls you already have: create-if-absent-with-TTL to acquire, Set on a counter key for the token, Get to check, Touch to keep, CompareAndDelete to release. The work is in getting the recipe right under failure: what happens when the holder stops, when a Touch fails, when two releasers race.

**Owner IDs.** Each `TryAcquire` makes a random owner ID with `crypto/rand` and stores it as the lock key's value. The ID is how a holder recognises its own lock. The version can't do that job: every holder creates the key fresh, so every holder's lock is at Version 1.

```go
b := make([]byte, 16)
rand.Read(b) // crypto/rand; never fails in Go 1.24
owner := hex.EncodeToString(b)
```

**A keepalive goroutine.** The lease is `ttl` long, so the holder must call Touch well before that, at `ttl/3`. That loop runs in its own goroutine, started in `TryAcquire`, with the same shape as the janitor in 1.5: a ticker, a `stop` channel, a `select`.

```go
t := time.NewTicker(ttl / 3)
defer t.Stop()
for {
	select {
	case <-stop:
		return
	case <-t.C:
		// Touch, then Get and check the owner ID; count failures;
		// close Done once the lease is surely gone
	}
}
```

Touch does not look at the value, so after a long pause a holder could touch a lock that someone else now holds. That is why each tick also reads the key back: a different owner ID means the lock is gone, whatever Touch said.

The goroutine owns `Done`: it closes that channel when the owner ID no longer matches, or when Touch has failed twice in a row and more than `ttl/2` has passed since the last success. Closing a channel wakes every reader at once, so the holder can watch `<-l.Done()` in a `select` next to its real work and stop as soon as the lease is lost.

**Fencing tokens.** A lock with a lease has a gap: the holder may believe it still holds the lock after the lease expired (a GC pause, a network blip). Anything it does in that gap is unsafe unless the service it talks to can tell old holders from new ones. That is the token's job: the service remembers the largest token it has seen and refuses anything smaller. Read Kleppmann's *How to do distributed locking* once you have it working; it explains the gap better than this paragraph can.

The lock key's own version can't be the token. The key is deleted on release or expires, and the next holder creates it at Version 1 again:

```text
t=0   A creates jobs/nightly       Version 1
t=5   A pauses (GC); its lease runs out at t=10
t=11  B creates jobs/nightly       Version 1 again
      A wakes up: A and B both hold "token 1"
```

So the token comes from a counter key, `<key>.fence`, that is never deleted and never given a TTL. Right after creating the lock, `Set(ctx, key+".fence", owner)`; the `Version` it returns is the token. A Set on an existing key bumps its version, and a key that is never deleted never restarts, so every Set gets a larger number than the one before.

**Acquire, take a token, verify.** Getting the token is a second call, and the lease can run out between the two. So there is a third step: `Get(key)` must still return your owner ID. If it doesn't, someone else holds the lock now; return `ErrLost` and never use that token.

```text
t=0   C creates the lock
      C pauses for 12 s; its lease runs out at t=10
t=11  D creates the lock, Set fence -> 9, Get = D: token 9
t=12  C wakes: Set fence -> 10, Get = D, not C: ErrLost
```

Without the check, C would carry token 10 while D holds the lock, and the service would turn D away. With it, tokens strictly increase across every holder that got one. If A verified at time t, A's lock key existed from its create to t (only A ever writes A's ID). A later holder can only create the lock once A's key is gone, so after A's verify. It takes its token after its own create, so after A's Set too, and the counter has only gone up since: its token is larger.

**Releasing only your own lock.** `Release` is `CompareAndDelete(ctx, key, lockVersion)`, with the version the create returned. That version is 1 for every holder, so on its own it can't stop a stale holder deleting the next holder's lock. Read the key first and delete only if it still holds your owner ID; anything else is `ErrLost`. Never touch the fence key.

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
2. Write `TryAcquire`:
   - Make a random owner ID (16 bytes from `crypto/rand`, hex-encoded).
   - Create the lock: `CompareAndSwapWithTTL(ctx, key, 0, owner, ttl)`. `store.ErrVersionMismatch` means someone else holds it: return `(nil, false, nil)`. Any other error comes back as-is.
   - Take the token: `Set(ctx, key+".fence", owner)`; the returned `Version` is the token. If the Set fails, release the lock key and return the error.
   - Verify: `Get(ctx, key)` must return your owner ID. If the key is gone or holds another ID, return `(nil, false, ErrLost)`.
   - Remember the key, the client, the `ttl`, the owner ID, the lock version from the create and the token. Start the keepalive goroutine and return `(l, true, nil)`.
3. Write `Acquire` as a loop over `TryAcquire`: on "held by someone else" or `ErrLost`, sleep with a small backoff and try again; stop and return `ctx.Err()` as soon as `ctx` is done.
   - Sleep with `select` over `time.After` and `ctx.Done()`, not `time.Sleep`, so cancel returns promptly.
4. Write the keepalive goroutine: every `ttl/3`, `Touch` the lock key, then `Get` it. A different owner ID (or `ErrNotFound`) closes `Done` at once. Otherwise note the time of each success; after two consecutive failures with more than `ttl/2` since the last success, close `Done` and exit. Exit on `Release` too.
5. `Token()` returns the fence version. `Done()` returns the channel the keepalive closes.
6. Write `Release`: stop the keepalive, `Get` the lock key and check the owner ID, then `CompareAndDelete(ctx, key, lockVersion)`. A missing key, another owner, `store.ErrNotFound` or `store.ErrVersionMismatch` all mean the lease was lost and possibly taken by someone else: return `ErrLost` and delete nothing. Make `Release` safe to call twice.
7. Never delete `<key>.fence` and never give it a TTL.
8. Keep every blocking call on the `ctx` you were given, and keep the keepalive on its own context that `Release` cancels.

## Example

Two programs contend for the same key. The second one blocks until the first releases, and gets a larger token:

```go
l1, _ := lock.Acquire(ctx, c, "jobs/nightly", 10*time.Second)
fmt.Println("p1 holds, token", l1.Token())   // p1 holds, token 1

_, held, _ := lock.TryAcquire(ctx, c, "jobs/nightly", 10*time.Second)
fmt.Println("p2 got it:", held)                 // p2 got it: false

go func() {
	time.Sleep(time.Second)
	l1.Release(ctx)
}()
l2, _ := lock.Acquire(ctx, c, "jobs/nightly", 10*time.Second)
fmt.Println("p2 holds, token", l2.Token())   // p2 holds, token 2

err := l1.Release(ctx)
fmt.Println(errors.Is(err, lock.ErrLost))       // true
fmt.Println(l2.Release(ctx))                    // <nil>
```

Between the two holders, `jobs/nightly` was deleted and created again at Version 1, while `jobs/nightly.fence` went from Version 1 to 2.

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
- [ ] Tokens of three successive holders of one key strictly increase, including when the second holder's lease expires instead of being released
- [ ] A holder whose lease expires between creating the lock and verifying it gets `ErrLost`, and the holder that took over keeps its lock
- [ ] `Release` by a holder whose lease was lost returns `ErrLost` and does not delete the current holder's lock
- [ ] `<key>.fence` is still there, with no expiry, after every holder has released or expired
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
// it; (nil, false, ErrLost) means the lease ran out before the token
// was verified.
func TryAcquire(ctx context.Context, c *burrow.Client, key string,
	ttl time.Duration) (*Lock, bool, error)

// Token is the Version from Set on <key>.fence: strictly increasing
// across holders of the same key.
func (l *Lock) Token() uint64

// Done is closed if the lease is lost: the key holds another owner ID,
// or the keepalive failed past ttl/2.
func (l *Lock) Done() <-chan struct{}

// Release checks the owner ID, then CompareAndDelete with the lock
// version; ErrLost if someone else holds the key.
func (l *Lock) Release(ctx context.Context) error
```

- The lock key `<key>` holds a random owner ID (`crypto/rand`, hex). Acquire and TryAcquire create it with `CompareAndSwapWithTTL` and `ifVersion` 0. The JSON API's `PutRequest` carries both `if_version` and `ttl`, so this is one HTTP call.
- The token is the `Version` returned by `Set(ctx, key+".fence", owner)`, taken right after the create. `<key>.fence` is never deleted and never has a TTL.
- After taking the token, `Get(key)` must return this holder's owner ID; otherwise `ErrLost` and the token is not used.
- A keepalive goroutine touches the lock key at `ttl/3` and checks the owner ID. It closes `Done` when the owner ID differs, or after two consecutive failures past `ttl/2`.
- Release checks the owner ID, then calls `CompareAndDelete(key, lockVersion)` with the version from the create. It never touches `<key>.fence`.

## The tests check

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/8.1/files/`, matching the earlier chapters.

Pass when: two clients contend for one key on a fake clock: exactly one holds at a time, a holder whose process stops touching loses the lock after ttl, every later holder's token is larger (also after an expiry), a holder whose lease runs out before it verifies gets ErrLost, and Release by a stale holder fails with ErrLost.

The suite will cover:

- Mutual exclusion across 10 goroutines x 20 acquisitions on one key (a shared counter incremented without CAS stays consistent)
- A holder that stops its keepalive (test hook) sees Done closed and another acquirer succeeds after the fake clock passes ttl
- Tokens strictly increase across three successive holders, where the second one's lease expires on the fake clock instead of being released
- A holder whose lease expires before it verifies gets ErrLost from TryAcquire (the test's server-side store advances the fake clock past ttl and hands the lock to another owner while the first holder's Set on `<key>.fence` is in flight); the new owner keeps its lock
- Release by a stale holder is ErrLost and doesn't delete the new holder's lock
- `<key>.fence` is never deleted and never has an expiry: after all of the above, Get on it succeeds with a zero ExpiresAt
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

composition of primitives · random owner IDs with crypto/rand · keepalive goroutines · fencing tokens from a counter key · fake clocks across client and server

Standard library: `context`, `time`, `sync`, `crypto/rand`, `encoding/hex`
