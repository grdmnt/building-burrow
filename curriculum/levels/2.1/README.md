# 2.1 · Record

Bytes with a length in front. This small codec is the part the wire protocol, the write-ahead log, cluster RPC and the queue broker are all built on, so its EOF rules and limits need to be exact.

## Build

```go
// wire/record.go
package wire

const MaxRecordSize = 16 << 20 // payload bytes

var ErrRecordTooLarge = errors.New("wire: record too large")

// Layout: uint32 big-endian payload length, then the payload.

// WriteRecord writes the length and payload with exactly one call to w.Write and
// returns io.ErrShortWrite if w accepted fewer bytes than given.
func WriteRecord(w io.Writer, payload []byte) error

// ReadRecord reads one record. An empty stream returns (nil, io.EOF); EOF anywhere
// after the first byte returns io.ErrUnexpectedEOF. A length over MaxRecordSize
// returns ErrRecordTooLarge without allocating. The returned slice is freshly allocated.
func ReadRecord(r io.Reader) ([]byte, error)

// AppendRecord appends the encoded record to buf and returns the extended slice,
// for writers that batch several records into one Write (4.5 group commit).
func AppendRecord(buf []byte, payload []byte) ([]byte, error)
```

- `WriteRecord`, `ReadRecord`, `AppendRecord`
- `MaxRecordSize` and `ErrRecordTooLarge`, checked before any allocation
- A fuzz target of your own in `wire/` (name it `FuzzRecord` or similar, not `FuzzL…`) with a seed corpus, that ReadRecord and the round trip never panic on

Slices, properly, because this level hands them out and takes them in:

- An array `[4]byte` is a value; a slice `[]byte` is a window (pointer, length, capacity) onto an array. Assigning or passing a slice copies the window, not the bytes, so two slices can alias the same memory. `ReadRecord` must return a slice that does not alias any buffer you keep, which is what `make` plus `copy` (or `io.ReadFull` into a fresh slice) gives you.
- `append` writes into spare capacity when there is any, which is how `AppendRecord` extends `buf` in place and also how a caller's second `append` can overwrite your first. The three-index slice `s[a:b:c]` sets the capacity to `c-a`, so `s[:len(s):len(s)]` forces the next `append` to allocate. `bytes.Clone` is the one-liner for a defensive copy.
- `binary.BigEndian.PutUint32(hdr[:], n)` on a `var hdr [4]byte` is the idiomatic header: an array on the stack, sliced only to pass it.

Decisions the tests rely on:

- `ReadRecord` of a zero-length record returns an empty, non-nil slice.
- `AppendRecord` errors only for an oversized payload.
- Your fuzz target lives in `wire/` and its name must not start with `FuzzL` (reserved for provided suites). It adds at least one seed with `f.Add`, passes its seed corpus under plain `go test`, and survives two seconds of `go test -fuzz` without a crasher. Fuzz whatever you like: ReadRecord never panicking on arbitrary bytes is the obvious one; a round trip through AppendRecord and ReadRecord is better.

## The tests check

- Golden: WriteRecord of `hi` is exactly 00 00 00 02 68 69; of an empty payload is 00 00 00 00
- Round trips of random binary payloads (0 bytes, 1 byte, 1 MiB, exactly MaxRecordSize), and several records back to back in one buffer
- Readers from testing/iotest: OneByteReader, HalfReader, DataErrReader (data together with EOF)
- EOF: an empty stream is io.EOF itself (not wrapped); a record cut at every offset after its first byte is io.ErrUnexpectedEOF
- Headers claiming 0xFFFFFFFF and MaxRecordSize+1 bytes return ErrRecordTooLarge with at most a handful of allocations (testing.AllocsPerRun); a record of exactly MaxRecordSize reads back
- WriteRecord over the limit is ErrRecordTooLarge and writes nothing
- WriteRecord calls Write exactly once (a counting writer), reports io.ErrShortWrite for a writer that accepts half, and returns a writer's error as-is
- The slice ReadRecord returns is independent of the reader's buffer
- AppendRecord of three payloads (one empty) reads back as three records; AppendRecord over the limit returns ErrRecordTooLarge and leaves buf unchanged
- FuzzL2_1_ReadRecord: never panics, and any successful read re-encodes to a prefix of the input
- TestL2_1_YourFuzzTarget: a fuzz target of yours exists in wire/ (name not starting with FuzzL), passes its seed corpus, and runs for 2 s under `go test -fuzz` without finding a crasher

## Try it

```bash
go test -fuzz=FuzzL2_1 ./wire            # for as long as you like
go test -fuzz=FuzzRecord -fuzztime=30s ./wire   # yours; crashers land in wire/testdata/fuzz/
```

## Uses

Nothing from earlier levels. This is a root part.

## In the wild

protobuf length-delimited streams, gRPC's 5-byte message frame, Kafka record batches, Redis RESP bulk strings: every binary protocol has this part

## New in Go

encoding/binary · io.ReadFull · io.ErrUnexpectedEOF vs io.EOF · defensive limits · arrays vs slices · copy and bytes.Clone · append and capacity · three-index slicing s[a:b:c] · testing/iotest · writing a fuzz target

Standard library: `encoding/binary`, `io`, `bytes`, `testing`, `testing/iotest`
