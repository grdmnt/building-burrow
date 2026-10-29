# 4.4 · Compaction

The log grows forever. Snapshot the state and start a fresh log, without any crash point that loses data.

## Build

```go
// store/memory.go addition
func (m *Memory) Snapshot() []Entry // a consistent copy of every live entry, taken under the read lock

// durable/durable.go additions
func (s *Store) Snapshot() error // after Close: store.ErrClosed

// Files in the data directory:
//   wal.log       the log
//   snapshot.db   the last complete snapshot: gob of []store.Entry, then a trailing crc32 of the gob bytes
//   snapshot.tmp  a snapshot being written; never trusted
// Snapshot: write snapshot.tmp, fsync it, rename over snapshot.db, fsync the directory, then
// reset the log (Truncate(0)), so LogLen returns to 0. Snapshot takes an exclusive lock that
// pauses writers while the entries are copied and the log is reset. Open: remove any snapshot.tmp, load snapshot.db if present (refuse
// on bad checksum), then replay wal.log on top.
```

- `Memory.Snapshot()`
- `durable.Store.Snapshot()` with tmp/fsync/rename/dir-sync/reset
- Open loads the snapshot then replays; checksum the snapshot

Decisions the tests rely on:

- `Memory.Snapshot()` returns live entries sorted by key, expired ones excluded.
- `Snapshot` takes an exclusive lock that pauses writers while entries are copied and again while the log is reset; `LogLen` is 0 afterwards.
- `snapshot.db` is the gob encoding of `[]store.Entry` followed by a 4-byte big-endian IEEE CRC32 of the gob bytes.
- Resetting the log is `Truncate(0)`.

## The tests check

- 10,000 writes then Snapshot: wal.log is under 64 bytes and a reopen restores identical state including versions and expiries
- Writes and deletes after a snapshot survive a reopen; repeated and empty snapshots work
- A leftover snapshot.tmp is ignored and removed on Open
- Crash between rename and log reset: the old log is put back next to the new snapshot; replaying it on top still gives the right state
- A flipped byte in snapshot.db makes Open fail (no silent partial state)
- Snapshot while 8 goroutines write for 200 ms: after reopen every key holds its last acknowledged value and version
- Snapshot after Close is store.ErrClosed

## Uses

- Durable store (built in 4.2 · Non-volatile Store)
- CRC guard (built in 4.3 · Parity Bits)

## In the wild

Redis RDB + AOF rewrite; etcd snapshots; the write-temp-then-rename idiom in every database and in `go build`'s own output

## New in Go

os.Rename atomicity · syncing a directory · encoding/gob · crash-safety reasoning

Standard library: `os`, `path/filepath`, `encoding/gob`, `hash/crc32`
