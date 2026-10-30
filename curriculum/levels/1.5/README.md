# 1.5 · Janitor

A background janitor that removes expired entries, and a clean way to stop it. The `OnEvict` hook it adds becomes a watch event in 8.2.

## Build

```go
// store/memory.go additions
var ErrClosed = errors.New("store: closed")

func WithSweepInterval(d time.Duration) Option // default 1s
func WithOnEvict(fn func(e Entry)) Option     // called once per key removed because it expired,
                                              // whether the sweeper or a reader noticed first.
                                              // Never called for Delete, overwrite or CompareAndDelete.

func (m *Memory) Close() error // stops the sweeper and waits for it; safe to call twice
                               // and concurrently; afterwards every operation returns ErrClosed

// The sweeper uses a time.Ticker on the real clock for its wake-ups and the injected
// clock (WithClock) to decide what has expired, so tests advance a FakeClock and wait
// with testkit.Eventually.
```

- `WithSweepInterval`, `WithOnEvict`
- A sweeper goroutine on a time.Ticker that removes entries the injected clock says are expired
- `Close()` that stops it and waits, idempotent, via sync.Once and a done channel
- Exactly-once `OnEvict`, called outside the lock

This is your first goroutine. The pattern to copy for every background goroutine you write from here on: start it in the constructor, give it a `done` channel to watch in its `select`, and have `Close` close that channel (inside a `sync.Once`) and then wait for the goroutine to exit, with a `sync.WaitGroup` or a second channel the goroutine closes on its way out. Closing a channel is a broadcast: every receiver unblocks at once, which is why `done` is a channel and not a bool.

Decisions the tests rely on:

- A reader that notices an expired key removes it and fires `OnEvict` itself, so evictions happen with the sweeper stopped too. Whoever removes the entry under the write lock owns the callback.
- The ticker runs on the real clock; what counts as expired comes from `WithClock`.
- `Close` waits for the sweeper, including any `OnEvict` call it is in the middle of.

## The tests check

- WithClock(fake.Now), WithSweepInterval(5ms): 100 keys with 1s TTL, Advance(2s); testkit.Eventually OnEvict has fired for all 100 without any read, and 30 ms of further sweeps add no calls
- OnEvict receives the expired Entry with its key, value, version and ExpiresAt
- OnEvict exactly once per key: 200 expired keys, 4 reader goroutines hammering Get while the sweeper runs; Eventually the count reaches 200 and 20 ms later it is still exactly 200
- OnEvict is not called for Delete, overwrite with Set, or CompareAndDelete (fake clock advanced an hour, 30 ms of sweeps, count is 0)
- OnEvict may call m.Set on another key without deadlocking, and that Set lands
- Real clock: WithSweepInterval(5ms) and a 30 ms TTL, Len reaches 0 within 2 s with no reads
- Close stops the sweeper: testkit.NoLeaks sees the goroutine count return to baseline; Close from 10 goroutines at once and a second Close all return nil
- After Close every method (Get, Set, Delete, Keys, CompareAndSwap, CompareAndDelete, SetWithTTL, Touch) returns ErrClosed

## Uses

- Expiry (built in 1.4 · Decay)

## In the wild

Redis's active expire cycle; patrickmn/go-cache's janitor; every Go server you'll write has a goroutine that needs exactly this Close discipline

## New in Go

goroutines · the go statement · time.Ticker · select · done channels · sync.WaitGroup · goroutine lifecycle · sync.Once · callbacks outside locks · testkit.Eventually and NoLeaks

Standard library: `time`, `sync`, `context`
