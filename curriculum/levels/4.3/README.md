# 4.3 · Parity Bits

Disks and crashes produce torn and damaged records. Detect them instead of trusting the bytes, and while you are indexing the log, expose `Len` and `Truncate(n)`: Raft will cut the log on conflict in 10.6.

## Build

```go
// wal/wal.go additions
var ErrCorrupt = errors.New("wal: corrupt")

// On disk each wire Record's payload is now: crc32 (IEEE, big-endian, over rec) | rec.
// That is why MaxRecordSize is wire.MaxRecordSize - 4: the record at the limit still fits.
// A payload shorter than 4 bytes, a zero length, or a checksum mismatch is a bad record.
// Open scans the whole log:
//   torn tail  - the last record is bad or incomplete, or the file ends in zeros/garbage:
//                truncate back to the last good record and continue.
//   corruption - a bad record followed by good records: return ErrCorrupt, file untouched.

func (l *Log) Len() int               // number of records currently in the log
func (l *Log) Truncate(n int) error    // keep the first n records, discard the rest, fsync; n > Len is an error
```

- CRC32 inside each record payload; verify on read
- Open scans, classifies torn tail vs corruption, repairs the tail
- `Len` and `Truncate(n)`, maintaining a record index (offsets) built during the scan
- Your own tests in `wal/` for corruption, tail repair and Truncate, on `t.TempDir()`; graded by mutation as in 0.2

Decisions the tests rely on:

- The CRC is IEEE over `rec`, stored big-endian in the 4 bytes before `rec`, inside the record payload.
- `Len` counts only good records; `Open` repairs the tail before `Len` is computed. `Truncate(n)` with `n < 0` or `n > Len()` is an error.
- Your tests (names not starting with `TestL` plus a digit or capital) pass on your code, create every log file under `t.TempDir()` (never in the package directory, never in `os.TempDir()` by hand), and at least one of them fails against each of three mutants: (1) corruption in the middle of the log is silently truncated away instead of returning `ErrCorrupt`, (2) torn-tail repair drops one extra good record, (3) `Truncate(n)` keeps `n+1` records. Write the file, damage the bytes with `os.WriteFile` or `os.OpenFile` + `WriteAt`, reopen, and assert on `Len` and the error.

## The tests check

- The last of 5 records cut at every possible byte offset: Open succeeds, 4 records come back, the file is truncated to exactly the last good boundary, and new appends land after it
- A flipped byte inside the last record is treated as a torn tail; a flipped byte in a middle record is ErrCorrupt and the file bytes are unchanged
- Flipping each bit of the middle record of three, header included, never yields a record that differs from what was written or an extra record
- 4 KiB of zeros after the last record do not become phantom records; an empty record survives a reopen
- 16 bytes of 0xff after the last record is a torn tail, and Open allocates well under 64 MB handling it (runtime.MemStats)
- Len is 0 for a new log and 5 after 5 appends; Truncate(3) leaves 3; appends after Truncate are visible; Truncate past the end and Truncate(-1) are errors; after a reopen Len is right; Truncate(0) leaves an empty file; Truncate after Close is ErrClosed
- Len counts good records after the torn tail is repaired
- Truncate is durable: a child process truncates to 6 records, prints `done`, and is SIGKILLed; the parent sees exactly 6
- TestL4_3_YourTestsCatchBugs: your wal tests use t.TempDir(), pass on your code, and fail against each of the three mutants above (mid-log corruption silently truncated, tail repair drops one good record, Truncate keeps n+1)

## Uses

- WAL (built in 4.1 · Write-Ahead Log)

## In the wild

etcd's wal records carry a CRC; Kafka segments; SQLite's WAL frames have checksums; Raft's log truncation on AppendEntries conflict (section 5.3)

## New in Go

hash/crc32 · File.Truncate · File.WriteAt · classifying failure modes · indexes built on scan · t.TempDir · testing your own failure paths

Standard library: `hash/crc32`, `os`, `io`
