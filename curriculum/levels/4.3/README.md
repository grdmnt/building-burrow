# 4.3 · Parity Bits

> A parity bit can't fix a flipped bit, but it can tell you one flipped. Knowing beats guessing.

## Your goal

The log trusts every byte it reads. Real files don't deserve that: a crash in the middle of a write leaves a half-record at the end of the file (a *torn tail*), a disk or a stray process can change bytes in the middle (*corruption*), and a filesystem can leave zeros or garbage after the last real write. This level makes the log notice. Every record gets a checksum, `Open` scans the whole file and sorts bad records into "torn tail: repair it" or "corruption: refuse to open", and while it is scanning it builds an index of where every record starts.

That index is worth exposing. `Len` says how many records are in the log and `Truncate(n)` cuts it back to the first `n`. You also write your own tests for the failure paths, graded by mutation like in 0.2.

## What you're using from before

**WAL** (4.1 · Write-Ahead Log). Same package, same three methods, with the checksum folded into each one. The on-disk shape of a record changes from `rec` to `crc32 | rec`, which is why `MaxRecordSize` was `wire.MaxRecordSize - 4` from the start:

```go
// Append: checksum first, then one wire record holding both
payload := binary.BigEndian.AppendUint32(nil, crc32.ChecksumIEEE(rec))
payload = append(payload, rec...)
err := wire.WriteRecord(l.file, payload)

// Records: read, split, verify, then yield rec
```

## What's new here

**`hash/crc32`.** A CRC is a 32-bit fingerprint of a byte string. Change any bit of the input and the CRC changes, with overwhelming probability. `crc32.ChecksumIEEE(b)` is the common polynomial, and it is fast enough to run on every record:

```go
sum := crc32.ChecksumIEEE([]byte("hello")) // 0x3610a686, a uint32
```

Store it big-endian in the 4 bytes before the record. On read, recompute over the bytes you got and compare. A mismatch means the bytes are not what was written. One trap: the CRC of nothing is 0, so a run of zeros where a record should be (`length 0`) must be rejected as a bad record before you even look at a checksum. A payload shorter than 4 bytes is bad for the same reason: there is no room for a CRC.

**Classifying failure modes.** Not every bad record means the same thing, and the response has to match:

| What you find | What it means | What to do |
|---|---|---|
| A bad or incomplete record, and nothing but zeros or garbage after it | the last write was interrupted (torn tail) | truncate the file back to the last good record and carry on |
| A bad record with good records after it | something changed bytes in the middle (corruption) | return `ErrCorrupt`, leave the file alone for a human |

A torn tail is normal after a crash and must heal itself. Corruption is not, and silently dropping the good records after it would lose data someone was told was safe.

**An index built on scan.** `Open` already has to read every record to verify it, so note each record's end offset in a slice as you go. That slice is the index: `Len()` is its length, record `i` runs from `offsets[i-1]` (or 0) to `offsets[i]`, and `Truncate(n)` is `f.Truncate(offsets[n-1])` (or `0`). Append pushes a new offset; `Records` can keep using the section reader.

**`File.Truncate` and `File.WriteAt`.** `f.Truncate(size)` cuts the file to `size` bytes (or extends it with zeros). Follow it with `Sync`: truncation is a change to the file like any other and must reach the disk before `Truncate` returns. `f.WriteAt(b, off)` writes at an absolute offset without moving the file's position; you won't need it in the log, but it is the tool your own tests use to flip a byte in the middle of a file.

**Garbage lengths.** A torn or garbage header can claim a length of 4 GB. `wire.ReadRecord` already refuses anything over `wire.MaxRecordSize` before allocating, with `ErrRecordTooLarge`. Treat that error during the scan as a bad record, not as a crash.

**Testing your own failure paths.** The provided tests check your log; your tests check that *you* checked. Build a log, read the file bytes, damage them (cut the file, flip a byte, append junk), write it back, reopen, and assert on `Len` and the error. `t.TempDir()` gives every test its own directory that is removed afterwards, so nothing leaks between tests or into the package directory:

```go
path := filepath.Join(t.TempDir(), "wal.log")
// ... build the log, close it ...
data, _ := os.ReadFile(path)
data[len(data)/2] ^= 0x01 // flip one bit in the middle
os.WriteFile(path, data, 0o644)
_, err := wal.Open(path)
// errors.Is(err, wal.ErrCorrupt) should be true
```

As in 0.2, the grader plants three bugs in your log one at a time and runs your tests against each; at least one test must fail for every bug.

## What to do

1. Add `ErrCorrupt`, an offsets slice to `Log`, and the two new methods from the Reference.
2. Change `Append` to write `crc32 | rec` as the record payload, and to push the new file size onto the offsets slice.
3. Change `Records` to split each payload, verify the checksum, and yield only `rec`. A bad record is a read error (yield it once, stop).
4. Make `Open` scan the file from the top:
   - Read records one by one, noting each good record's end offset.
   - On the first bad record (short payload, zero length, checksum mismatch, `ErrRecordTooLarge`, or `io.ErrUnexpectedEOF`), decide: if it runs into the end of the file, or it is the last record, or everything after it is zeros, it is a torn tail: `Truncate` the file to the last good offset, `Sync`, and continue. Otherwise return `ErrCorrupt` and change nothing.
5. Write `Len` and `Truncate(n)`: reject `n < 0` or `n > Len()`; otherwise `f.Truncate` to the right offset, `Sync`, and cut the index. After `Close` it is `ErrClosed`.
6. Write your own tests in `wal/` (names not starting with `TestL`), every file under `t.TempDir()`: corruption in the middle is `ErrCorrupt`, a torn tail repairs to exactly the good records, and `Truncate(n)` leaves exactly `n`.
7. Run `gofmt -l .` and `go vet ./...` until both print nothing.

## Example

```go
l, _ := wal.Open(path)
for i := range 5 {
	l.Append([]byte(fmt.Sprintf("r%d", i)))
}
l.Len()      // 5
l.Truncate(3)
l.Len()      // 3: r0, r1, r2 remain, and the file is cut to match
l.Close()

// simulate a crash mid-write: chop the last few bytes off the file
data, _ := os.ReadFile(path)
os.WriteFile(path, data[:len(data)-3], 0o644)
l, err := wal.Open(path) // err == nil
l.Len()                  // 2: torn record gone, file repaired

// simulate corruption: flip a byte in the first record
data, _ = os.ReadFile(path)
data[6] ^= 0xff
os.WriteFile(path, data, 0o644)
_, err = wal.Open(path)
// errors.Is(err, wal.ErrCorrupt), file untouched
```

On disk, one record holding `hi` now looks like this:

```text
00 00 00 06  xx xx xx xx  68 69
| len 6    | crc32(hi)  | h  i
```

## Done when

- [ ] A last record cut at any byte opens fine, yields the earlier records, truncates the file to the last good boundary, and new appends land after it
- [ ] A flipped byte in the last record is repaired as a torn tail; a flipped byte in a middle record is `ErrCorrupt` and the file is left as it was
- [ ] No single bit flip anywhere in a record, header included, produces a wrong or extra record
- [ ] Zeros or `0xff` bytes after the last record are not records, and a garbage length doesn't make `Open` allocate gigabytes
- [ ] An empty record survives a reopen
- [ ] `Len` counts records (0 on a new log, good ones only after a repair); `Truncate(n)` keeps the first `n`, appends after it work, `Truncate` past the end or with `-1` is an error, `Truncate(0)` leaves an empty file, after `Close` it is `ErrClosed`
- [ ] A `Truncate` in a process that is then killed with `kill -9` sticks
- [ ] Your own tests use `t.TempDir()`, pass, and catch all three planted bugs
- [ ] `gofmt -l .` and `go vet ./...` print nothing

## Reference

The exact contract the tests hold you to.

```go
// wal/wal.go additions
var ErrCorrupt = errors.New("wal: corrupt")

// On disk each wire Record's payload is now:
//   crc32 (IEEE, big-endian, over rec) | rec
// That is why MaxRecordSize is wire.MaxRecordSize - 4: the record at
// the limit still fits. A payload shorter than 4 bytes, a zero length,
// or a checksum mismatch is a bad record.
// Open scans the whole log:
//   torn tail  - the last record is bad or incomplete, or the file
//                ends in zeros/garbage: truncate back to the last good
//                record and continue.
//   corruption - a bad record followed by good records: return
//                ErrCorrupt, file untouched.

// Len is the number of records currently in the log.
func (l *Log) Len() int
// Truncate keeps the first n records, discards the rest and fsyncs;
// n > Len is an error.
func (l *Log) Truncate(n int) error
```

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

## Try it

```bash
./burrow watch
go test -run 'TestL4_3_EveryBitFlipIsCaught' -v ./wal
go test -skip 'TestL[0-9A-Z]' -v ./wal      # just your own tests
```

Open a log file in `xxd` (`xxd wal.log | head`) and find the length, the CRC and the bytes of each record. Then change one hex digit with a text editor that can edit binary, or `printf '\xff' | dd of=wal.log bs=1 seek=12 conv=notrunc`, and watch `Open` catch it.

## In the wild

etcd's wal records carry a CRC; Kafka segments; SQLite's WAL frames have checksums.

## New in Go

hash/crc32 · File.Truncate · File.WriteAt · classifying failure modes · indexes built on scan · t.TempDir · testing your own failure paths

Standard library: `hash/crc32`, `os`, `io`
