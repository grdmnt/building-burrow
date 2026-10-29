# 3.2 · Parity Bits

Disks and crashes produce torn and damaged records. Detect them instead of trusting the bytes.

## Build

```go
var ErrCorrupt = errors.New("wal: corrupt")
```

Add a CRC32 checksum to every record. `Open` now scans the log:

- **Torn tail:** the final record is incomplete or fails its checksum, or the file ends in zeros or garbage. That's what a crash mid-write looks like. Truncate the file back to the last good record and carry on.
- **Corruption:** a bad record with good records after it. Return an error matching `ErrCorrupt` and **don't modify the file**. Someone needs to look at it.

## The tests check

- The last record cut at every possible byte offset: opens fine, 4 of 5 records come back, the file is truncated to exactly the last good boundary, and new appends land after it.
- A flipped byte inside the last record is treated as a torn tail.
- A flipped byte in a middle record is `ErrCorrupt`, and the file is untouched.
- Flipping each bit of a record in turn never produces a record that differs from what was written. The checksum has to cover the length too.
- 4 KiB of zeros after the last record must not turn into phantom empty records. (CRC32 of nothing is 0.)
- A garbage length of `0xffffffff` doesn't allocate gigabytes.
- Empty records survive a reopen.

## New in Go

`hash/crc32` · `File.Truncate` · `io.SectionReader` / `ReadAt` · reasoning about crash states
