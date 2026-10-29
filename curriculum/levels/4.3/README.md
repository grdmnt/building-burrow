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

Decisions the tests rely on:

- The CRC is IEEE over `rec`, stored big-endian in the 4 bytes before `rec`, inside the record payload.
- `Len` counts only good records; `Open` repairs the tail before `Len` is computed. `Truncate(n)` with `n < 0` or `n > Len()` is an error.

## The tests check

- The last of 5 records cut at every possible byte offset: Open succeeds, 4 records come back, the file is truncated to exactly the last good boundary, and new appends land after it
- A flipped byte inside the last record is treated as a torn tail; a flipped byte in a middle record is ErrCorrupt and the file bytes are unchanged
- Flipping each bit of a 64-byte record in turn never yields a record that differs from what was written (length bits included)
- 4 KiB of zeros after the last record do not become phantom empty records; an empty record (zero-length rec, 4-byte payload) survives a reopen
- A garbage length of 0xFFFFFFFF doesn't allocate gigabytes (AllocsPerRun / time bound)
- Len counts records after Open, Append and Truncate; Truncate(3) of 5 leaves exactly 3 in Records and on disk after reopen; Truncate(0) empties; Truncate(Len+1) errors; appends after Truncate are visible
- Truncate is durable: a child process truncates, prints `done`, and is SIGKILLed; the parent sees exactly n records

## Uses

- WAL (built in 4.1 · Write-Ahead Log)

## In the wild

etcd's wal records carry a CRC; Kafka segments; SQLite's WAL frames have checksums; Raft's log truncation on AppendEntries conflict (section 5.3)

## New in Go

hash/crc32 · File.Truncate · classifying failure modes · indexes built on scan

Standard library: `hash/crc32`, `os`, `io`
