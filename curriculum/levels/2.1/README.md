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
- A fuzz target that ReadRecord never panics on

Decisions the tests rely on:

- `ReadRecord` of a zero-length record returns an empty, non-nil slice.
- `AppendRecord` errors only for an oversized payload.

## The tests check

- Golden: WriteRecord of `hi` is exactly 00 00 00 02 68 69; of empty payload is 00 00 00 00
- Round trips of random binary payloads (0 bytes, 1 byte, 1 MiB, exactly MaxRecordSize), and ten records back to back in one buffer
- Readers from testing/iotest: OneByteReader, DataErrReader (data together with EOF), HalfReader
- EOF: empty stream is io.EOF itself; a stream cut after 1, 3, 4 and 4+n bytes is io.ErrUnexpectedEOF
- A header claiming 0xFFFFFFFF bytes returns ErrRecordTooLarge and testing.AllocsPerRun reports 0 large allocations; a length of MaxRecordSize+1 is rejected, MaxRecordSize accepted
- WriteRecord calls Write exactly once (a counting writer) and reports io.ErrShortWrite for a writer that accepts fewer bytes; a writer error is returned as-is
- The slice ReadRecord returns is independent of the reader's buffer (mutating a bytes.Reader's backing array afterwards doesn't change it)
- AppendRecord of three payloads reads back as three records; AppendRecord rejects oversized payloads
- FuzzL2_1_ReadRecord: never panics, and any successful read re-encodes to a prefix of the input

## Try it

```bash
go test -fuzz=FuzzL2_1 ./wire   # for as long as you like
```

## Uses

Nothing from earlier levels. This is a root part.

## In the wild

protobuf length-delimited streams, gRPC's 5-byte message frame, Kafka record batches, Redis RESP bulk strings: every binary protocol has this part

## New in Go

encoding/binary · io.ReadFull · io.ErrUnexpectedEOF vs io.EOF · defensive limits · testing/iotest · fuzzing · append patterns

Standard library: `encoding/binary`, `io`, `bytes`, `testing`, `testing/iotest`
