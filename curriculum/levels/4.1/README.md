# 4.1 · Write-Ahead Log

> Memory forgets the moment the power goes. Paper doesn't. Write it down before you say "done".

## Your goal

Everything in Burrow so far lives in RAM: kill the process and every key is gone. The fix is a *write-ahead log*, or WAL: an append-only file where each change is written *ahead* of being acknowledged. If the record is on disk before `Append` returns, a crash a millisecond later loses nothing, because on restart you can read the file from the top and replay what happened.

This level builds just the log, in a package called `wal`. It has three operations: `Append` a record and don't return until it is physically on disk, `Records` to read them all back in order, and `Close`. The next level wires it under the store.

## What you're using from before

**Record codec** (2.1 · Record). A log file is nothing more than wire records back to back: a 4-byte length, then the bytes, repeated. `WriteRecord` writes one, `ReadRecord` reads one and reports `io.EOF` when the file is exhausted:

```go
err := wire.WriteRecord(l.file, rec)          // Append
rec, err := wire.ReadRecord(r)                // Records, in a loop
if errors.Is(err, io.EOF) { /* end of log */ }
```

## What's new here

**Opening a file for appending.** `os.OpenFile` takes flags that say how you want the file: `os.O_RDWR` to read and write, `os.O_CREATE` to create it if missing, `os.O_APPEND` so every write lands at the end. The last argument is the permission bits for a new file:

```go
f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o644)
```

Opening a path whose directory doesn't exist fails, and so does opening a directory as if it were a file. Both are errors `Open` should return as they are.

**`Write` is not "on disk".** When `f.Write` returns, the bytes are in the operating system's cache, not on the platter or the flash. A process crash (`kill -9`) keeps them, since the OS is still running, but a power cut loses them. `f.Sync()` is the call that waits until the OS has pushed them to the device. It is slow, on the order of milliseconds, and it is the whole contract of a WAL:

```go
if _, err := f.Write(data); err != nil { return err }
return f.Sync() // only now may Append return
```

Writes from several goroutines must not interleave inside a record, so `Append` holds a mutex from the write through the sync.

**Reading without disturbing the writer.** Reading the file through the same `*os.File` would move its offset around. `io.NewSectionReader(f, 0, size)` gives you an `io.Reader` over a fixed byte range of the file, using `ReadAt` underneath, so replay has its own position and the writer's is untouched. `f.Stat()` gives you `size`. Wrap the section reader in a `bufio.Reader` so `ReadRecord` doesn't make a system call per 4-byte header.

**Iterators: `iter.Seq2` and `range` over a function.** Since Go 1.23 a `for ... range` loop can run over a function. The function takes a `yield` callback, calls it once per element, and must stop as soon as `yield` returns `false` (that is the loop body saying `break`). `iter.Seq2[K, V]` is the two-value version:

```go
func countdown(n int) iter.Seq2[int, string] {
	return func(yield func(int, string) bool) {
		for i := n; i > 0; i-- {
			if !yield(i, "...") {
				return // the caller broke out of the loop
			}
		}
	}
}

for i, s := range countdown(3) {
	fmt.Println(i, s) // 3 ..., 2 ..., 1 ...
}
```

For the log, the two values are `([]byte, error)`. A read error is delivered as the last element: `yield(nil, err)` once, then return. The caller sees it at the usual place, inside the loop:

```go
for rec, err := range l.Records() {
	if err != nil {
		return err
	}
	// use rec now; it is only valid until the next iteration
}
```

If your iterator keeps calling `yield` after it returned `false`, the runtime panics with "range function continued iteration", so the `return` after a false `yield` is not optional.

**A test that re-runs itself as a child process.** You can't `kill -9` the test you are in. `TestL4_1_KillNine` starts the test binary again (`os.Args[0]`) with `-test.run` naming a helper test and an environment variable carrying the log path. The helper appends records and prints `acked N` after each `Append` returns. The parent kills the child mid-stream and checks that every acknowledged record is in the file. If `Append` returned before `Sync`, some would be missing. You don't write any of this, but it is worth recognising: it is the standard Go pattern for testing crashes and `os.Exit`.

## What to do

1. Create `wal/wal.go` with the constants and errors from the Reference, a `Log` struct holding the `*os.File`, a mutex and a closed flag, and the `Option` type (unused for now; `Open` must accept `...Option`).
2. Write `Open`: `os.OpenFile` with read/write, create and append; return a `*Log`.
3. Write `Append`:
   - Return `ErrClosed` if closed and `ErrRecordTooLarge` if `len(rec) > MaxRecordSize`, before touching the file.
   - Under the mutex: `wire.WriteRecord` to the file, then `Sync`. Return only after both.
4. Write `Records` as an `iter.Seq2[[]byte, error]`:
   - If closed, yield `(nil, ErrClosed)` once and return.
   - Take the file size now; build a buffered `io.SectionReader` from 0 to that size.
   - Loop on `wire.ReadRecord`: `io.EOF` ends the loop; any other error is yielded once with a nil record, then stop; a record is yielded, and if `yield` returns `false`, stop.
5. Write `Close`: close the file, set the flag, return `nil` on a second call.
6. Run `gofmt -l .` and `go vet ./...` until both print nothing.

## Example

```go
l, err := wal.Open("/tmp/demo/wal.log") // the directory must exist
l.Append([]byte("first"))
l.Append([]byte("second")) // each returns only after fsync

for rec, err := range l.Records() {
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s\n", rec)
}
// first
// second

l.Close()
l.Append([]byte("x")) // wal.ErrClosed
```

Reopen the same path later and the two records are still there; new appends go after them. On disk the file is the raw records:

```text
00 00 00 05 66 69 72 73 74  00 00 00 06 73 65 63 6f 6e 64
|  len 5   | f  i  r  s  t |  len 6    | s  e  c  o  n  d
```

## Done when

- [ ] Records come back in the order they were appended, including an empty one, binary ones and a 1 MiB one; a new log yields nothing
- [ ] Reopening a log keeps what was there and appends after it
- [ ] `Records` always starts from the top and sees every append so far; iterating twice gives the same sequence
- [ ] `break` inside the range loop stops the iterator without a panic
- [ ] Writing into a yielded slice doesn't change the log; changing a slice after `Append` doesn't either
- [ ] An oversized record is `ErrRecordTooLarge` and the file is untouched
- [ ] After `Close`: `Append` is `ErrClosed`, `Records` yields `(nil, ErrClosed)` once, a second `Close` is `nil`
- [ ] `Open` fails for a missing directory or a directory path; after `Append` returns the bytes are in the file
- [ ] 20 goroutines appending at once never interleave inside a record
- [ ] A child process killed with `kill -9` mid-stream loses no acknowledged record
- [ ] `gofmt -l .` and `go vet ./...` print nothing

## Reference

The exact contract the tests hold you to.

```go
// wal/wal.go
package wal

// MaxRecordSize leaves 4 bytes of each wire Record for the checksum 4.3
// adds, so the constant never changes.
const MaxRecordSize = wire.MaxRecordSize - 4

var (
	ErrClosed         = errors.New("wal: closed")
	ErrRecordTooLarge = errors.New("wal: record too large")
)

type Option func(*options) // used from 4.5; accept ...Option now

// Open creates the file if needed; the directory must exist.
func Open(path string, opts ...Option) (*Log, error)

// Append writes rec as one wire Record and returns only once it is
// fsynced.
func (l *Log) Append(rec []byte) error

// Records iterates every record from the start, in order. The yielded
// slice is valid only until the next iteration step. If reading fails
// the final yield carries the error. Breaking out of the range loop
// stops reading.
func (l *Log) Records() iter.Seq2[[]byte, error]

func (l *Log) Close() error // second Close returns nil
```

Decisions the tests rely on:

- `MaxRecordSize` is `wire.MaxRecordSize - 4` from the start, so the 4-byte checksum 4.3 adds never changes the limit.
- On a read error `Records` yields `(nil, err)` once and stops. After `Close` it yields `(nil, ErrClosed)` once.
- `Records` reads up to the file size at the moment it is called; appends made during iteration are not included.
- The tests never assert what a torn final record yields here: 4.3 changes that behaviour, and every level's tests keep passing at later levels.

## The tests check

- 104 records (including an empty one, one with NUL and newline bytes, and a 1 MiB one) come back in order; a new log yields nothing; reopening appends after what's there
- Records always starts from the beginning and sees every append; iterating twice gives the same sequence
- `for rec, err := range l.Records()`: breaking after the second of 1000 records stops the loop (Go panics if the iterator keeps calling yield)
- Scribbling on the yielded slice doesn't change the log; mutating a slice after Append doesn't either
- Record over MaxRecordSize is ErrRecordTooLarge and the file is unchanged
- After Close: Append is ErrClosed, Records yields (nil, ErrClosed) exactly once, a second Close returns nil
- Open in a missing directory fails; Open on a directory fails; after Append returns the record is already in the file
- 20 goroutines x 50 appends never interleave bytes (every record decodes to one of the inputs)
- kill -9: the test re-runs itself as a child (TestL4_1_zHelperAppender) that appends records and prints each one once Append returns; the parent SIGKILLs it mid-stream and every printed record is in the log

## Try it

```bash
./burrow watch
go test -run 'TestL4_1_KillNine' -v ./wal     # watch the crash test
```

To see what fsync costs, time 1000 appends with and without the `Sync` call (temporarily). The difference is the price of the durability guarantee.

## In the wild

etcd's wal package, RocksDB's WAL, Postgres XLOG; iter.Seq2 (Go 1.23) is how new standard library APIs expose sequences.

## New in Go

iter.Seq2 and range over functions · os.OpenFile flags · File.Sync · io.SectionReader · re-running the test binary as a helper process

Standard library: `os`, `io`, `iter`, `bufio`
