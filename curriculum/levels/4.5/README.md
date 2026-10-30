# 4.5 · Overclock

fsync is slow. Let many concurrent appends share one, without ever acknowledging data that isn't on disk. Measure before and after.

## Build

```go
// wal/wal.go additions
// WithSyncFunc replaces the fsync done after appends (default: (*os.File).Sync).
func WithSyncFunc(fn func(f *os.File) error) Option

// Syncs reports how many times the sync function has run for appends. It is called from
// other goroutines while the writer is appending, so the counter is an atomic.Uint64.
func (l *Log) Syncs() uint64

// Append's contract is unchanged: it returns once its record is on disk. Internally a
// single writer goroutine drains waiting appends, encodes them with wire.AppendRecord into
// one buffer, writes once, syncs once, and wakes every waiter. Once a sync fails, that error
// is returned by every append in the batch and every append after it.
```

- `WithSyncFunc` and `Syncs()` on an `atomic.Uint64`
- A writer goroutine with a request channel and per-request reply channels
- Sticky sync failures; Close that drains or fails in-flight appends and stops the goroutine
- A benchmark of your own in `wal/` (not `BenchmarkL…`) using `b.Loop`, run before and after, and a CPU profile you look at

The channel rules this design depends on:

- Every channel has one owner, and only the owner closes it. Here `Append` sends and the `Log` (in `Close`) closes the request channel; the writer goroutine only receives. Closing from the sender side is what makes `for req := range requests` in the writer terminate cleanly; a send on a closed channel panics, which is why `Close` must also stop new appends (a mutex or a closed `done` channel checked first) before closing the channel.
- `v, ok := <-ch` reports a closed channel with `ok == false`; `for range ch` stops there by itself.
- A nil channel blocks forever in `select`. Setting a channel variable to nil is how you switch a `case` off without restructuring the loop: a writer that wants to stop accepting while it finishes a batch sets `requests = nil` and the select ignores it.
- Each reply channel is buffered (size 1) so the writer never blocks on a waiter that has given up.

Decisions the tests rely on:

- A batch holds at most 256 records or 4 MiB, whichever comes first.
- `Syncs` counts every call of the sync function, including ones that failed. It is safe to call from any goroutine while appends are in flight: an `atomic.Uint64` (`Add` in the writer, `Load` in `Syncs`), not a plain `uint64` and not the mutex that guards the file.
- `Close` on a poisoned log returns nil; the sticky error is only for appends.
- Your benchmark lives in `wal/`, its name does not start with `BenchmarkL`, and it runs cleanly with `-benchtime=10x` (so it must not depend on a minimum number of iterations or leave files behind outside `b.TempDir()`). `for b.Loop() { ... }` is the Go 1.24 shape; setup before the loop is excluded from the timing automatically.

## The tests check

- Sequential: 30 appends, 30 syncs, and each append returns only after a sync covering its bytes (the slow sync records the file size at each call)
- 100 goroutines x 20 appends with a 2 ms sync: at most 500 syncs, under 2 s, all 2000 records present, and Syncs() equals the number of times the sync function ran
- A sync that fails from its 5th call on: 6 of 10 sequential appends fail with that error (every one after the first failure); when every sync fails, no append of 50 reports success; records acknowledged before the failure are still readable
- Close with appends in flight: nothing hangs, each append either succeeds or returns ErrClosed, everything acknowledged is on disk afterwards, no goroutine leaks
- Without WithSyncFunc the log still syncs (Syncs() >= 1)
- 200 batched appends from 20 goroutines are still 200 separate records: Len is 200, Truncate(150) works, and tearing the last record leaves 149 after reopen
- Two benchmarks, serial and parallel, run with -bench=L4_5 -run=^$
- TestL4_5_YourBenchmark: a benchmark of yours exists in wal/ (name not starting with BenchmarkL) and runs cleanly with -benchtime=10x
- TestL4_5_SyncsConcurrentRead: Syncs() is called from several goroutines while appends run; under -race this is clean only if the counter is atomic

## Try it

```bash
go test -bench=L4_5 -run='^$' -cpuprofile cpu.out ./wal && go tool pprof -top cpu.out | head -20
go test -bench=. -run='^$' -benchmem -count=3 ./wal   # yours too; -count for noise
```

## Uses

- CRC guard (built in 4.3 · Parity Bits)

## In the wild

Postgres commit_delay / group commit, InnoDB's log group commit, etcd's batched fsync; errgroup is not this, this is a classic actor loop

## New in Go

channels of request structs · reply channels · channel ownership and close · for range over a channel · nil channels in select · non-blocking drain with select/default · backpressure · sync/atomic · writing a benchmark with b.Loop · -cpuprofile and go tool pprof

Standard library: `sync`, `sync/atomic`, `testing`, `runtime/pprof`, `os`
