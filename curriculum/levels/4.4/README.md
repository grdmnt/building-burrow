# 4.4 · Compaction

> A diary that records every change grows without end. Every so often, write down where things stand and start a fresh page.

## Your goal

The log only ever grows. A key set ten thousand times is ten thousand records, and every restart replays all of them to arrive at one value. *Compaction* fixes that: write the current state of the whole store to a file (a *snapshot*), then empty the log. Restart becomes "load the snapshot, replay whatever came after it".

The hard part is not the writing, it is the crashing. The process can die at any instruction: halfway through writing the snapshot, right after it, between swapping the snapshot in and emptying the log. Every one of those points has to leave files on disk that `Open` turns back into the right state. This level is about that reasoning as much as about the code.

## What you're using from before

**Durable store** (4.2 · Non-volatile Store). `Snapshot` is a new method on `durable.Store`, and `Open` gains a step before replay: load `snapshot.db` if there is one. `Memory` gets a `Snapshot()` of its own that returns a copy of every live entry, which is what gets written to the file. Loading uses the same `Restore` as replay:

```go
for _, e := range entries { // decoded from snapshot.db
	s.Memory.Restore(e)
}
// then replay wal.log on top, exactly as before
```

**CRC guard** (4.3 · Parity Bits). Two things. `Truncate(0)` is how the log is emptied after a snapshot, and `Len()` is what `LogLen` reports. And the same `crc32.ChecksumIEEE` from 4.3 guards the snapshot file: the gob bytes are followed by their checksum, and `Open` refuses a snapshot whose checksum doesn't match.

## What's new here

**`os.Rename` is atomic.** Within one directory, a rename either happened or it didn't; a reader never sees a half-renamed file. So the way to replace a file safely is never to write into it. Write the new contents to a temporary name, make sure they are on disk, then rename over the old one:

```go
tmp := filepath.Join(dir, "snapshot.tmp")
f, _ := os.Create(tmp)
// write everything, then:
f.Sync()
f.Close()
os.Rename(tmp, filepath.Join(dir, "snapshot.db")) // the switch
```

At every instant `snapshot.db` is either the complete old snapshot or the complete new one. `snapshot.tmp` is by definition untrustworthy, so `Open` deletes one if it finds it.

**Syncing a directory.** `f.Sync()` makes the file's *bytes* durable. The rename is a change to the *directory*, and that lives in its own metadata which also sits in the cache. To make the rename durable, open the directory and sync it:

```go
d, err := os.Open(dir)
// ...
err = d.Sync()
d.Close()
```

Without it, a power cut right after the rename can bring back the old name pointing at the old file.

**`encoding/gob`.** gob is Go's own serialisation format: give an encoder any value and it writes it, give a decoder a pointer and it reads it back. No tags, no schema; it handles slices, structs and `time.Time` on its own:

```go
var buf bytes.Buffer
gob.NewEncoder(&buf).Encode(entries) // entries is []store.Entry

var back []store.Entry
gob.NewDecoder(bytes.NewReader(data)).Decode(&back)
```

gob is Go-only, which is fine for a file only this program reads. Its one blind spot is bit flips inside a value: a changed byte in the middle of a string still decodes without complaint. That is why the checksum goes after the gob bytes.

**Crash-safety reasoning.** Walk the snapshot procedure step by step and ask, at every gap, "what is on disk now, and what does `Open` do with it?":

| Crash point | On disk | `Open` does |
|---|---|---|
| While writing `snapshot.tmp` | old `snapshot.db` (maybe none), full log, a partial tmp | remove tmp, load old snapshot, replay the full log |
| After the rename, before the log reset | new `snapshot.db`, the full old log | load the new snapshot, replay the old log on top |
| After the log reset | new `snapshot.db`, empty log | load the snapshot, nothing to replay |

The middle row is the interesting one. Replaying old records on top of a snapshot that already contains them is harmless because `Restore` is *idempotent*: restoring the same entry twice gives the same state. Deletes of keys that are already gone must be tolerated too. If you ever find a crash point where `Open` can't recover, the order of steps is wrong.

**Pausing writers while you copy.** The snapshot must be one consistent picture, not a mix of before and after some write. The per-key stripes from 4.2 let unrelated writes run in parallel; put them all under the read side of one `sync.RWMutex` and `Snapshot` can take the write side for the two short moments it needs: copying the entries (and noting the log length), and later resetting the log. Writing the file itself happens with the lock released.

## What to do

1. In `store/memory.go`, add `Snapshot() []Entry`: under the read lock, copy every live entry (expired ones skipped), values cloned, sorted by key.
2. In `durable`, put every write's stripe lock under the read side of a store-wide `sync.RWMutex`.
3. Write `Store.Snapshot()`:
   - `store.ErrClosed` if the store is closed.
   - Take the exclusive lock, call `s.Memory.Snapshot()` and note `s.log.Len()`, release the lock.
   - Encode the entries with gob into a buffer, append the big-endian CRC32 of the gob bytes, write it all to `snapshot.tmp`, `Sync`, `Close`.
   - `os.Rename` tmp over `snapshot.db`, then sync the directory.
   - Take the exclusive lock again, `Truncate(0)` the log, reset the counter behind `LogLen`, release.
4. In `Open`, before replay: remove `snapshot.tmp` if present; if `snapshot.db` exists, read it, check the trailing CRC against the bytes before it (a mismatch fails `Open`), decode, and `Restore` every entry.
5. Run `gofmt -l .` and `go vet ./...` until both print nothing.

## Example

```go
s, _ := durable.Open(dir)
for i := range 10_000 {
	s.Set(ctx, fmt.Sprintf("k%02d", i%100), []byte(strconv.Itoa(i)))
}
s.LogLen()   // 10000; wal.log is a few hundred KB
s.Snapshot()
s.LogLen()   // 0; wal.log is empty, snapshot.db holds 100 entries
s.Set(ctx, "k00", []byte("after"))
s.LogLen()   // 1
s.Close()

s2, _ := durable.Open(dir) // loads 100 entries, replays 1 record
e, _ := s2.Get(ctx, "k00") // "after", with the version it had before
```

The data directory afterwards:

```bash
$ ls -l data
-rw-r--r--  1 you  staff   2131  snapshot.db
-rw-r--r--  1 you  staff     29  wal.log
```

## Done when

- [ ] After 10,000 writes and a `Snapshot`, `wal.log` is under 64 bytes, `snapshot.db` exists, and a reopen gives identical state
- [ ] Writes and deletes made after a snapshot survive a reopen; snapshots can be repeated, taken with nothing new, or taken on an empty store
- [ ] A leftover `snapshot.tmp` full of garbage is ignored and removed by `Open`
- [ ] If the old log is put back next to a new snapshot (a crash before the log reset), replaying it on top still gives the right state
- [ ] A changed byte inside a value in `snapshot.db` makes `Open` fail
- [ ] 10 snapshots while 8 goroutines write: after a reopen every key holds its last acknowledged value and version
- [ ] `Snapshot` after `Close` is `store.ErrClosed`; after `Snapshot`, `LogLen` is 0 and versions and expiries are preserved
- [ ] `Memory.Snapshot` returns every live entry, sorted by key, as copies
- [ ] `gofmt -l .` and `go vet ./...` print nothing

## Reference

The exact contract the tests hold you to.

```go
// store/memory.go addition
// Snapshot returns a consistent copy of every live entry, taken under
// the read lock.
func (m *Memory) Snapshot() []Entry

// durable/durable.go additions
func (s *Store) Snapshot() error // after Close: store.ErrClosed

// Files in the data directory:
//   wal.log       the log
//   snapshot.db   the last complete snapshot: gob of []store.Entry,
//                 then a trailing crc32 of the gob bytes
//   snapshot.tmp  a snapshot being written; never trusted
// Snapshot: write snapshot.tmp, fsync it, rename over snapshot.db,
// fsync the directory, then reset the log (Truncate(0)), so LogLen
// returns to 0. Snapshot takes an exclusive lock that pauses writers
// while the entries are copied and the log is reset.
// Open: remove any snapshot.tmp, load snapshot.db if present (refuse on
// bad checksum), then replay wal.log on top.
```

| File | Holds | Trusted? |
|---|---|---|
| `wal.log` | change records since the last snapshot | yes (4.3 checks it) |
| `snapshot.db` | gob of `[]store.Entry` + 4-byte big-endian IEEE CRC32 of the gob bytes | only if the CRC matches |
| `snapshot.tmp` | a snapshot in progress | never; removed on `Open` |

Decisions the tests rely on:

- `Memory.Snapshot()` returns live entries sorted by key, expired ones excluded.
- `Snapshot` takes an exclusive lock that pauses writers while entries are copied and again while the log is reset; `LogLen` is 0 afterwards.
- `snapshot.db` is the gob encoding of `[]store.Entry` followed by a 4-byte big-endian IEEE CRC32 of the gob bytes.
- Resetting the log is `Truncate(0)`.

## The tests check

- 10,000 writes then Snapshot: wal.log is under 64 bytes, snapshot.db exists, and a reopen restores identical state
- Writes and deletes after a snapshot survive a reopen; repeated snapshots and a snapshot with nothing new work; a snapshot of an empty store works
- A leftover snapshot.tmp full of garbage is ignored and removed on Open
- Crash between rename and log reset: the old log is put back next to the new snapshot; replaying it on top still gives the right state
- A changed byte inside a stored value in snapshot.db (the file still decodes) makes Open fail: only a checksum can catch it
- 10 snapshots while 8 goroutines write: after reopen every key holds its last acknowledged value and version
- Snapshot after Close is store.ErrClosed; after Snapshot, LogLen is 0 and versions and expiries are preserved
- Memory.Snapshot returns every live entry sorted by key, as copies

## Try it

```bash
./burrow watch
go test -run 'TestL4_4_CrashBeforeLogReset' -v ./durable
```

Then do the crash test by hand: with a throwaway server on `durable.Open("./data")`, set a few keys, call `Snapshot` (a tiny program that opens the directory and calls it will do), `ls -l data` before and after, and `cat` the old `wal.log` back into place to see `Open` cope.

## In the wild

Redis RDB + AOF rewrite; etcd snapshots; the write-temp-then-rename idiom in every database and in `go build`'s own output.

## New in Go

os.Rename atomicity · syncing a directory · encoding/gob · crash-safety reasoning

Standard library: `os`, `path/filepath`, `encoding/gob`, `hash/crc32`
