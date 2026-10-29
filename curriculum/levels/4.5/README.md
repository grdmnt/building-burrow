# 4.5 · Overclock

fsync is slow. Let many concurrent appends share one, without ever acknowledging data that isn't on disk. Measure before and after.

## Build

```go
// wal/wal.go additions
// WithSyncFunc replaces the fsync done after appends (default: (*os.File).Sync).
func WithSyncFunc(fn func(f *os.File) error) Option

// Syncs reports how many times the sync function has run for appends.
func (l *Log) Syncs() uint64

// Append's contract is unchanged: it returns once its record is on disk. Internally a
// single writer goroutine drains waiting appends, encodes them with wire.AppendRecord into
// one buffer, writes once, syncs once, and wakes every waiter. Once a sync fails, that error
// is returned by every append in the batch and every append after it.
```

- `WithSyncFunc` and `Syncs()`
- A writer goroutine with a request channel and per-request reply channels
- Sticky sync failures; Close that drains or fails in-flight appends and stops the goroutine
- `BenchmarkL4_5` before/after and a CPU profile you look at

Decisions the tests rely on:

- A batch holds at most 256 records or 4 MiB, whichever comes first.
- `Syncs` counts every call of the sync function, including ones that failed.
- `Close` on a poisoned log returns nil; the sticky error is only for appends.

## The tests check

- Sequential: 30 appends, 30 syncs, and each append returns only after a sync covering its bytes (the slow sync records the file size at each call)
- 100 goroutines x 20 appends with a 2 ms sync: at most 500 syncs, under 2 s, all 2000 records present, and Syncs() equals the number of times the sync function ran
- A sync that fails from its 5th call on: 6 of 10 sequential appends fail with that error (every one after the first failure); when every sync fails, no append of 50 reports success; records acknowledged before the failure are still readable
- Close with appends in flight: nothing hangs, each append either succeeds or returns ErrClosed, everything acknowledged is on disk afterwards, no goroutine leaks
- Without WithSyncFunc the log still syncs (Syncs() >= 1)
- 200 batched appends from 20 goroutines are still 200 separate records: Len is 200, Truncate(150) works, and tearing the last record leaves 149 after reopen
- Two benchmarks, serial and parallel, run with -bench=L4_5 -run=^$

## Try it

```bash
go test -bench=L4_5 -run='^$' -cpuprofile cpu.out ./wal && go tool pprof -top cpu.out | head -20
```

## Uses

- CRC guard (built in 4.3 · Parity Bits)

## In the wild

Postgres commit_delay / group commit, InnoDB's log group commit, etcd's batched fsync; errgroup is not this, this is a classic actor loop

## New in Go

channels of request structs · reply channels · non-blocking drain with select/default · backpressure · benchmarks and b.Loop · -cpuprofile and go tool pprof

Standard library: `sync`, `testing`, `runtime/pprof`, `os`
