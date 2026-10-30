# 1.3 · Versions

> Every write stamps the slot with a number. Before you overwrite, say which number you saw.

## Your goal

Give every stored key a version number and add two operations that use it: `CompareAndSwap` (write this value, but only if the key is still at the version I last saw) and `CompareAndDelete` (same, for delete). Together they are called CAS, for compare-and-swap, and they are the building block for safe read-modify-write: a counter that many goroutines increment, a lock, or a "don't overwrite someone else's edit" check.

Versions start at 1 and go up by one on every successful write to that key. A deleted key forgets its version and starts again at 1 if it comes back.

## What you're using from before

**Arbiter (1.2).** The write lock is what makes CAS atomic. Compare and write happen inside one `Lock()`/`Unlock()` pair, so no other goroutine can change the entry between your check and your write:

```go
m.mu.Lock()
defer m.mu.Unlock()
// look up the entry, compare its version, then write: nobody else
// can touch the map while this runs
```

A CAS that took the lock twice (once to read, once to write) would have exactly the hole it exists to close.

The `Entry` struct and `Set`, `Get`, `Delete` from 1.1 stay, with one change: `Set` now assigns a version, and `Get` reports it.

## What's new here

**Optimistic concurrency.** A pessimistic design locks a key while a client thinks. An optimistic one lets everybody read freely, and makes a write say what it expects to find: "I read version 3, write this only if it's still version 3". If someone else got there first the version is now 4, the write is refused, and the client re-reads and tries again. No client ever blocks another, and a slow or crashed client never holds anything.

**Sentinel errors for outcomes.** `ErrVersionMismatch` is not a failure in the usual sense. It is a normal, expected answer that callers branch on, the same way `ErrNotFound` is. The caller's code is:

```go
_, err := s.CompareAndSwap(ctx, key, seen, newValue)
switch {
case err == nil:
    // it landed
case errors.Is(err, store.ErrVersionMismatch):
    // someone else wrote first: re-read and retry
default:
    // a real problem (bad key, cancelled context)
}
```

**Retry loops.** `for { ... }` with no condition loops forever until a `break` or `return`. A CAS retry loop is read, compute, try to write, and on mismatch go round again. Here is a counter that 50 goroutines can increment safely; this is what the tests run against your store:

```go
for {
    e, err := s.Get(ctx, "counter")
    n := 0
    if err == nil {
        n, _ = strconv.Atoi(string(e.Value))
    }
    // e.Version is 0 when Get failed with ErrNotFound: the zero Entry
    _, err = s.CompareAndSwap(ctx, "counter", e.Version,
        []byte(strconv.Itoa(n+1)))
    if err == nil {
        break
    }
    if !errors.Is(err, store.ErrVersionMismatch) {
        return err
    }
}
```

**`strconv`.** Values are `[]byte`, so a number is stored as its decimal text. `strconv.Itoa(42)` gives `"42"`; `strconv.Atoi("42")` gives `42, nil`. `string(b)` and `[]byte(s)` convert between the two.

**`uint64`.** Go's integer types have explicit sizes. Versions are `uint64`: unsigned, 64 bits, so they never go negative and never realistically wrap. The zero value `0` doubles as "no version", which is why `ifVersion == 0` means "the key must not exist".

## What to do

1. Add `Version uint64` to `Entry`, and `ErrVersionMismatch` and the two new methods to the `Store` interface, as in the Reference.
2. Store the version with the value. The map value becomes a small struct holding both, rather than a second map keyed the same way.
3. Update `Set`: a new key gets version 1, an existing key gets its version plus one. Return the entry as stored, with the new version. `Get` returns the stored version too.
4. Write `CompareAndSwap(ctx, key, ifVersion, value)`. After the context and key checks, under the write lock:
   - `ifVersion == 0`: the key must be absent. If present, `ErrVersionMismatch`. Otherwise create it at version 1.
   - `ifVersion != 0`: the key must be present at exactly that version. Missing is `ErrNotFound`; a different version is `ErrVersionMismatch`. Otherwise store the value and bump the version.
   - A mismatch changes nothing. Copy the value in, like `Set`.
5. Write `CompareAndDelete(ctx, key, ifVersion)` the same way. Missing is `ErrNotFound`; any version other than the stored one, including 0, is `ErrVersionMismatch`.
6. `Delete` already removes the whole entry, so a deleted key naturally starts at 1 again. Check it does.

## Example

```go
m := store.NewMemory()
ctx := context.Background()

e, _ := m.Set(ctx, "k", []byte("one"))      // e.Version == 1
e, _ = m.Set(ctx, "k", []byte("two"))       // e.Version == 2

e, err := m.CompareAndSwap(ctx, "k", 2, []byte("three"))
// err == nil, e.Version == 3

_, err = m.CompareAndSwap(ctx, "k", 2, []byte("stale"))
// err == store.ErrVersionMismatch; the key still holds "three" at 3

_, err = m.CompareAndSwap(ctx, "k", 0, []byte("x"))
// err == store.ErrVersionMismatch: 0 means "must not exist"

e, err = m.CompareAndSwap(ctx, "new", 0, []byte("x"))
// err == nil, e.Version == 1: created

_, err = m.CompareAndSwap(ctx, "missing", 7, []byte("x"))
// err == store.ErrNotFound

err = m.CompareAndDelete(ctx, "k", 2)       // store.ErrVersionMismatch
err = m.CompareAndDelete(ctx, "k", 3)       // nil, k is gone
err = m.CompareAndDelete(ctx, "k", 3)       // store.ErrNotFound

e, _ = m.Set(ctx, "k", []byte("back"))      // e.Version == 1 again
```

The counter test, in words: 50 goroutines each run the retry loop above 200 times. With plain `Get` then `Set` you would lose updates and end below 10,000. With CAS the final value is exactly `10000`, its version is exactly 10000 (one per successful write), and the test logs how many retries the mismatches caused.

## Done when

- [ ] `Set` returns version 1, then 2, 3; `Get` reports the same; delete then set starts at 1 again
- [ ] CAS with the right version succeeds and bumps the version; a wrong version returns `ErrVersionMismatch` and leaves value and version untouched
- [ ] CAS with `ifVersion` 0 creates a missing key at version 1 and refuses an existing one with `ErrVersionMismatch`; a non-zero `ifVersion` on a missing key is `ErrNotFound`
- [ ] `CompareAndDelete` deletes only on an exact version match, `ErrNotFound` for a missing key, `ErrVersionMismatch` otherwise
- [ ] The slice passed to CAS is copied; invalid keys and cancelled contexts fail before anything is stored
- [ ] 50 goroutines doing 200 CAS-retry increments each end on exactly 10,000
- [ ] `storetest.RunCAS` passes

## Reference

```go
// store/store.go additions
var ErrVersionMismatch = errors.New("store: version mismatch")

// Entry gains:
//   Version uint64  // 1 on first Set, +1 on every Set or successful
//                   // CAS; a key deleted and set again starts at 1

// Store gains:
//   CompareAndSwap(ctx context.Context, key string, ifVersion uint64,
//       value []byte) (Entry, error)
//     ifVersion 0 means the key must not exist (create-if-absent).
//     Otherwise the stored version must equal ifVersion. On mismatch:
//     ErrVersionMismatch and no change.
//     A missing key with ifVersion != 0 is ErrNotFound.
//   CompareAndDelete(ctx context.Context, key string,
//       ifVersion uint64) error
//     Deletes only if the version matches. ErrNotFound /
//     ErrVersionMismatch otherwise.

// Set and CAS return the Entry as stored, with its new Version.
// This level installs storetest.RunCAS.
```

Decisions the tests rely on:

- For `CompareAndDelete`, `ifVersion` 0 never matches: an existing key gives `ErrVersionMismatch`, a missing key `ErrNotFound`.
- `CompareAndSwap` copies the value slice it is given, like `Set`.

## The tests check

- Set returns Version 1, then 2, 3; Get reports the same; Delete then Set restarts at 1 (storetest.RunCAS VersionsCount)
- CAS with the right version succeeds and bumps the version; the wrong version returns ErrVersionMismatch and leaves the value and version untouched
- CAS with ifVersion 0 creates a missing key (Version 1) and fails with ErrVersionMismatch when the key exists; CAS with ifVersion 7 on a missing key is ErrNotFound
- CompareAndDelete: stale version is ErrVersionMismatch and deletes nothing, right version deletes, missing key is ErrNotFound; after a CompareAndDelete, CAS with the old version is ErrNotFound and CAS with ifVersion 0 recreates the key at Version 1
- Counter: 50 goroutines x 200 iterations of Get, parse, CAS, retry on mismatch; final value is 10,000 and the test logs how many mismatches happened
- CAS copies the value slice it is given
- Invalid key (CAS and CompareAndDelete) and cancelled context fail before anything is stored
- storetest.RunCAS passes

## Try it

```bash
./burrow watch
go test -race -v -run TestL1_3_LostUpdatesPrevented ./store/  # retries
```

Then write the counter as a tiny program of your own: 50 goroutines, 200 CAS-retry increments each, print the final value. Swap the CAS for a plain `Set` and watch the total come up short.

## In the wild

etcd Txn with mod_revision compares, memcached cas tokens, HTTP ETag/If-Match, DynamoDB condition expressions

## New in Go

optimistic concurrency · sentinel errors for outcomes · retry loops · strconv · uint64

Standard library: `errors`, `strconv`, `sync`
