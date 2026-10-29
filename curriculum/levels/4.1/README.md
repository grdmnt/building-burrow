# 4.1 · Write-Ahead Log

Survive a crash by writing every change to an append-only log before acknowledging it. The log is a sequence of records from 2.1, and replay is an iterator rather than a callback.

## Build

```go
// wal/wal.go
package wal

// MaxRecordSize leaves 4 bytes of each wire Record for the checksum 4.3 adds, so the
// constant never changes.
const MaxRecordSize = wire.MaxRecordSize - 4

var (
	ErrClosed         = errors.New("wal: closed")
	ErrRecordTooLarge = errors.New("wal: record too large")
)

type Option func(*options) // used from 4.5; accept ...Option now

func Open(path string, opts ...Option) (*Log, error) // creates the file if needed; the directory must exist

// Append writes rec as one wire Record and returns only once it is fsynced.
func (l *Log) Append(rec []byte) error

// Records iterates every record from the start, in order. The yielded slice is valid
// only until the next iteration step. If reading fails the final yield carries the error.
// Breaking out of the range loop stops reading.
func (l *Log) Records() iter.Seq2[[]byte, error]

func (l *Log) Close() error // second Close returns nil
```

- `wal.Open`, `Append`, `Records`, `Close`
- Records as an `iter.Seq2` using `wire.ReadRecord` on an `io.SectionReader` so replay doesn't move the write position
- fsync after every Append

Decisions the tests rely on:

- `MaxRecordSize` is `wire.MaxRecordSize - 4` from the start, so the 4-byte checksum 4.3 adds never changes the limit.
- On a read error `Records` yields `(nil, err)` once and stops. After `Close` it yields `(nil, ErrClosed)` once.
- `Records` reads up to the file size at the moment it is called; appends made during iteration are not included.
- The tests never assert what a torn final record yields here: 4.3 changes that behaviour, and every level's tests keep passing at later levels.

## The tests check

- Records come back in order, including empty, binary and 1 MiB records; reopening appends after what's there
- Records always starts from the beginning and sees every append; iterating twice gives the same sequence
- `for rec, err := range l.Records()`: break after the second record stops reading (a counting reader proves it); after Close, Records yields (nil, ErrClosed) once and stops
- The yielded slice may be reused: a test that keeps the slices and compares later must copy them, and the suite documents this by checking only copies
- Append copies what it's given: mutating rec after Append doesn't change the log
- 20 goroutines appending at once never interleave bytes (every record decodes to one of the inputs)
- Record over MaxRecordSize (wire.MaxRecordSize - 4) is ErrRecordTooLarge and the file is unchanged; a record of exactly MaxRecordSize works; after Close, Append returns ErrClosed
- Open on a path in a missing directory fails; the file exists on disk after the first Append
- kill -9: the test re-runs itself as a child (TestL4_1_zHelperWriter) that appends records and prints each one once Append returns; the parent SIGKILLs it mid-stream and every printed record is in the log

## Uses

- Record codec (built in 2.1 · Record)

## In the wild

etcd's wal package, RocksDB's WAL, Postgres XLOG; iter.Seq2 (Go 1.23) is how new std APIs expose sequences, e.g. maps.Keys and bufio.Scanner's successor patterns

## New in Go

iter.Seq2 and range over functions · os.OpenFile flags · File.Sync · io.SectionReader · re-running the test binary as a helper process

Standard library: `os`, `io`, `iter`, `bufio`
