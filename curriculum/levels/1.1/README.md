# 1.1 · Register File

> The smallest memory a machine has is a handful of named slots. Yours is a map behind an interface.

## Your goal

Build the first version of the store: a Go `map` wrapped in a type called `Memory`, with `Get`, `Set`, `Delete` and `Keys`. Alongside it, define the `Store` interface, the contract that says what any Burrow storage engine must be able to do. `Memory` is the first engine to satisfy it; later levels add others, and the provided conformance suite `storetest.Run` checks each of them the same way.

The hard part is not the map. It is being precise about edge cases: a missing key versus a key holding an empty value, slices that secretly share memory, and what happens when the caller's context is already cancelled.

## What you're using from before

**ValidateKey (0.2).** It decides whether a string is a usable key. Every method of `Memory` calls it first, before touching the map, so a bad key is rejected with `ErrInvalidKey` and nothing is stored:

```go
func (m *Memory) Get(ctx context.Context, key string) (Entry, error) {
    if err := ValidateKey(key); err != nil {
        return Entry{}, err
    }
    // ...
}
```

Because `Memory` is in the same package as `ValidateKey`, you call it without a prefix.

## What's new here

**Structs, methods and constructors.** A struct is a type with named fields. Methods are functions with a *receiver* in front of the name. Use a pointer receiver, `(m *Memory)`, whenever the method changes the struct or the struct holds something that must not be copied, which covers everything here. A function named `NewX` that returns a ready-to-use pointer to `X` is Go's constructor convention; there is no language-level constructor:

```go
type Counter struct {
    counts map[string]int
}

func NewCounter() *Counter {
    return &Counter{counts: make(map[string]int)}
}

func (c *Counter) Add(name string) { c.counts[name]++ }
```

**Maps.** `map[K]V` is a hash map. Reading a missing key returns the zero value of `V`, so use the two-value form to tell "missing" from "stored as zero". Writing to a nil map panics, which is why the constructor above calls `make`. Iteration order is deliberately random:

```go
m := make(map[string][]byte)
m["a"] = []byte("1")
v, ok := m["a"]      // ok == true
v, ok = m["zzz"]     // ok == false, v == nil
delete(m, "a")       // no error if "a" wasn't there
len(m)               // number of keys
for k, v := range m { /* random order each run */ }
```

**Interfaces are implicit.** An interface lists methods. Any type with those methods satisfies it; there is no `implements` keyword. The compiler only checks this where a value is actually used as the interface, so a type can drift away from an interface without anyone noticing until a caller breaks. The idiom to catch that at compile time is a throwaway assignment:

```go
var _ Store = (*Memory)(nil)
```

Read it as "a nil pointer to `Memory`, used as a `Store`, assigned to nothing". It compiles only if `Memory` (via its pointer) has every method `Store` lists, and it costs nothing at runtime.

**`context.Context` first.** Any function that might wait, do I/O or be cancelled takes a `ctx context.Context` as its first parameter. It carries deadlines and cancellation down a call chain. An in-memory map never waits, but the interface is shared with engines that will, so the methods take it now and honour one rule: if `ctx.Err()` is already non-nil, return that error and do nothing:

```go
ctx, cancel := context.WithCancel(context.Background())
cancel()
ctx.Err()    // context.Canceled
```

**Multiple returns and the zero value.** Go functions return several values, and `(Entry, error)` is the usual shape: callers check `err` first. When returning an error, return the zero value of the other result too, written `Entry{}` for a struct. The tests check that a missing key gives exactly `Entry{}` and `ErrNotFound`.

**Slices share memory.** A `[]byte` is a small header (pointer, length, capacity) pointing at an array. Assigning or passing a slice copies the header, not the bytes, so two slices can alias the same array:

```go
a := []byte("hello")
b := a
b[0] = 'J'
string(a)    // "Jello": a and b share one array
```

If `Set` stores the caller's slice as-is, the caller can change your data afterwards; if `Get` returns your stored slice, the caller can too. `bytes.Clone(v)` makes an independent copy. Note that `bytes.Clone(nil)` returns `nil`, and `len(nil)` is 0, so a nil value still reads back as empty.

**Prefix matching and sorting.** `strings.HasPrefix(key, prefix)` with an empty prefix is true for every key. `slices.Sort(keys)` sorts a `[]string` in byte order, which is what the tests expect.

**Reading the provided tests.** They use small helpers like `mustSet(t, m, k, v)` that call `t.Helper()` first, so a failure is reported at the line in the test that called the helper, not inside it. Worth copying in tests of your own.

## What to do

1. Create `store/store.go` with `ErrNotFound`, the `Entry` struct and the `Store` interface exactly as in the Reference below.
2. Create `store/memory.go` with a `Memory` struct holding a map from key to stored value, `NewMemory()` that makes the map, and the assertion `var _ Store = (*Memory)(nil)`.
3. Write the four interface methods plus `Len()`. In each one, before touching the map:
   - return `ctx.Err()` if it is non-nil
   - return the error from `ValidateKey(key)` if there is one
4. Get the rules right:
   - `Get` of a missing key returns `Entry{}` and `ErrNotFound`. A key set to an empty or nil value is present: return an empty `Value` and no error.
   - `Set` stores a copy of the value and returns the `Entry` as stored, with its own copy. Overwriting replaces, it doesn't add.
   - `Delete` of a missing key returns `ErrNotFound`.
   - `Keys(prefix)` returns matching keys sorted in byte order, in a fresh slice each call. Empty prefix means all keys. No match may return nil.
   - `Get` returns a copy of the stored value, never the stored slice itself.
5. Run `./burrow watch` and work through the failures. The conformance suite `storetest.Run` reports each rule as a named subtest.

## Example

```go
m := store.NewMemory()
ctx := context.Background()

e, err := m.Set(ctx, "user:1", []byte("gerard"))
// e == Entry{Key: "user:1", Value: []byte("gerard")}, err == nil

e, err = m.Get(ctx, "user:1")
// e.Value == []byte("gerard"), err == nil

_, err = m.Get(ctx, "user:2")
// err == store.ErrNotFound, e == Entry{}

m.Set(ctx, "user:2", nil)
e, err = m.Get(ctx, "user:2")
// len(e.Value) == 0, err == nil: the key exists, its value is empty

keys, _ := m.Keys(ctx, "user:")   // []string{"user:1", "user:2"}
keys, _ = m.Keys(ctx, "")         // all keys, sorted
keys, _ = m.Keys(ctx, "zzz")      // nil

err = m.Delete(ctx, "user:1")     // nil
err = m.Delete(ctx, "user:1")     // store.ErrNotFound
m.Len()                           // 1

_, err = m.Set(ctx, "has space", []byte("x"))
// errors.Is(err, store.ErrInvalidKey) == true, nothing stored
```

And the aliasing rule in action:

```go
buf := []byte("hello")
m.Set(ctx, "k", buf)
buf[0] = 'J'
e, _ := m.Get(ctx, "k")   // e.Value is still "hello"
```

## Done when

- [ ] Set, Get, overwrite and Delete work; `Len` counts keys and an overwrite doesn't add one
- [ ] A missing key is `Entry{}` with `ErrNotFound` from `Get`, and `ErrNotFound` from `Delete`
- [ ] Empty and nil values are stored and read back as empty, not as missing
- [ ] Changing a slice after passing it to `Set`, or changing a `Value` you got back, never changes what's stored
- [ ] Invalid keys fail with `ErrInvalidKey` on every method and store nothing
- [ ] A cancelled context returns `context.Canceled` from every method and changes nothing
- [ ] `Keys` filters by prefix, returns sorted results in a fresh slice, and handles an empty prefix
- [ ] Two `NewMemory()` stores share nothing
- [ ] `storetest.Run` passes

## Reference

```go
// store/store.go
package store

var ErrNotFound = errors.New("store: not found")

// Entry is what a key holds. More fields arrive in later levels; adding
// fields does not break these tests.
type Entry struct {
	Key   string
	Value []byte
}

// Store is the contract every Burrow engine satisfies. It is
// deliberately small, and the provided conformance suite is written
// against it.
type Store interface {
	Get(ctx context.Context, key string) (Entry, error)
	Set(ctx context.Context, key string, value []byte) (Entry, error)
	Delete(ctx context.Context, key string) error
	// Keys returns matches sorted by byte order; nil or empty for no
	// match.
	Keys(ctx context.Context, prefix string) ([]string, error)
}

// store/memory.go
type Memory struct { /* yours */ }

func NewMemory() *Memory
func (m *Memory) Len() int

var _ Store = (*Memory)(nil)

// Rules: every method validates the key first and returns ctx.Err()
// without doing anything if the context is already done. Get of a
// missing key is (Entry{}, ErrNotFound). Delete of a missing key is
// ErrNotFound. A key set to an empty or nil value exists: Get returns
// an empty Value and nil error. Byte slices are copied on the way in
// and on the way out.
```

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

## Try it

```bash
./burrow watch
go test -v -run TestL1_1_Conformance ./store/    # one subtest per rule
go test -v -run 'TestL1_1_Conformance/NoAliasing' ./store/
```

## In the wild

This is the shape of every Go repository interface; the Store interface here is etcd's clientv3.KV with fewer options

## New in Go

structs and methods · pointer receivers · constructors · maps · implicit interfaces · compile-time assertions · context.Context as first argument · multiple returns and zero values · slice aliasing · bytes.Clone · slices.Sort · t.Helper

Standard library: `errors`, `bytes`, `context`, `slices`, `strings`
