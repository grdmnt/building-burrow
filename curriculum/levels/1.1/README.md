# 1.1 · Register File

An in-memory key-value store behind a small interface. Small, but it has to be correct about missing keys, empty values and shared memory, and the interface it defines is the contract three more engines will satisfy: the network client (3.3), the durable store (4.2) and the Raft state machine (10.3).

## Build

```go
// store/store.go
package store

var ErrNotFound = errors.New("store: not found")

// Entry is what a key holds. More fields arrive in 1.3 (Version) and 1.4 (ExpiresAt);
// adding fields does not break these tests.
type Entry struct {
	Key   string
	Value []byte
}

// Store is the contract every Burrow engine satisfies. It is deliberately small.
// Two more engines are coming (the network client in 3.3, the durable engine in 4.2),
// and the provided conformance suite is written against it.
type Store interface {
	Get(ctx context.Context, key string) (Entry, error)
	Set(ctx context.Context, key string, value []byte) (Entry, error)
	Delete(ctx context.Context, key string) error
	Keys(ctx context.Context, prefix string) ([]string, error) // sorted by byte order; nil or empty for no match
}

// store/memory.go
type Memory struct { /* yours */ }

func NewMemory() *Memory
func (m *Memory) Len() int

var _ Store = (*Memory)(nil)

// Rules: every method validates the key first and returns ctx.Err() without doing
// anything if the context is already done (3.4 and 7.4 build on this).
// Get of a missing key is (Entry{}, ErrNotFound). Delete of a missing key is ErrNotFound.
// A key set to an empty or nil value exists: Get returns an empty Value and nil error.
// Byte slices are copied on the way in and on the way out.
```

- `store.Entry`, `store.Store`, `var ErrNotFound`
- `NewMemory`, `Get`, `Set`, `Delete`, `Keys`, `Len`
- Copy byte slices in and out; return fresh slices from Keys
- The compile-time assertion `var _ Store = (*Memory)(nil)`

Decisions the tests rely on:

- `Keys` returns a nil slice for no match; the tests accept any zero-length slice.
- `Get` of a missing key returns the zero `Entry` with `ErrNotFound`.
- Entries returned by `Set` carry the key and a copy of the value.

## The tests check

- Set/Get/overwrite/delete; setting a deleted key again; Len counts keys and overwrite doesn't add one; Set returns the stored Entry
- Get of a missing key is the zero Entry with ErrNotFound; Delete of a missing key is ErrNotFound
- Empty and nil values exist and read back as empty
- Aliasing: mutating the slice passed to Set doesn't change the stored value; mutating the Value in an Entry returned by Set or Get doesn't either
- Binary values with \x00 and \xff, and a 1.2 MB value
- Invalid keys fail with ErrInvalidKey on all four methods (Get doesn't say ErrNotFound for them) and store nothing
- A cancelled context returns context.Canceled from every method and a cancelled Set or Delete changes nothing
- Keys: empty prefix means all; a prefix that is itself a key; prefixes of prefixes; Unicode prefixes; no match gives len 0; 1000 keys inserted in reverse come back sorted; the returned slice is a snapshot unaffected by later writes
- 10,000 keys, delete half; two NewMemory stores share nothing
- Compile-time: *Memory satisfies store.Store and store.Store has every method storetest needs
- storetest.Run(t, factory) passes

## Uses

- ValidateKey (built in 0.2 · Test Bench)

## In the wild

This is the shape of every Go repository interface; the Store interface here is etcd's clientv3.KV with fewer options

## New in Go

maps · pointer receivers · constructors · implicit interfaces · compile-time assertions · context.Context as first argument · slice aliasing · slices.Sort · t.Helper

Standard library: `errors`, `bytes`, `context`, `slices`, `strings`
