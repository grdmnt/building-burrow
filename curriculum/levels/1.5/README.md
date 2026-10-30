# 1.5 · Janitor

> Lazy expiry hides the dead entries. Somebody still has to carry them out.

## Your goal

Start a background goroutine inside `Memory` that wakes up on a timer, removes entries the clock says have expired, and reports each one through an optional `OnEvict` callback. Add `Close()` to stop it cleanly, after which the store refuses every operation with `ErrClosed`.

This is the first goroutine you write yourself, and the point of the level is the lifecycle around it: starting it, telling it to stop, waiting until it has actually stopped, and proving all of that in a test. Every background goroutine you write in Go from here on follows this shape.

## What you're using from before

**Expiry (1.4).** The sweeper decides what is expired with the same alive-at-`now` check your methods already use, and reads `now` from the same injected clock. `WithClock` and the `options` struct behind it get two more options, `WithSweepInterval` and `WithOnEvict`, added the same way:

```go
m := store.NewMemory(
    store.WithClock(clock.Now),
    store.WithSweepInterval(5*time.Millisecond),
    store.WithOnEvict(func(e store.Entry) {
        log.Println("gone", e.Key)
    }),
)
```

The read paths from 1.4 change in one way: when `Get` or another method notices an expired entry, it now removes it and fires `OnEvict`, instead of just pretending it isn't there. The write lock from 1.2 is what decides who removes an entry when the sweeper and a reader both notice it at once.

## What's new here

**Goroutines and the `go` statement.** `go f()` runs `f` on a new goroutine: a lightweight thread managed by the Go runtime, cheap enough to have thousands. The call returns immediately and `f` runs alongside everything else. Closures work too: `go func() { ... }()`. A goroutine has no handle and no result; if you want to know when it is done or make it stop, you build that yourself with channels.

**Channels.** A channel is a typed pipe between goroutines. `ch := make(chan int)`, `ch <- 1` sends, `v := <-ch` receives, and both block until the other side is ready. Closing a channel with `close(ch)` makes every receive on it return immediately, forever, which is why a channel with no values, `chan struct{}`, is the standard way to broadcast "stop" to any number of goroutines:

```go
stop := make(chan struct{})
// in any goroutine: <-stop blocks until close(stop) runs somewhere
close(stop)
```

A plain `bool` can't do this: the goroutine would have to poll it, and sharing it without a lock is a data race.

**`select` and `time.Ticker`.** `select` waits on several channel operations at once and runs whichever is ready first. `time.NewTicker(d)` gives you a channel, `t.C`, that delivers the current time every `d`. Put the two together and you have a loop that does periodic work and can be interrupted. Here it is on a toy heartbeat; a sweeper is the same loop with real work in the tick case:

```go
func heartbeat(interval time.Duration, stop <-chan struct{}) {
    ticker := time.NewTicker(interval)
    defer ticker.Stop()           // release the timer when we leave
    for {
        select {
        case <-ticker.C:
            fmt.Println("tick")
        case <-stop:
            return
        }
    }
}
```

The ticker runs on the real clock: it is how the goroutine wakes up. What it does when awake is decided with the injected clock from `WithClock`, so a test can advance a fake clock by a minute and wait a few real milliseconds for the next sweep.

**Waiting for a goroutine to finish.** Closing `stop` asks the goroutine to leave. To know that it has left, have it close a second channel on its way out (`defer close(done)` as its first line), and have `Close` receive from that: `<-done` returns only once the goroutine is gone. `sync.WaitGroup` is the other tool for this, `wg.Add(1)` before `go`, `defer wg.Done()` inside, `wg.Wait()` to block; use it when there are several goroutines to wait for.

**`sync.Once`.** `Close` may be called twice, or from ten goroutines at once, and `close(stop)` panics on an already-closed channel. `once.Do(f)` runs `f` the first time only; every other caller waits for that first run to finish and then returns. So `Close` is `once.Do(func() { close(stop) })` followed by `<-done`, and every caller gets back after the sweeper has really stopped.

**Callbacks outside the lock.** `OnEvict` is user code, and the tests pass one that calls `m.Set` on the store. If you call it while holding the write lock, that `Set` tries to take the same lock and the program deadlocks forever; Go's mutexes are not re-entrant. So sweep in two phases: under the lock, collect the expired entries into a slice and delete them from the map; after unlocking, call `OnEvict` for each. The same two-phase shape applies to a reader that evicts.

**Exactly once.** The sweeper and four readers may all notice the same expired key within microseconds. Only one of them may fire `OnEvict`. The rule is: whoever actually removes the entry from the map, under the write lock, owns the callback. A reader holding only a read lock that sees an expired entry must upgrade to the write lock and look again, because the sweeper may have removed it in between; if the entry is gone, there is nothing to evict.

**Test helpers for this.** `testkit.Eventually(t, timeout, cond, msg)` polls `cond` until it is true or the timeout passes, which is how you test "the sweeper will get to it soon" without a fixed sleep. `testkit.NoLeaks(t)` counts goroutines when called and again when the test ends, and fails if the number went up: the test that a goroutine really stopped.

## What to do

1. Add `ErrClosed`, `WithSweepInterval` (default 1 s) and `WithOnEvict` as in the Reference. Store the interval and the callback in your options struct next to the clock.
2. Add to `Memory`: a `stop` channel, a `done` channel, and a `sync.Once`. Make the channels in `NewMemory` and start the sweeper there with `go m.sweep()` (or whatever you name it).
3. Write the sweeper: `defer close(done)`, a ticker on the interval, and a `select` loop over the ticker and `stop`. On each tick:
   - read the clock once
   - under the write lock, collect every expired entry into a slice and delete it from the map
   - unlock, then call `OnEvict` once per collected entry, if one was set
4. Write `Close()`: close `stop` inside `once.Do`, then wait on `done`, then return `nil`. Set a flag under the lock so that every other method returns `ErrClosed` afterwards; check it at the top of each method along with the context and key checks.
5. Update the read paths. When a method finds an entry that is expired, take the write lock (if it doesn't already hold it), re-check that the entry is still there and still expired, remove it, and remember it; after unlocking, fire `OnEvict` for it. Then carry on with the "key is absent" behaviour from 1.4.
6. Make sure `Delete`, an overwriting `Set` and `CompareAndDelete` never fire `OnEvict`: it is for expiry only.
7. Run `./burrow test`. If `TestL1_5_OnEvictMayUseTheStore` hangs, a callback is running under the lock. If `TestL1_5_CloseStopsJanitor` reports a leak, the sweeper isn't exiting.

## Example

```go
clock := testkit.NewFakeClock()
m := store.NewMemory(
    store.WithClock(clock.Now),
    store.WithSweepInterval(5*time.Millisecond),
    store.WithOnEvict(func(e store.Entry) {
        fmt.Printf("evicted %s (v%d)\n", e.Key, e.Version)
    }),
)
ctx := context.Background()

m.SetWithTTL(ctx, "a", []byte("1"), time.Second)
m.SetWithTTL(ctx, "b", []byte("2"), time.Second)
m.Set(ctx, "keeper", []byte("3"))

clock.Advance(2 * time.Second)
time.Sleep(20 * time.Millisecond)   // a few sweeps on the real clock
// printed, in some order, each exactly once:
//   evicted a (v1)
//   evicted b (v1)
m.Len()                             // 1

m.Delete(ctx, "keeper")             // no OnEvict: not an expiry

m.Close()                           // returns once the sweeper exited
m.Close()                           // nil again, nothing happens
_, err := m.Get(ctx, "keeper")      // store.ErrClosed
```

And the reader-evicts case, with no sweeper fast enough to matter:

```go
m := store.NewMemory(store.WithClock(clock.Now),
    store.WithSweepInterval(time.Hour),
    store.WithOnEvict(func(e store.Entry) {
        fmt.Println("gone:", e.Key)
    }))
m.SetWithTTL(ctx, "k", []byte("v"), time.Second)
clock.Advance(time.Minute)
m.Get(ctx, "k")      // ErrNotFound, and prints "gone: k" once
m.Get(ctx, "k")      // ErrNotFound, prints nothing: already evicted
```

## Done when

- [ ] 100 expired keys are evicted by the sweeper alone, with nobody reading them, and each `OnEvict` fires exactly once
- [ ] `OnEvict` receives the full `Entry`: key, value, version and `ExpiresAt`
- [ ] With readers hammering 200 expired keys while the sweeper runs, `OnEvict` still fires exactly 200 times
- [ ] `Delete`, overwrite with `Set` and `CompareAndDelete` never fire `OnEvict`
- [ ] An `OnEvict` that calls back into the store doesn't deadlock, and its write lands
- [ ] On the real clock, a 30 ms TTL is swept within 2 s with no reads
- [ ] `Close` returns within 2 s, leaves no goroutine behind, and returns `nil` when called twice or from 10 goroutines at once
- [ ] After `Close`, every method returns `ErrClosed`

## Reference

```go
// store/memory.go additions
var ErrClosed = errors.New("store: closed")

func WithSweepInterval(d time.Duration) Option // default 1s
func WithOnEvict(fn func(e Entry)) Option     // called once per key
                                              // removed because it
                                              // expired,
                                              // whether the sweeper or
                                              // a reader noticed first.
                                              // Never called for
                                              // Delete, overwrite or
                                              // CompareAndDelete.

func (m *Memory) Close() error // stops the sweeper and waits for it;
                               // safe to call twice and concurrently;
                               // afterwards every operation returns
                               // ErrClosed

// The sweeper uses a time.Ticker on the real clock for its wake-ups and
// the injected clock (WithClock) to decide what has expired, so tests
// advance a FakeClock and wait with testkit.Eventually.
```

Decisions the tests rely on:

- A reader that notices an expired key removes it and fires `OnEvict` itself, so evictions happen with the sweeper stopped too. Whoever removes the entry under the write lock owns the callback.
- The ticker runs on the real clock; what counts as expired comes from `WithClock`.
- `Close` waits for the sweeper, including any `OnEvict` call it is in the middle of.

## The tests check

- WithClock(fake.Now), WithSweepInterval(5ms): 100 keys with 1s TTL, Advance(2s); testkit.Eventually OnEvict has fired for all 100 without any read, and 30 ms of further sweeps add no calls
- OnEvict receives the expired Entry with its key, value, version and ExpiresAt
- OnEvict exactly once per key: 200 expired keys, 4 reader goroutines hammering Get and Touch while the sweeper runs; Eventually the count reaches 200 and 20 ms later it is still exactly 200
- OnEvict is not called for Delete, overwrite with Set, or CompareAndDelete (fake clock advanced an hour, 30 ms of sweeps, count is 0)
- OnEvict may call m.Set on another key without deadlocking, and that Set lands
- Real clock: WithSweepInterval(5ms) and a 30 ms TTL, Len reaches 0 within 2 s with no reads
- Close stops the sweeper: it returns within 2 s (testkit.Within) and testkit.NoLeaks sees the goroutine count return to baseline; Close from 10 goroutines at once and a second Close all return nil
- After Close every method (Get, Set, Delete, Keys, CompareAndSwap, CompareAndDelete, SetWithTTL, Touch) returns ErrClosed

## Try it

```bash
./burrow watch
go test -race -v -run TestL1_5_OnEvictMayUseTheStore ./store/ # deadlock
go test -race -v -run TestL1_5_CloseStopsJanitor ./store/     # leak
```

To see what a leak report looks like, comment out the `case <-stop:` branch and run the second command.

## In the wild

Redis's active expire cycle; patrickmn/go-cache's janitor; every Go server you'll write has a goroutine that needs exactly this Close discipline

## New in Go

goroutines · the go statement · channels and close · select · time.Ticker · done channels · sync.WaitGroup · goroutine lifecycle · sync.Once · callbacks outside locks · testkit.Eventually and NoLeaks

Standard library: `time`, `sync`, `context`
