# 4.2 · Non-volatile Store

> Volatile memory forgets when the power drops. Non-volatile memory doesn't. Same chip, plus a battery.

## Your goal

Put the log under the store. `durable.Store` is the in-memory engine from chapter 1 with one change: every write is appended to the WAL *before* it is applied, and `Open` replays the log so a restarted process has exactly the state it had when it died. Reads don't change: `Get`, `Keys` and `Len` are the Memory engine's own methods, reached through *embedding*, Go's way of building one type on top of another.

The one design rule: a log record stores the *state that resulted*, not the *operation that was asked for*. A successful CAS is logged as "key k now holds this entry, version 3, expiring at this instant". Replay then has nothing to decide; it installs entries. A new method on `Memory`, `Restore`, is the hook for that.

## What you're using from before

**WAL** (4.1 · Write-Ahead Log). `Open` it at `<dir>/wal.log`, `Append` one encoded change record per write, and on startup walk `Records` to rebuild state:

```go
for rec, err := range s.log.Records() {
	if err != nil {
		return nil, err
	}
	// decode rec, then s.Memory.Restore(entry)
	// or s.Memory.Delete(ctx, key)
}
```

**Memory store** (1.1 · Register File). The engine you are wrapping. It gains two methods here, `Restore` (install an entry exactly as given) and `Now` (its clock reading). Everything you don't override, `Get`, `Keys`, `Len`, `Close`, comes through the embedded `*store.Memory` untouched.

**CAS** (1.3 · Versions). The version rules (1 on first set, +1 on every change, mismatch means no change) still decide what happens. You re-apply them when computing the entry to log, and `store.ErrVersionMismatch` is still what a stale CAS returns, without touching the log.

**Expiry** (1.4 · Decay). `ExpiresAt` is an absolute instant, and that is what goes into the record (as Unix nanoseconds, 0 for never). Compute it with `s.Memory.Now().Add(ttl)` so a `WithClock` option passed through `durable.Open` governs the log too. `Touch` changes `ExpiresAt` and nothing else, and it is logged like any other write.

**Janitor** (1.5 · Janitor). The sweeper keeps running inside the embedded Memory, and the caller's `WithOnEvict` still fires. Evictions are *not* logged (see below). `Close` must stop the janitor (`s.Memory.Close()`) and close the log.

## What's new here

**Struct embedding and method promotion.** Putting a type in a struct without a field name embeds it. The outer type then has every method of the inner one, as if it had declared them itself:

```go
type Logger struct {
	*bytes.Buffer // embedded, no field name
}

var l Logger
l.Buffer = new(bytes.Buffer)
l.WriteString("hi") // promoted from *bytes.Buffer
l.Buffer.Len()      // the inner value, reachable by its type name
```

This is composition, not inheritance: `Logger` is not a `*bytes.Buffer` and can't be passed as one, but it has the methods. For the store, `type Store struct { *store.Memory }` means `durable.Store` satisfies `store.Store` the moment it is declared, because all the methods are there.

**Overriding a promoted method.** Declare a method with the same name on the outer type and it shadows the promoted one. The inner method is still there, one field away:

```go
func (s *Store) Set(ctx context.Context, key string, value []byte) (
	store.Entry, error) {
	// ... decide, log ...
	return s.Memory.Set(ctx, key, value) // or Restore; the inner Set
}
```

Override only the seven methods that change state. `Get`, `Keys`, `Len` and the rest stay promoted.

**Decide, log, apply.** Each write follows the same order: validate the key and context; lock the key; read the current entry from `s.Memory`; work out what Memory's rules say the new entry is (or that the write is rejected); `Append` the record; then install it with `Restore` (or `Delete`). Rejected writes never reach the log. The lock is per key so unrelated keys don't queue behind one fsync; a fixed array of mutexes indexed by a hash of the key ("striped" locks) is the cheap way to get that:

```go
var stripes [64]sync.Mutex
h := fnv.New32a()
h.Write([]byte(key))
mu := &stripes[h.Sum32()%64]
```

**`encoding/binary` for the record.** A change record is a few fixed-width integers and two byte strings. `binary.BigEndian.AppendUint64` and friends build it without a struct or reflection:

```go
buf := []byte{byte(wire.OpSet)}
buf = binary.BigEndian.AppendUint64(buf, e.Version)
buf = binary.BigEndian.AppendUint64(buf, uint64(expiresAtUnixNanos))
buf = binary.BigEndian.AppendUint16(buf, uint16(len(e.Key)))
buf = append(buf, e.Key...)
buf = append(buf, e.Value...)
```

Decoding reads the fields back in the same order with `binary.BigEndian.Uint64(buf[1:9])` and so on. The op byte up front tells you which of the two record shapes you are looking at.

**Replay.** On `Open`, before serving anything, walk the log from the top and apply each record in order: a put calls `Restore`, a delete calls `Delete` (ignoring `ErrNotFound`, since replaying a delete of an already-gone key is fine). Because every put carries its absolute `ExpiresAt`, `Restore` can drop what has already expired, and the log never needs an "expired" record. That matters: a reader can evict key `k`, another goroutine can then `Set k` and log a put, and an eviction record would land *after* that put and wrongly delete the new value on replay.

## What to do

1. In `store/memory.go` add `Restore(e Entry) error` (validate the key; if `e.ExpiresAt` is set and already past on the store's clock, remove any existing entry and return nil without calling `OnEvict`; otherwise install the entry exactly as given) and `Now() time.Time`.
2. Create `durable/durable.go` with `Store` embedding `*store.Memory`, plus a `*wal.Log`, the stripe locks and a record counter for `LogLen`.
3. Write `Open(dir, opts...)`: `os.MkdirAll(dir)`, `store.NewMemory(opts...)`, `wal.Open(filepath.Join(dir, "wal.log"))`, then replay every record into the Memory and count them.
4. Write the two encoders and one decoder for the change records in the Reference.
5. Override `Set`, `SetWithTTL`, `Delete`, `CompareAndSwap`, `CompareAndSwapWithTTL`, `CompareAndDelete` and `Touch`. Each one: validate, check `ctx.Err()`, lock the stripe, read the current entry with `s.Memory.Get`, apply Memory's rules to decide the new entry or the error, `Append`, then `Restore` or `s.Memory.Delete`. Bump the record counter after a successful append.
6. Write `LogLen` and `Close` (close the Memory, then the log; a second `Close` is `nil`).
7. Add `var _ store.Store = (*Store)(nil)`.
8. Run `gofmt -l .` and `go vet ./...` until both print nothing.

## Example

```go
s, _ := durable.Open("/tmp/burrow-data")
s.Set(ctx, "a", []byte("1"))                 // logged, then applied
s.Set(ctx, "a", []byte("2"))                 // version 2
s.SetWithTTL(ctx, "t", []byte("x"), time.Hour)
s.Delete(ctx, "a")
s.Set(ctx, "a", []byte("3"))                 // version 1 again
s.LogLen()                                   // 5
s.Close()

// later, maybe after a crash:
s2, _ := durable.Open("/tmp/burrow-data")
e, _ := s2.Get(ctx, "a")  // "3", version 1
e, _ = s2.Get(ctx, "t")   // "x", same ExpiresAt as before
e, _ = s2.Set(ctx, "a", []byte("4")) // version 2: counting continues
```

What is in `wal.log` after the five writes, one record per line, decoded:

```text
put    a  v1  never  "1"
put    a  v2  never  "2"
put    t  v1  <abs>  "x"      <abs> = the instant one hour from then
delete a
put    a  v1  never  "3"
```

Nothing is written for a `Get`, a rejected CAS, or a key that expires.

## Done when

- [ ] `storetest.Run`, `RunConcurrent`, `RunCAS` and `RunTTL` pass against `durable.Open(t.TempDir())`
- [ ] After `Close` and `Open`, every key, value and version is back exactly, and versions keep counting from where they were
- [ ] Expiries are absolute: a key with 7 s left survives a restart with the same `ExpiresAt`, a key that expired while the store was down is gone without `OnEvict`, and a `Touch` survives too
- [ ] A CAS counter at 1000 reopens at 1000 with version 1000; a `CompareAndSwapWithTTL` lock keeps value, version and expiry
- [ ] Rejected writes and reads don't grow the log; evictions don't either; `LogLen` is 0 on a fresh directory
- [ ] Dropping the file handles without `Close` still restores everything acknowledged
- [ ] `Open` creates nested directories; `Close` twice is `nil`; `Set` and `Get` after `Close` are `store.ErrClosed`
- [ ] `Memory.Restore` installs an entry as given, drops an already-expired one silently, and `Memory.Now` reads the injected clock
- [ ] A child process killed with `kill -9` mid-stream loses no acknowledged write
- [ ] `gofmt -l .` and `go vet ./...` print nothing

## Reference

The exact contract the tests hold you to.

```go
// store/memory.go additions (for engines that replay a log)
// Restore installs an entry exactly as given, including Version and
// ExpiresAt, without logging or validation beyond the key. An entry
// already expired on the store's clock is dropped silently and never
// reaches OnEvict.
func (m *Memory) Restore(e Entry) error
// Now is the store's clock reading (time.Now, or WithClock), so engines
// built on Memory compute expiries with the same clock, including under
// a FakeClock in tests.
func (m *Memory) Now() time.Time

// durable/durable.go
package durable

// Open creates dir if needed. The log lives at <dir>/wal.log. opts go
// straight to store.NewMemory, so a caller's WithClock or WithOnEvict
// works unchanged.
func Open(dir string, opts ...store.Option) (*Store, error)

type Store struct {
	*store.Memory // embedded: Get, Keys, Len, Close... come for free
	// yours
}

// Overridden so that every change is appended to the log before it is
// applied: Set, SetWithTTL, Delete, CompareAndSwap,
// CompareAndSwapWithTTL, CompareAndDelete, Touch.
// Each write: validate, take a per-key lock (striped mutexes are fine),
// read the current entry from the embedded Memory, decide the new state
// with Memory's rules, Append the record, then apply it with Restore or
// Delete.

// LogLen is the number of records in the log since the last snapshot.
func (s *Store) LogLen() int
// Close closes the Memory (its janitor) and the log.
func (s *Store) Close() error

var _ store.Store = (*Store)(nil)
```

Change records store absolute state, never operations. All integers are big-endian:

| Record | Layout |
|---|---|
| put | `wire.OpSet` (0x02) · version uint64 · expiresAt int64 Unix nanos (0 = never) · key len uint16 · key · value |
| delete | `wire.OpDelete` (0x03) · key len uint16 · key |

A put holds the full entry as stored. Expiries are not logged: every put carries its absolute `ExpiresAt` and `Restore` drops what has already expired at replay.

Decisions the tests rely on:

- Order on every mutation: validate, lock the key's stripe, read the current entry, decide the new state with Memory's rules, append the record, then apply it with `Restore` or `Delete`. A crash between append and apply leaves a logged change the client was never told about; replay applies it, which is allowed.
- Expiries are never logged. A reader can evict key k, another goroutine can then Set k and log a put, and the eviction's delete would land after the put, so replay would delete the new value; absolute `ExpiresAt` in every put plus `Restore` dropping expired entries makes replay correct without eviction records.
- `Restore` of an entry that is already expired on the store's clock returns nil and does nothing. `ExpiresAt` is computed from `Memory.Now()`, so a `FakeClock` passed through `durable.Open` governs the log too.
- `durable.Open` passes its options straight to `store.NewMemory`; a caller's `WithOnEvict` fires on expiry exactly as it would on a Memory store.
- The op byte of a change record is `wire.OpSet` (0x02) for a put and `wire.OpDelete` (0x03) for a delete.

## The tests check

- storetest.Run, RunConcurrent, RunCAS and RunTTL pass against durable.Open(t.TempDir())
- Restart restores state exactly: overwrites, deletes, empty and binary values, and Version numbers continue from where they were (Set after restart bumps, not resets)
- Expiry is absolute: on one FakeClock, a 10 s TTL and a Touch of 20 s, close, Advance(3s), reopen: ExpiresAt is unchanged, the Touch's expiry survived, a key that expired while the store was down is gone, OnEvict never ran for it, and the key expires at its original instant
- A CAS counter run to 1000 then restarted reads 1000 with Version 1000; a lock taken with CompareAndSwapWithTTL keeps its value, version and expiry across a restart
- Rejected writes (invalid key, version mismatch, delete of a missing key, Touch of a missing key, ttl 0, a cancelled context) and reads don't grow the log
- Expiries are not logged: 20 keys expire under the janitor (your WithOnEvict passed through durable.Open sees all 20) and the file size and LogLen stay at 20
- LogLen is 0 on a fresh directory
- Restart without Close (the file handle is simply dropped) still restores everything acknowledged
- Open creates nested directories and puts the log at <dir>/wal.log; Open on a path that can't be a directory fails; Close twice is nil; Set and Get after Close are store.ErrClosed
- Memory.Restore installs an entry as given (version 42 stays 42 and the next Set gives 43); restoring an already-expired entry is not an error, installs nothing, replaces what was there, and never reaches OnEvict; Memory.Now reads the injected clock
- kill -9: a child process writes keys through durable.Store and prints each acknowledged key; it is SIGKILLed mid-stream; every printed key is present after reopen with the right value

## Try it

```bash
./burrow watch
go test -run 'TestL4_2_RestartRestoresState' -v ./durable
```

To see it from the outside, run a throwaway server with `durable.Open("./data")` in place of `store.NewMemory()` (the two-line `main` from 3.3's Try it), set a few keys with `burrowctl`, `kill -9` the server, start it again, and `get` them back.

## In the wild

Redis AOF, Bitcask, the memory+log core of LevelDB's memtable; Go's embedding is how net/http middleware and io wrappers extend types without inheritance.

## New in Go

struct embedding and method promotion · overriding promoted methods · binary encoding with encoding/binary · replay

Standard library: `os`, `encoding/binary`, `path/filepath`, `time`
