# 1.4 · Decay Timer

Keys that expire, a background janitor that cleans them up, and a clean way to stop it.

## Build

```go
var (
	ErrClosed     = errors.New("store closed")
	ErrInvalidTTL = errors.New("invalid ttl")
)

type Option func(*options) // your unexported options struct

func WithClock(now func() time.Time) Option     // default time.Now
func WithSweepInterval(d time.Duration) Option // default 1s
func WithOnEvict(fn func(key string)) Option   // called once per expired key removed

func NewMemory(opts ...Option) *Memory // existing NewMemory() calls still compile

func (m *Memory) SetWithTTL(ctx context.Context, key string, value []byte, ttl time.Duration) error
func (m *Memory) TTL(ctx context.Context, key string) (time.Duration, error)
func (m *Memory) Close() error
```

- A key **expires when now ≥ expiry**. Expired keys are invisible everywhere at once: `Get`, `Keys`, `Len`, `Delete`, `TTL`, `Update`.
- `TTL` returns the time remaining, `0` for a key with no expiry, `ErrNotFound` for missing or expired keys.
- `ttl <= 0` is `ErrInvalidTTL`. A plain `Set` clears any TTL; `SetWithTTL` again restarts the countdown.
- A janitor goroutine sweeps expired keys every sweep interval, even if nobody reads them.
- `Close` stops the janitor and waits for it to exit. It's safe to call twice, or from many goroutines. After `Close`, operations return `ErrClosed`.

## The tests check

- Expiry at exactly the TTL with a fake clock (`internal/testkit.FakeClock`), 1ns before and at the instant.
- The janitor evicts 100 expired keys with no reads, and **each eviction fires `OnEvict` exactly once**, even when readers notice the expiry at the same moment.
- `OnEvict` is for expiry only: not for `Delete` or overwrite.
- `OnEvict` may call back into the store without deadlocking.
- A 30ms TTL on the real clock expires.
- After `Close`, no goroutines are left running (the test counts them).

## New in Go

`time.Time` and `Duration` · `time.Ticker` · `select` · functional options · goroutine lifecycles · injectable clocks · `sync.Once`
