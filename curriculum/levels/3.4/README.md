# 3.4 · Overclock

fsync is slow. Let many concurrent appends share one, without ever acknowledging data that isn't on disk.

## Build

```go
// WithSyncFunc replaces the fsync done after appends (default: (*os.File).Sync).
func WithSyncFunc(fn func(f *os.File) error) Option

// Syncs reports how many times the sync function has run for appends.
func (l *Log) Syncs() uint64
```

Group commit: one writer goroutine collects waiting appends, writes them together, syncs once, then wakes every waiter.

## The tests check

- Sequential appends: each one returns only after a sync covering its bytes. 30 appends, 30 syncs.
- 100 goroutines × 20 appends with a 2ms sync: at most 500 syncs and under 2 seconds. One sync per append would take 4.
- **A failed sync is sticky:** every append in the failed batch, and every append after, returns that error. You can't know what reached the disk.
- `Close` with appends in flight: nothing hangs, appends either succeed or return `ErrClosed`, and everything acknowledged is in the log afterwards. No goroutines leak.
- Two benchmarks to compare: `go test -bench=L3_4 -run=^$ ./wal`.

## New in Go

channels of request structs · reply channels · batching with a non-blocking drain · benchmarks and `b.Loop` · profiling with `-cpuprofile`
