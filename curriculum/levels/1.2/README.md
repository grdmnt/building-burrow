# 1.2 · Arbiter

Make the store safe for many goroutines. The network server in chapter 3 will have dozens of connections hitting one store; this level is where the race detector teaches you what that means.

## Build

```go
// No new exported API. Memory must be safe for concurrent use by any number of goroutines.
// Two valid designs, both pass:
//   (a) a sync.RWMutex inside Memory, locked inside methods, never by callers;
//   (b) one goroutine owns the map and methods send it requests over a channel (an OTP-style actor).
// This level installs store/storetest.RunConcurrent.
```

- Make every Memory method safe under concurrent use
- Pass `storetest.RunConcurrent` under `-race`
- Try removing the protection and watch the race detector catch you

The goroutines in this level belong to the tests; you supply the lock. Your first goroutine of your own, and the discipline of stopping it, is 1.5. Design (b) above gets you there early if you want it.

## The tests check

- Runs only under the race detector (testkit.RequireRace)
- storetest.RunConcurrent: 16 goroutines writing 200 disjoint keys each, every final value is right; then 24 goroutines doing 600 mixed Set/Get/Delete/Keys ops over 32 hot keys
- Len and Keys called 2000 times while a writer sets and deletes over 500 keys: Len stays within 0..500 and Keys never errors
- Readers only ever see whole values: a writer alternates two 4 KiB values 3000 times while readers check every read is exactly one of them
- 8 stores used from 8 goroutines at once hold 200 keys each and share nothing

## Try it

Remove your lock and run `burrow test` to watch the race detector catch you.

## Uses

- Memory store (built in 1.1 · Register File)

## In the wild

go test -race (ThreadSanitizer); sync.Map exists for a narrow case (append-only caches) and is not what you want here

## New in Go

sync.RWMutex · data races · the race detector · check-then-act · channels as an alternative to locks

Standard library: `sync`
