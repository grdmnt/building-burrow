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

## The tests check

- Runs only under the race detector (testkit.RequireRace)
- 32 goroutines writing disjoint keys 1000 times each: every final value is right
- Mixed hammer: 16 goroutines doing Set/Get/Delete/Keys over 32 hot keys for 200 ms; no panics, no races
- Len and Keys called continuously while 8 writers run: Len never exceeds the key count and Keys stays sorted
- Readers only ever see whole values: a writer alternates two 64 KiB values while readers check every read is exactly one of them

## Try it

Remove your lock and run `burrow test` to watch the race detector catch you.

## Uses

- Memory store (built in 1.1 · Register File)

## In the wild

go test -race (ThreadSanitizer); sync.Map exists for a narrow case (append-only caches) and is not what you want here

## New in Go

goroutines · sync.WaitGroup · sync.RWMutex · data races · the race detector · check-then-act · channels as an alternative to locks

Standard library: `sync`, `sync/atomic`
