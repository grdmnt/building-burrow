# 4.5 · Overclock

> One trip to the disk per write is the speed limit. Carpooling gets everyone there in one trip.

## Your goal

`Append` is correct and slow: every call does its own fsync, and an fsync costs milliseconds. With a hundred goroutines appending at once, that is a hundred fsyncs in a row, one after another behind the mutex. *Group commit* is the fix every database uses. One writer goroutine collects whatever appends are waiting, writes them to the file in a single `Write`, syncs once, and then tells all of them "done". The contract of `Append` doesn't change at all: it still returns only once its record is on disk. It just shares the trip.

To see that it worked, you also write a benchmark and look at a CPU profile, before and after.

## What you're using from before

**CRC guard** (4.3 · Parity Bits). The log you are changing. Each record in a batch is still its own checksummed wire record, so `Len`, `Truncate` and the torn-tail repair keep working on batched logs. The batch is built with `wire.AppendRecord` (from 2.1, under 4.1's log), which appends an encoded record to a byte slice instead of writing it, so one buffer can hold the whole batch for one `Write`:

```go
var batch []byte
for _, req := range reqs {
	payload := crcAndRec(req.rec) // crc32 | rec, as in 4.3
	batch, _ = wire.AppendRecord(batch, payload)
}
l.file.Write(batch) // one write for the whole batch
```

## What's new here

**A request struct on a channel.** Instead of doing the work itself, `Append` describes it and sends the description to a goroutine that does the work. The description carries the record and a channel to answer on:

```go
type request struct {
	rec  []byte
	done chan error
}

func (l *Log) Append(rec []byte) error {
	req := request{rec: rec, done: make(chan error, 1)}
	l.requests <- req   // hand it to the writer
	return <-req.done   // block until the writer answers
}
```

The reply channel has a buffer of one, so the writer can drop its answer in and move on even if the caller has somehow stopped listening.

**The writer loop: block for one, drain the rest.** The writer waits for a request (blocking), then grabs every other request that is *already* waiting without blocking, up to a limit. A `select` with a `default` case is the non-blocking receive:

```go
for req := range l.requests { // block for the first
	batch := []request{req}
collect:
	for len(batch) < 256 { // and the same for the 4 MiB cap
		select {
		case more := <-l.requests: // one is waiting: take it
			batch = append(batch, more)
		default: // none waiting: stop collecting
			// a labelled break leaves the for, not the select
			break collect
		}
	}
	// encode all, write once, sync once, answer everyone
}
```

(A plain `break` inside a `select` only leaves the `select`; the label makes it leave the loop.)

Under light load every batch is one record and nothing changes. Under heavy load the batch fills while the previous sync is running, and one sync covers them all. That is the whole trick.

**`for range` over a channel, and who closes it.** `for req := range ch` receives until the channel is closed, then the loop ends. Closing is how the writer learns it is time to go. The rules that keep this safe: a channel has one owner, and only the owner closes it. Here `Append` sends, the `Log` owns the channel and closes it in `Close`, and the writer only receives. A send on a closed channel panics, so `Close` must first stop new `Append` calls (set a closed flag under a mutex, or close a separate `done` channel that `Append` checks) and only then close the request channel. Then it waits for the writer to finish its last batch.

**Nil channels in `select`.** Receiving from a nil channel blocks forever, which in a `select` means that case is simply never chosen. Setting a channel variable to nil is the way to switch a case off without rewriting the loop. If your writer has a `select` with several cases and wants to stop taking new requests while it finishes something, `requests = nil` does it.

**Backpressure.** The batch has a cap (256 records or 4 MiB), and the request channel is unbuffered or small. When the writer is busy, `Append` callers wait at the send. That is correct: a caller that wants durability has to wait for the disk, and the queue can't grow without bound.

**Sticky failures.** If a sync fails, you don't know what reached the disk. Every append in that batch gets the error, and so does every append after it: remember the error and answer with it without writing anything more. `Close` on a poisoned log still returns nil; the error is for appends.

**`sync/atomic`.** `Syncs()` is read by other goroutines while the writer increments it. A plain `uint64` would be a data race (the race detector will tell you). `atomic.Uint64` has `Add` and `Load` methods that are safe without a lock:

```go
var syncs atomic.Uint64
syncs.Add(1)       // in the writer, after each sync
n := syncs.Load()  // from anywhere
```

**Benchmarks and profiles.** A benchmark is a function `BenchmarkXxx(b *testing.B)` whose body loops with `for b.Loop() { ... }` (Go 1.24). The runtime decides how many iterations to run and reports time per operation; setup before the loop is not timed:

```go
func BenchmarkAppend(b *testing.B) {
	l, _ := wal.Open(filepath.Join(b.TempDir(), "wal.log"))
	defer l.Close()
	rec := make([]byte, 128)
	for b.Loop() {
		l.Append(rec)
	}
}
```

Run it with `go test -bench=Append -run='^$' ./wal` (`-run='^$'` skips the tests). Add `-benchmem` for allocations and `-cpuprofile cpu.out` to record where the time goes; `go tool pprof -top cpu.out` lists the hottest functions. Before group commit, `syscall.Fsync` dominates. After it, under `RunParallel`, it should shrink.

## What to do

1. Add `WithSyncFunc` and the `Syncs` method (an `atomic.Uint64` on the `Log`). The default sync function is `(*os.File).Sync`.
2. Add the `request` type, a request channel, a `done`/closed signal and a `sync.WaitGroup` (or a channel) to know when the writer has exited. Start the writer goroutine in `Open`.
3. Rewrite `Append`: size check and closed check as before, then send a request and wait on its `done` channel.
4. Write the writer loop:
   - Block for one request, drain more without blocking until 256 records or 4 MiB.
   - If a sticky error is set, answer every request in the batch with it and continue.
   - Encode each record as `crc32 | rec` with `wire.AppendRecord` into one buffer, `Write` it once, call the sync function once (count it, whether or not it failed).
   - On a sync error, remember it. Answer every `done` channel with the result. On success, push each record's end offset onto the index.
5. Rewrite `Close`: mark closed so new appends return `ErrClosed`, close the request channel, wait for the writer to exit, close the file. A second `Close` returns nil.
6. Make sure `Len`, `Truncate` and `Records` still agree with the writer (they share the file and the index; a mutex between them and the writer's apply step is the simplest way).
7. Write a benchmark of your own in `wal/` (name not starting with `BenchmarkL`) using `b.Loop` and `b.TempDir()`. Run it before and after your change, with `-benchmem` and `-cpuprofile`, and look at the profile.
8. Run `gofmt -l .` and `go vet ./...` until both print nothing.

## Example

```go
l, _ := wal.Open(path)
l.Append([]byte("a")) // nothing else waiting: a batch of one, one sync
l.Append([]byte("b")) // same
l.Syncs()             // 2

var wg sync.WaitGroup
for i := range 100 {
	wg.Add(1)
	go func() {
		defer wg.Done()
		// returns once its batch is synced
		l.Append([]byte(fmt.Sprint(i)))
	}()
}
wg.Wait()
l.Syncs() // far fewer than 102: the 100 shared a handful of syncs
l.Len()   // 102, every record separate and checksummed
```

And the shape of the benchmark output before and after (illustrative numbers; yours depend on the disk):

```text
$ go test -bench=L4_5 -run='^$' ./wal
BenchmarkL4_5_AppendSerial-10      420    2.6 ms/op   # before
BenchmarkL4_5_AppendParallel-10    380    2.9 ms/op
BenchmarkL4_5_AppendSerial-10      410    2.6 ms/op   # after: same
BenchmarkL4_5_AppendParallel-10   5200    0.21 ms/op  # after: ~14x
```

Serial appends can't be batched, so that number doesn't move. Parallel ones share the sync, and that is the number that should drop by an order of magnitude.

## Done when

- [ ] 30 appends one after another still cost 30 syncs, and each returns only after a sync that covers its bytes
- [ ] 100 goroutines doing 20 appends each with a 2 ms sync finish in under 2 s with at most 500 syncs, all 2000 records present, and `Syncs()` equal to the real count
- [ ] Once a sync fails, that append and every later one return the error; records acknowledged before it are still readable
- [ ] `Close` with appends in flight doesn't hang: each append either succeeded (and is on disk) or returns `ErrClosed`; no goroutine is left behind
- [ ] Without `WithSyncFunc` the log still syncs
- [ ] 200 batched appends are 200 records: `Len` is 200, `Truncate(150)` works, tearing the last record leaves 149
- [ ] `Syncs()` can be read from several goroutines during appends without a race
- [ ] A benchmark of yours exists in `wal/`, uses `b.Loop`, and runs cleanly with `-benchtime=10x`
- [ ] `gofmt -l .` and `go vet ./...` print nothing

## Reference

The exact contract the tests hold you to.

```go
// wal/wal.go additions
// WithSyncFunc replaces the fsync done after appends (default:
// (*os.File).Sync).
func WithSyncFunc(fn func(f *os.File) error) Option

// Syncs reports how many times the sync function has run for appends.
// It is called from other goroutines while the writer is appending, so
// the counter is an atomic.Uint64.
func (l *Log) Syncs() uint64

// Append's contract is unchanged: it returns once its record is on
// disk. Internally a single writer goroutine drains waiting appends,
// encodes them with wire.AppendRecord into one buffer, writes once,
// syncs once, and wakes every waiter. Once a sync fails, that error is
// returned by every append in the batch and every append after it.
```

| Rule | Value |
|---|---|
| Batch limit | 256 records or 4 MiB, whichever comes first |
| `Syncs()` | every call of the sync function, failed ones included |
| After a failed sync | every append returns that error; nothing more is written |
| `Close` on a poisoned log | returns nil |
| Your benchmark | in `wal/`, name not starting with `BenchmarkL`, clean under `-benchtime=10x`, files only under `b.TempDir()` |

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
./burrow watch
go test -bench=L4_5 -run='^$' -cpuprofile cpu.out ./wal
go tool pprof -top cpu.out | head -20
go test -bench=. -run='^$' -benchmem -count=3 ./wal   # yours too
```

Run the benchmark line once before you touch the code and save the output; the comparison is the point.

## In the wild

Postgres commit_delay / group commit, InnoDB's log group commit, etcd's batched fsync; this is a classic actor loop.

## New in Go

channels of request structs · reply channels · channel ownership and close · for range over a channel · nil channels in select · non-blocking drain with select/default · backpressure · sync/atomic · writing a benchmark with b.Loop · -cpuprofile and go tool pprof

Standard library: `sync`, `sync/atomic`, `testing`, `runtime/pprof`, `os`
