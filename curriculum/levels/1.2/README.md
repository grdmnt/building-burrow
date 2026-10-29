# 1.2 · Bus Interface

Define the contract every storage engine in Burrow will satisfy, and meet the curriculum's conformance suite.

## Build

```go
// store/store.go
type Store interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte) error
	Delete(ctx context.Context, key string) error
	Keys(ctx context.Context, prefix string) ([]string, error)
}

// Add to Memory:
func (m *Memory) Keys(ctx context.Context, prefix string) ([]string, error)
```

`Keys` returns every key starting with `prefix`, **sorted by byte order**. An empty prefix means all keys. No matches means an empty (or nil) slice and a nil error.

This level installs a provided package, `store/storetest`. It's a conformance suite you'll run against every engine you build (the durable store in Chapter 3 runs it too). Don't edit it; `burrow done` checks it's unchanged.

## The tests check

- Compile-time: `*Memory` satisfies `store.Store`, and `store.Store` has every method the suite needs.
- The full `storetest.Run` suite against `NewMemory()`.
- `Keys` with prefixes that are keys themselves, prefixes of prefixes, Unicode, no matches.
- `Keys` returns a snapshot: later writes don't change a slice you already returned.
- 1000 keys inserted in reverse come back sorted.

## New in Go

interfaces (satisfied implicitly) · compile-time assertions `var _ I = (*T)(nil)` · the `slices` package · test helpers with `t.Helper()`
