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

Decisions the tests rely on:

- A reader that notices an expired key removes it and fires `OnEvict` itself, so evictions happen with the sweeper stopped too. Whoever removes the entry under the write lock owns the callback.
- The ticker runs on the real clock; what counts as expired comes from `WithClock`.
- `Close` waits for the sweeper, including any `OnEvict` call it is in the middle of.

## The tests check

- WithClock(fake.Now), WithSweepInterval(time.Millisecond): 100 keys with 1s TTL, fake.Advance(2s); testkit.Eventually Len is 0 without any read having happened
- OnEvict exactly once per key: 200 expired keys, 4 reader goroutines hammering Get while the sweeper runs; Eventually the count reaches 200 and after a further 20 ms it is still exactly 200
- OnEvict is not called for Delete, overwrite with Set, or CompareAndDelete (fake clock advanced an hour, sleep 30 ms, count is 0)
- OnEvict may call m.Set on another key without deadlocking, and that Set lands
- Close stops the sweeper: testkit.NoLeaks sees the goroutine count return to baseline; Close twice and from 8 goroutines all return nil
- After Close every method returns ErrClosed
- Real clock: a store with a 5 ms sweep interval and a 30 ms TTL has Len 0 within 200 ms with no reads

## Uses

- Expiry (built in 1.4 · Decay)

## In the wild

Redis's active expire cycle; patrickmn/go-cache's janitor; every Go server you'll write has a goroutine that needs exactly this Close discipline

## New in Go

time.Ticker · select · goroutine lifecycle · sync.Once · callbacks outside locks · testkit.Eventually and NoLeaks

Standard library: `time`, `sync`, `context`
