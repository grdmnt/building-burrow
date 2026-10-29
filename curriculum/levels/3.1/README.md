# 3.1 · Write-Ahead Log

Survive a crash by writing every change to an append-only log before acknowledging it.

## Build

```go
// wal/wal.go
const MaxRecordSize = 16 << 20

var (
	ErrClosed         = errors.New("wal: closed")
	ErrRecordTooLarge = errors.New("wal: record too large")
)

type Option func(*options) // used from 3.4; accept ...Option now

func Open(path string, opts ...Option) (*Log, error) // creates the file if needed; the directory must exist
func (l *Log) Append(rec []byte) error               // returns only once rec is in the file and fsynced
func (l *Log) Replay(fn func(rec []byte) error) error // every record from the start, in order
func (l *Log) Close() error                          // second Close returns nil
```

```go
// durable/durable.go
// Open creates dir if needed. The log lives at <dir>/wal.log.
func Open(dir string) (*Store, error)
// *Store implements store.Store (Get, Set, Delete, Keys) plus Close.
```

The durable store is a Memory store plus a log: validate, append the change, then apply it. On `Open`, replay the log to rebuild memory. Encoding each change as a `wire.Frame` works well.

## The tests check

- Records come back in order, including empty, binary and 1 MiB records; reopening appends after what's there.
- `Replay` always starts from the beginning and sees every append, and is repeatable. A callback error stops it and is returned.
- The log copies what it's given and isn't hurt by callbacks that scribble on their slice.
- 20 goroutines appending at once never interleave records.
- `durable.Store` passes `storetest.Run` **and** `RunConcurrent`.
- State after a restart matches exactly: overwrites, deletes, empty and binary values.
- Rejected writes, reads, and deletes of missing keys don't grow the log.
- **kill -9:** a child process writes and prints each acknowledged key; it's killed with SIGKILL mid-stream; every acknowledged key must survive. (`TestL3_1_zHelperWriter` is that child; it skips when run normally.)

## New in Go

`os.OpenFile` flags · `File.Sync` · `encoding/binary` again · re-running the test binary as a helper process
