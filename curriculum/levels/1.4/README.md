# 1.4 · Decay

Keys that expire. Don't delete them on a timer; decide at read time whether an entry is still alive, using a clock the tests can control. The atomic `CompareAndSwapWithTTL` added here is what a lease lock needs in one call.

## Build

```go
// store/store.go additions
var ErrInvalidTTL = errors.New("store: invalid ttl")

// Entry gains:
//   ExpiresAt time.Time  // zero means never

// Store gains:
//   SetWithTTL(ctx context.Context, key string, value []byte, ttl time.Duration) (Entry, error)
//   CompareAndSwapWithTTL(ctx context.Context, key string, ifVersion uint64, value []byte, ttl time.Duration) (Entry, error)
//     CAS rules from 1.3, and the new entry expires at now+ttl. This is the one atomic
//     create-if-absent-with-expiry that a lease lock (8.1) needs.
//   Touch(ctx context.Context, key string, ttl time.Duration) (Entry, error)
//     Touch resets the expiry of an existing key to now+ttl without changing value or version.
//     ErrNotFound for a missing or expired key.

// store/memory.go additions
type Option func(*options)                  // options is your unexported struct
func WithClock(now func() time.Time) Option // default time.Now; tests inject testkit.FakeClock
func NewMemory(opts ...Option) *Memory      // existing NewMemory() calls still compile

// Rules: a key expires when now >= ExpiresAt, with now read from the clock once per operation.
// Expired keys are invisible to Get, Keys, Len, Delete, Touch, CompareAndDelete. CAS with
// ifVersion 0 on an expired key succeeds (it is absent) and the new entry has Version 1.
// A plain Set or CompareAndSwap clears any expiry. ttl <= 0 is ErrInvalidTTL.
// This level installs storetest.RunTTL (takes a clock factory).
```

- `Entry.ExpiresAt`, `SetWithTTL`, `CompareAndSwapWithTTL`, `Touch`, `var ErrInvalidTTL`
- `type Option`, `WithClock`, `NewMemory(opts ...Option)`
- An expiry check on every read and write path, under the lock, using one clock reading per operation
- `Set` and `CompareAndSwap` clear the TTL; CAS treats expired as absent

Decisions the tests rely on:

- The clock is read once per operation; every expiry decision in that operation uses the same `now`.
- `Touch` on a key that has no expiry adds one. `Touch` never changes the version.
- `SetWithTTL` bumps the version exactly like `Set`. `CompareAndSwapWithTTL` with `ttl <= 0` is `ErrInvalidTTL`; use `CompareAndSwap` for no expiry.

## The tests check

- With testkit.FakeClock: SetWithTTL 10s; Advance(10s-1ns) and the key exists; Advance(1ns) and Get is ErrNotFound
- Expired keys are invisible to Get, Keys, Len, Delete (ErrNotFound), Touch (ErrNotFound), CompareAndDelete (ErrNotFound)
- CAS ifVersion 0 on an expired key succeeds with Version 1; CAS with the old version on an expired key is ErrNotFound
- CompareAndSwapWithTTL: create-if-absent with ifVersion 0 sets ExpiresAt = now+ttl and Version 1; a mismatch changes nothing; ttl <= 0 is ErrInvalidTTL; the new entry expires exactly at now+ttl
- Touch moves ExpiresAt to now+ttl and keeps Value and Version; Touch on a key without expiry adds one; Touch with ttl <= 0 is ErrInvalidTTL
- Plain Set or CompareAndSwap after SetWithTTL clears ExpiresAt (zero); SetWithTTL again restarts the countdown
- Get returns the ExpiresAt that was set, to the nanosecond; the clock is read once per operation (a counting clock proves it)
- One real-clock test: NewMemory() with no options, a 30 ms TTL has expired after 60 ms
- storetest.RunTTL passes

## Uses

- CAS (built in 1.3 · Versions)

## In the wild

Redis EXPIRE/PERSIST/TTL and its lazy expiry on access; etcd leases; memcached's lazy expiration

## New in Go

time.Time and time.Duration · zero values as sentinels · functional options · injectable clocks

Standard library: `time`
