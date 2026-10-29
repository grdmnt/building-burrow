# 4.2 · Non-volatile Store

The durable store is the Memory engine plus a log. Embed `*store.Memory`, override only the methods that change state, and the whole conformance suite passes from disk. Records store the state that resulted, never the operation that was asked for.

## Build

```go
// store/memory.go additions (for engines that replay a log)
// Restore installs an entry exactly as given, including Version and ExpiresAt, without
// logging or validation beyond the key. An entry already expired on the store's clock is
// dropped silently and never reaches OnEvict.
func (m *Memory) Restore(e Entry) error
// Now is the store's clock reading (time.Now, or WithClock), so engines built on Memory
// compute expiries with the same clock, including under a FakeClock in tests.
func (m *Memory) Now() time.Time

// durable/durable.go
package durable

// Open creates dir if needed. The log lives at <dir>/wal.log. opts go straight to
// store.NewMemory, so a caller's WithClock or WithOnEvict works unchanged.
func Open(dir string, opts ...store.Option) (*Store, error)

type Store struct {
	*store.Memory // embedded: Get, Keys, Len, Close... come for free
	// yours
}

// Overridden so that every change is appended to the log before it is applied:
// Set, SetWithTTL, Delete, CompareAndSwap, CompareAndSwapWithTTL, CompareAndDelete, Touch.
// Each write: validate, take a per-key lock (striped mutexes are fine), read the current
// entry from the embedded Memory, decide the new state with Memory's rules, Append the
// record, then apply it with Restore or Delete.
// Change records store absolute state, never operations:
//   put    wire.OpSet (0x02)    | version uint64 | expiresAt int64 unix nanos (0 = never) | key len uint16 | key | value   (the full entry as stored)
//   delete wire.OpDelete (0x03) | key len uint16 | key
// Expiries are not logged: every put carries its absolute ExpiresAt and Restore drops
// what has already expired at replay.
func (s *Store) LogLen() int // records in the log since the last snapshot (5.1 uses it)
func (s *Store) Close() error // closes the Memory (its janitor) and the log

var _ store.Store = (*Store)(nil)
```

- `Memory.Restore` and `Memory.Now`
- `durable.Store` embedding `*store.Memory`, overriding the seven mutating methods behind per-key locks
- A change-record encoding carrying version and absolute expiry, with wire.OpSet / wire.OpDelete as the op byte
- Replay on Open through Restore; `LogLen`

Decisions the tests rely on:

- Order on every mutation: validate, lock the key's stripe, read the current entry, decide the new state with Memory's rules, append the record, then apply it with `Restore` or `Delete`. A crash between append and apply leaves a logged change the client was never told about; replay applies it, which is allowed.
- Expiries are never logged. A reader can evict key k, another goroutine can then Set k and log a put, and the eviction's delete would land after the put, so replay would delete the new value; absolute `ExpiresAt` in every put plus `Restore` dropping expired entries makes replay correct without eviction records.
- `Restore` of an entry that is already expired on the store's clock returns nil and does nothing. `ExpiresAt` is computed from `Memory.Now()`, so a `FakeClock` passed through `durable.Open` governs the log too.
- `durable.Open` passes its options straight to `store.NewMemory`; a caller's `WithOnEvict` fires on expiry exactly as it would on a Memory store.
- The op byte of a change record is `wire.OpSet` (0x02) for a put and `wire.OpDelete` (0x03) for a delete.

## The tests check

- storetest.Run, RunConcurrent, RunCAS and RunTTL pass against durable.Open(t.TempDir(), opts...) (RunTTL passes store.WithClock through)
- Restart restores state exactly: overwrites, deletes, empty and binary values, and Version numbers continue from where they were (Set after restart bumps, not resets)
- A key with a 10 s TTL, restart after 3 s on the same FakeClock: ExpiresAt is unchanged and the key expires at the original instant; a key already expired at restart is invisible, OnEvict was not called for it, and the log has no delete record for it
- A CAS counter run to 1000 then restarted reads 1000 with Version 1000; CompareAndSwapWithTTL survives a restart with its ExpiresAt
- Rejected writes (invalid key, version mismatch, delete of a missing key), reads, and janitor evictions don't grow the log (file size and LogLen unchanged)
- LogLen counts one record per applied change and is 0 on a fresh directory
- Restart without Close (the file handle is simply dropped) still restores everything acknowledged
- Open creates the directory; Open on an unwritable path fails; Close twice is nil; after Close every method returns store.ErrClosed; a caller's WithOnEvict passed to Open fires on expiry
- kill -9: a child process writes keys through durable.Store and prints each acknowledged key; it is SIGKILLed mid-stream; every printed key is present after reopen with the right value

## Uses

- WAL (built in 4.1 · Write-Ahead Log)
- Janitor (built in 1.5 · Janitor)
- CAS (built in 1.3 · Versions)
- Expiry (built in 1.4 · Decay)
- Memory store (built in 1.1 · Register File)

## In the wild

Redis AOF, Bitcask, the memory+log core of LevelDB's memtable; Go's embedding is how net/http middleware and io wrappers extend types without inheritance

## New in Go

struct embedding and method promotion · overriding promoted methods · binary encoding with encoding/binary · replay

Standard library: `os`, `encoding/binary`, `path/filepath`, `time`
