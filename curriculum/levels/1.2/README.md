# 1.2 · Arbiter

> When two parties want the same slot at the same moment, somebody has to decide who goes first.

## Your goal

Make `Memory` safe to use from many goroutines at once. A goroutine is Go's lightweight thread, and a Go server handles every connection on its own goroutine, so a store that only works single-threaded is useless the moment a second client connects. Nothing in the public API changes. What changes is the inside of `Memory`: every method takes a lock around its use of the map, so two calls can never read and write the map at the same instant.

The level also introduces the race detector, the tool that finds exactly this kind of bug for you, and the test runner has it switched on from here forward.

## What you're using from before

**Memory store (1.1).** The map, the methods and the copy-in, copy-out rules all stay as they are. You are adding one field to the struct and two lines to each method. The provided suite `storetest.RunConcurrent` hammers your existing `Set`, `Get`, `Delete` and `Keys` from dozens of goroutines and expects the same answers 1.1's tests did.

## What's new here

**Goroutines, briefly.** `go f()` starts `f` running concurrently and returns immediately. The tests in this level start goroutines that call your methods; you don't write any goroutines yourself yet. What you need to know is that two goroutines really do run at the same time on different CPU cores, and nothing coordinates them unless you do.

**A data race.** When two goroutines touch the same memory at the same time and at least one of them writes, the result is undefined. For a Go map it is worse than wrong answers: the runtime detects concurrent map writes and crashes the whole program with `fatal error: concurrent map writes`. Even `Len()` reading `len(m.data)` while another goroutine inserts is a race. Being "mostly fine in practice" doesn't count; a race is a bug whether or not it has bitten yet.

**The race detector.** `go test -race` builds the test binary with instrumentation that records every memory access and reports when two goroutines accessed the same address without something ordering them. It prints `WARNING: DATA RACE` with both stack traces, and the test fails. It only catches races that actually happen during the run, which is why the tests hammer the store hard. `./burrow test` always passes `-race`; the first test in this level, `TestL1_2_RaceDetectorOn`, only checks that the flag is there.

**`sync.RWMutex`.** A mutex is a lock: `Lock()` waits until no one else holds it, `Unlock()` releases it. A read-write mutex adds `RLock()`/`RUnlock()`, which many readers can hold at the same time as long as no writer holds `Lock()`. The zero value is ready to use, so a struct field needs no initialisation. Lock inside the method, never make callers do it, and use `defer` so the unlock runs on every return path:

```go
type Counter struct {
    mu     sync.RWMutex
    counts map[string]int
}

func (c *Counter) Add(name string) {
    c.mu.Lock()
    defer c.mu.Unlock()
    c.counts[name]++
}

func (c *Counter) Get(name string) int {
    c.mu.RLock()
    defer c.mu.RUnlock()
    return c.counts[name]
}
```

Two rules that bite newcomers: never copy a struct that contains a mutex (pass `*Counter`, not `Counter`; `go vet` warns about this), and a goroutine that locks a mutex it already holds deadlocks, because Go's mutexes are not re-entrant.

**Check-then-act.** The lock has to cover the whole decision, not just the map access. `Delete` looks the key up, then deletes it, and must hold the write lock across both; if it took a read lock to look and a write lock to delete, another goroutine could delete the key in between and both would think they succeeded. The same goes for anything you copy out: copy the value while you still hold the lock, or the next writer can change it under you.

**Channels as the other option.** Go's alternative to locks is to give the map to one goroutine and have every method send it a request over a channel, so the map is only ever touched from one place. That design passes this level too (it is the OTP actor model, if you know Elixir). The mutex is the idiomatic answer for a data structure like this and the shorter path, so take it first; the channel version is a good exercise once the mutex one passes.

## What to do

1. Add a `sync.RWMutex` field to `Memory`. By convention it sits right above the fields it protects and is called `mu`.
2. In `Set` and `Delete`, take the write lock (`Lock`) after the context and key checks and before touching the map. Release it with `defer`.
3. In `Get`, `Keys` and `Len`, take the read lock (`RLock`) the same way.
4. Make sure every copy happens under the lock: `Get` clones the value before unlocking, and `Keys` builds its result slice before unlocking.
5. Run `./burrow test`. Then, to see the detector work, comment out one `Lock`/`Unlock` pair and run it again.

## Example

What the race detector prints when `Set` writes the map without a lock while a `Get` reads it (shortened):

```bash
$ ./burrow test
==================
WARNING: DATA RACE
Write at 0x00c0000a4180 by goroutine 9:
  runtime.mapassign_faststr()
  burrow/store.(*Memory).Set()
      /home/you/burrow/store/memory.go:41 +0x1c4
  ...
Previous read at 0x00c0000a4180 by goroutine 8:
  runtime.mapaccess2_faststr()
  burrow/store.(*Memory).Get()
      /home/you/burrow/store/memory.go:28 +0x9c
  ...
==================
--- FAIL: TestL1_2_ConcurrentConformance (0.14s)
    testing.go:1490: race detected during execution of test
```

The two stack traces are the two sides of the race. With the lock in place the same run is quiet:

```bash
$ ./burrow test
ok      burrow/store    1.872s
```

## Done when

- [ ] `./burrow test` reports no data races with 16 goroutines writing disjoint keys and 24 goroutines doing mixed operations on 32 hot keys
- [ ] `Len` and `Keys` can be called while a writer is busy and always see a consistent store
- [ ] `Get` never returns a torn value: it is always exactly one of the values that was written
- [ ] Separate `Memory` instances used from separate goroutines share nothing
- [ ] Removing a lock makes the race detector complain, so you have seen it work once

## Reference

```go
// No new exported API. Memory must be safe for concurrent use by any
// number of goroutines. Two valid designs, both pass:
//   (a) a sync.RWMutex inside Memory, locked inside methods, never by
//       callers;
//   (b) one goroutine owns the map and methods send it requests over a
//       channel (an OTP-style actor).
// This level installs store/storetest.RunConcurrent.
```

The goroutines in this level belong to the tests; you supply the lock.

## The tests check

- Runs only under the race detector (testkit.RequireRace)
- storetest.RunConcurrent: 16 goroutines writing 200 disjoint keys each, every final value is right; then 24 goroutines doing 600 mixed Set/Get/Delete/Keys ops over 32 hot keys
- Len and Keys called 2000 times while a writer sets and deletes over 500 keys: Len stays within 0..500 and Keys never errors
- Readers only ever see whole values: a writer alternates two 4 KiB values 3000 times while readers check every read is exactly one of them
- 8 stores used from 8 goroutines at once hold 200 keys each and share nothing

## Try it

```bash
./burrow test                                  # always runs with -race
go test -race -run TestL1_2 -count=5 ./store/  # races depend on timing
```

Remove your lock and run `./burrow test` to watch the race detector catch you.

## In the wild

go test -race (ThreadSanitizer); sync.Map exists for a narrow case (append-only caches) and is not what you want here

## New in Go

goroutines (as callers) · data races · the race detector · sync.RWMutex · defer for unlock · check-then-act · channels as an alternative to locks

Standard library: `sync`
