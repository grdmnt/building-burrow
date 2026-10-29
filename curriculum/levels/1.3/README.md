# 1.3 · Versions

Give every key a version and add compare-and-swap. This one primitive becomes the lock in 8.1, the counter probe in 11.3 and `If-Match` in 7.1.

## Build

```go
// store/store.go additions
var ErrVersionMismatch = errors.New("store: version mismatch")

// Entry gains:
//   Version uint64  // 1 on first Set, +1 on every Set or successful CAS; a key that
//                   // is deleted and set again starts at 1

// Store gains:
//   CompareAndSwap(ctx context.Context, key string, ifVersion uint64, value []byte) (Entry, error)
//     ifVersion 0 means the key must not exist (create-if-absent). Otherwise the
//     stored version must equal ifVersion. On mismatch: ErrVersionMismatch and no change.
//     A missing key with ifVersion != 0 is ErrNotFound.
//   CompareAndDelete(ctx context.Context, key string, ifVersion uint64) error
//     Deletes only if the version matches. ErrNotFound / ErrVersionMismatch otherwise.

// Set and CAS return the Entry as stored, with its new Version.
// This level installs storetest.RunCAS.
```

- `Entry.Version`, maintained by Set, CAS and Delete
- `CompareAndSwap` and `CompareAndDelete`, atomic under the Arbiter's lock
- `var ErrVersionMismatch`

Decisions the tests rely on:

- For `CompareAndDelete`, `ifVersion` 0 never matches: an existing key gives `ErrVersionMismatch`, a missing key `ErrNotFound`.
- `CompareAndSwap` copies the value slice it is given, like `Set`.

## The tests check

- Set returns Version 1, then 2, 3; Get reports the same; Delete then Set restarts at 1
- CAS with the right version succeeds and bumps the version; the wrong version returns ErrVersionMismatch and leaves the value and version untouched
- CAS with ifVersion 0 creates a missing key (Version 1) and fails with ErrVersionMismatch when the key exists
- CAS with ifVersion != 0 on a missing key is ErrNotFound
- CompareAndDelete: right version deletes, wrong version is ErrVersionMismatch, missing key is ErrNotFound
- Counter: 50 goroutines x 200 iterations of Get, parse, CAS, retry on mismatch; final value is 10,000 and the test counts at least one ErrVersionMismatch
- Invalid key and cancelled context fail before anything happens; the value slice passed to CAS is copied
- storetest.RunCAS passes

## Try it

Write the counter loop as a tiny program: 50 goroutines, 200 CAS-retry increments each, print the final value.

## Uses

- Arbiter (built in 1.2 · Arbiter)

## In the wild

etcd Txn with mod_revision compares, memcached cas tokens, HTTP ETag/If-Match, DynamoDB condition expressions

## New in Go

optimistic concurrency · sentinel errors for outcomes · strconv · retry loops

Standard library: `errors`, `strconv`, `sync`
