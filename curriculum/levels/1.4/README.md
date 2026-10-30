# 1.4 · Decay

> Some slots are only meant to hold their value for a while. Nobody wipes them; they just stop counting.

## Your goal

Let a key carry an expiry time. `SetWithTTL` stores a value that stops existing after a *TTL* (time to live, a duration like 10 seconds), `Touch` pushes an existing key's expiry further out, and `CompareAndSwapWithTTL` is CAS from 1.3 with an expiry attached, in one atomic step.

Nothing gets deleted on a timer in this level. Instead every method asks "is this entry still alive right now?" before it uses it, and treats a dead one as if it were not there. That is called lazy expiry. To test it without real waiting, the store reads the time from a clock you can swap out, which is also your first *functional option*.

## What you're using from before

**CAS (1.3).** `CompareAndSwapWithTTL` has exactly the version rules of `CompareAndSwap` (0 means create-if-absent, otherwise the version must match), plus it sets the expiry on the new entry. Both share the same comparison code; only what gets stored differs:

```go
// CompareAndSwap is CompareAndSwapWithTTL with "no expiry", so one
// unexported helper under the lock can serve both:
//   cas(key, ifVersion, value, expiresAt time.Time)
// with a zero expiresAt meaning never.
```

The write lock from 1.2 still wraps everything, and the expiry check happens inside it. The versions from 1.3 keep working: `SetWithTTL` bumps the version like `Set`, `Touch` leaves it alone, and an expired key that comes back starts at version 1, exactly like a deleted one.

## What's new here

**`time.Time` and `time.Duration`.** `time.Time` is an instant; `time.Duration` is a length of time stored as an `int64` of nanoseconds, with constants so you never write raw numbers:

```go
ttl := 10 * time.Second            // a Duration
now := time.Now()                  // a Time
later := now.Add(ttl)              // Time + Duration = Time
later.Sub(now)                     // Time - Time = Duration (10s)
now.Before(later)                  // true
!now.Before(later)                 // false: "now >= later" is written
                                   // this way, Time has no >= operator
var never time.Time                // the zero Time
never.IsZero()                     // true
```

Compare `Time` values with `Equal`, `Before` and `After`, not `==`, because two `Time`s can describe the same instant with different internal state.

**Zero values as sentinels.** Every Go type has a zero value (`0`, `""`, `nil`, a struct with all fields zero). When "no value" is a meaningful state, the idiom is to use the zero value for it instead of a pointer or a flag. An `Entry` with a zero `ExpiresAt` never expires, and `Entry{}.ExpiresAt.IsZero()` is how you check.

**Functional options.** `NewMemory()` needs a way to accept settings without a long parameter list that grows every level. The Go idiom is a variadic list of functions, each of which sets one field on an unexported settings struct. Here is the pattern on a toy type:

```go
type greeter struct{ name string; loud bool }

type Option func(*greeter)

func WithName(n string) Option {
    return func(g *greeter) { g.name = n }
}
func Loud() Option {
    return func(g *greeter) { g.loud = true }
}

func NewGreeter(opts ...Option) *greeter {
    g := &greeter{name: "world"}     // defaults first
    for _, opt := range opts {
        opt(g)                       // each option overrides one thing
    }
    return g
}

NewGreeter()                          // defaults
NewGreeter(WithName("Gerard"), Loud())
```

`opts ...Option` means "zero or more `Option` values", so every existing `NewMemory()` call keeps compiling.

**An injectable clock.** If the store calls `time.Now()` directly, a test of "expires after 10 seconds" must really wait 10 seconds. Instead the store holds a field `now func() time.Time`, defaulting to `time.Now`, and `WithClock` replaces it. The tests pass `testkit.FakeClock`, whose `Now` returns a fixed instant until `Advance` moves it:

```go
clock := testkit.NewFakeClock()
m := store.NewMemory(store.WithClock(clock.Now))
m.SetWithTTL(ctx, "k", []byte("v"), 10*time.Second)
clock.Advance(10*time.Second - time.Nanosecond)
m.Get(ctx, "k")                   // still there
clock.Advance(time.Nanosecond)
m.Get(ctx, "k")                   // store.ErrNotFound
```

`clock.Now` without parentheses is a *method value*: the method bound to that clock, usable wherever a `func() time.Time` is wanted. Read the clock once at the top of each method, into a local `now`, and make every decision in that call from the same reading; two readings a few nanoseconds apart can disagree about whether a key is alive.

## What to do

1. Add `ExpiresAt time.Time` to `Entry`, `ErrInvalidTTL`, and the three new methods to the `Store` interface, as in the Reference.
2. Add an unexported options struct with a `now func() time.Time` field, `type Option func(*options)`, `WithClock`, and change `NewMemory` to `NewMemory(opts ...Option)`. Default `now` to `time.Now`.
3. Store the expiry with each entry (a zero `time.Time` means never) and write one unexported helper that says whether an entry is alive at a given `now`: alive means no expiry, or `now` is before `ExpiresAt`. At exactly `ExpiresAt` the key is gone.
4. Go through every existing method. Read the clock once at the top. Under the lock, treat an entry that isn't alive as absent: `Get`, `Delete`, `Touch` and `CompareAndDelete` say `ErrNotFound`; `Keys` and `Len` skip it; `Set` and CAS with `ifVersion` 0 create it afresh at version 1; CAS with a non-zero `ifVersion` says `ErrNotFound`. Expired entries may stay in the map for now; the rule is only that nobody can observe them.
5. Make a plain `Set` or `CompareAndSwap` clear any expiry: the stored `ExpiresAt` becomes zero.
6. Write the three new methods. Each rejects `ttl <= 0` with `ErrInvalidTTL` (after validating the key and before changing anything), and sets `ExpiresAt` to `now + ttl`:
   - `SetWithTTL`: like `Set`, bumps the version, restarts the countdown.
   - `CompareAndSwapWithTTL`: like `CompareAndSwap`, with the expiry on the result.
   - `Touch`: only for an existing, live key; keeps value and version, moves the expiry. A key with no expiry gains one.
7. Return `ExpiresAt` in every `Entry` you hand out.

## Example

```go
clock := testkit.NewFakeClock()
m := store.NewMemory(store.WithClock(clock.Now))
ctx := context.Background()

e, _ := m.SetWithTTL(ctx, "session", []byte("abc"), 10*time.Second)
// e.Version == 1, e.ExpiresAt == clock.Now().Add(10*time.Second)

e, _ = m.Set(ctx, "config", []byte("x"))
// e.ExpiresAt.IsZero() == true: no expiry

clock.Advance(9 * time.Second)
e, _ = m.Touch(ctx, "session", 30*time.Second)
// e.Version == 1 (unchanged), e.ExpiresAt is now 30s from the new now

clock.Advance(30 * time.Second)
_, err := m.Get(ctx, "session")        // store.ErrNotFound
m.Len()                                // 1: only "config"
_, err = m.Touch(ctx, "session", time.Minute)   // store.ErrNotFound

e, _ = m.Set(ctx, "session", []byte("new"))
// e.Version == 1 again: the expired entry was gone

_, err = m.SetWithTTL(ctx, "k", []byte("v"), 0)   // store.ErrInvalidTTL

// a lease lock in one call: create-if-absent with an expiry
e, err = m.CompareAndSwapWithTTL(ctx, "lock", 0, []byte("me"),
    time.Minute)
// err == nil, e.Version == 1
_, err = m.CompareAndSwapWithTTL(ctx, "lock", 0, []byte("you"),
    time.Minute)
// store.ErrVersionMismatch: held
clock.Advance(time.Minute)
_, err = m.CompareAndSwapWithTTL(ctx, "lock", 0, []byte("you"),
    time.Minute)
// err == nil: the lease lapsed, "you" holds it now at version 1
```

## Done when

- [ ] With a fake clock, a 10 s TTL key exists 1 ns before the deadline and is `ErrNotFound` at it; `ExpiresAt` is exactly `now + ttl`
- [ ] A key set without a TTL has a zero `ExpiresAt`
- [ ] Expired keys are invisible everywhere: `Get`, `Keys`, `Len`, `Delete`, `Touch`, `CompareAndDelete`, and CAS with a non-zero version
- [ ] `Set` and CAS with `ifVersion` 0 on an expired key start it over at version 1
- [ ] `Touch` keeps value and version, moves `ExpiresAt`, adds an expiry to a key that had none, and restarts the countdown when called again
- [ ] `SetWithTTL` on a live key bumps the version and restarts the countdown
- [ ] `CompareAndSwapWithTTL` creates with an expiry atomically; a failed one changes nothing; a later plain `CompareAndSwap` clears the expiry
- [ ] `ttl <= 0` is `ErrInvalidTTL` on all three methods and changes nothing
- [ ] `NewMemory()` with no options uses the real clock
- [ ] `storetest.RunTTL` passes

## Reference

```go
// store/store.go additions
var ErrInvalidTTL = errors.New("store: invalid ttl")

// Entry gains:
//   ExpiresAt time.Time  // zero means never

// Store gains:
//   SetWithTTL(ctx context.Context, key string, value []byte,
//       ttl time.Duration) (Entry, error)
//   CompareAndSwapWithTTL(ctx context.Context, key string,
//       ifVersion uint64, value []byte,
//       ttl time.Duration) (Entry, error)
//     CAS rules from 1.3, and the new entry expires at now+ttl. This is
//     the one atomic create-if-absent-with-expiry a lease lock needs.
//   Touch(ctx context.Context, key string,
//       ttl time.Duration) (Entry, error)
//     Touch resets the expiry of an existing key to now+ttl without
//     changing value or version. ErrNotFound for a missing or expired
//     key.

// store/memory.go additions
type Option func(*options)                  // options: your own struct
func WithClock(now func() time.Time) Option // default time.Now; tests
                                            // inject testkit.FakeClock
func NewMemory(opts ...Option) *Memory      // existing NewMemory()
                                            // calls still compile

// Rules: a key expires when now >= ExpiresAt, with now read from the
// clock once per operation. Expired keys are invisible to Get, Keys,
// Len, Delete, Touch, CompareAndDelete. CAS with ifVersion 0 on an
// expired key succeeds (it is absent) and the new entry has Version 1.
// A plain Set or CompareAndSwap clears any expiry. ttl <= 0 is
// ErrInvalidTTL. This level installs storetest.RunTTL. It runs on the
// real clock with short TTLs, so the same suite works for every engine.
```

Decisions the tests rely on:

- The clock is read once per operation; every expiry decision in that operation uses the same `now`.
- `Touch` on a key that has no expiry adds one. `Touch` never changes the version.
- `SetWithTTL` bumps the version exactly like `Set`. `CompareAndSwapWithTTL` with `ttl <= 0` is `ErrInvalidTTL`; use `CompareAndSwap` for no expiry.

## The tests check

- With testkit.FakeClock through WithClock: SetWithTTL 10s; Advance(10s-1ns) and the key exists; Advance(1ns) and Get is ErrNotFound; ExpiresAt equals the clock's now + 10s exactly; a key without TTL has a zero ExpiresAt
- Expired keys are invisible to Get, Keys, Len, Delete (ErrNotFound), Touch (ErrNotFound), CompareAndDelete (ErrNotFound)
- CAS with the old version on an expired key is ErrNotFound; CAS ifVersion 0 on an expired key succeeds with Version 1; Set on an expired key starts over at Version 1
- Touch keeps Value and Version and moves ExpiresAt to now+ttl; Touch on a key without expiry adds one; a second Touch restarts the countdown
- SetWithTTL on a live key bumps the version and restarts the countdown
- CompareAndSwapWithTTL: ifVersion 0 creates with Version 1 and ExpiresAt = now+ttl; a second create-if-absent is ErrVersionMismatch and changes neither value nor expiry; once expired the key is absent again and create-if-absent succeeds; a plain CompareAndSwap afterwards clears the expiry, like Set
- ttl 0 and negative are ErrInvalidTTL for SetWithTTL, Touch and CompareAndSwapWithTTL, and change nothing; SetWithTTL validates the key first
- NewMemory() with no options uses time.Now: a 1h TTL has an ExpiresAt about an hour away
- storetest.RunTTL passes; it runs on the real clock with 60 ms TTLs so it also works for remote engines later

## Try it

```bash
./burrow watch
go test -race -v -run TestL1_4_ExpiresAtExactlyTTL ./store/
go test -race -v -run TestL1_4_TTLConformance ./store/  # real clock
```

## In the wild

Redis EXPIRE/PERSIST/TTL and its lazy expiry on access; etcd leases; memcached's lazy expiration

## New in Go

time.Time and time.Duration · zero values as sentinels · functional options · variadic parameters · method values · injectable clocks

Standard library: `time`
