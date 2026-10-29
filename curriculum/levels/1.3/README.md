# 1.3 · Arbiter

Make the store safe for many goroutines, and add an atomic read-modify-write.

## Build

```go
// Add to Memory:
// Update runs fn with the current value and stores what it returns, atomically.
// For a missing key fn gets (nil, false). If fn returns an error, nothing changes
// and Update returns that error.
func (m *Memory) Update(ctx context.Context, key string,
	fn func(old []byte, exists bool) ([]byte, error)) error
```

Protect Memory with a `sync.RWMutex`. Readers can share; writers can't.

This level adds `storetest.RunConcurrent` to the provided suite.

## The tests check

- **Runs only under the race detector.** `burrow test` always passes `-race`. On macOS that needs the Xcode command line tools.
- Disjoint writers, a mixed hammer test (Set/Get/Delete/Keys over 32 hot keys), and `Len`/`Keys` while writing.
- No torn values: readers only ever see one whole write.
- 50 goroutines × 200 increments through `Update` end at exactly 10,000. A Get-then-Set would lose updates.
- `Update` hands `fn` a **copy**: if `fn` scribbles on `old` and returns an error, the stored value is unchanged.
- The slice `fn` returns is copied too.
- An invalid key fails without calling `fn`.

## Try it

Remove your lock and run `burrow test` to watch the race detector catch you.

## New in Go

goroutines · `sync.WaitGroup` · `sync.RWMutex` · data races · the race detector · check-then-act bugs
