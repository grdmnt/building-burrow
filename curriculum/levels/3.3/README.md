# 3.3 · Compaction

The log grows forever. Snapshot the state and start a fresh log, without any crash point that loses data.

## Build

```go
func (s *Store) Snapshot() error // after Close: store.ErrClosed
```

Files in the data directory:

- `wal.log`: the log
- `snapshot.db`: the last complete snapshot
- `snapshot.tmp`: a snapshot being written (never trusted)

Snapshot: write everything to `snapshot.tmp`, fsync it, rename it over `snapshot.db`, then reset the log. Open: delete any `snapshot.tmp`, load `snapshot.db` if present, then replay `wal.log` on top.

## The tests check

- 10,000 writes then a snapshot: the log ends up under 512 bytes, and a reopen restores identical state.
- Writes and deletes after a snapshot survive; repeated and empty snapshots work.
- A leftover `snapshot.tmp` is ignored and removed.
- **Crash between rename and log reset:** the old log is put back next to the new snapshot. Replaying it on top must still give the right state.
- A damaged `snapshot.db` must not open silently with missing keys. Checksum it.
- Snapshots taken while 8 goroutines write: after reopen, every key holds its last acknowledged value.

## New in Go

`os.Rename` atomicity · syncing a directory · `encoding/gob` (or your own format) · `sync.RWMutex` to pause writers
