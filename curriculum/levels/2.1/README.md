# 2.1 · Record

> A stream of bytes has no edges. The first thing any protocol does is draw them: this many bytes, then the next lot.

## Your goal

Write the smallest possible binary codec: a *record* is a 4-byte length followed by that many bytes of payload. `WriteRecord` puts one on any `io.Writer`, `ReadRecord` takes one off any `io.Reader`, and `AppendRecord` builds one into a byte slice. It lives in a new package, `wire`, the home for everything about bytes on a connection.

Three things make this harder than it looks and are the whole point: a read from a network connection can return fewer bytes than you asked for, "the stream ended" means different things at different points, and a 4-byte length can claim 4 GiB, which you must refuse before allocating it. Every protocol Burrow speaks later sits on top of this one codec, so its rules need to be exact.

## What you're using from before

Nothing yet. This is a root part: `wire` does not import `store`.

## What's new here

**`io.Reader` and `io.Writer`.** You met `io.Writer` in 0.1; `io.Reader` is its mirror, `Read(p []byte) (n int, err error)`, which fills `p` with up to `len(p)` bytes. "Up to" matters: a TCP connection may hand you 3 of the 4 header bytes and the rest on the next call. `io.ReadFull(r, buf)` loops until `buf` is completely full, and translates a stream that ends early into a specific error:

```go
var hdr [4]byte
n, err := io.ReadFull(r, hdr[:])
// err == nil:                  all 4 bytes read
// err == io.EOF:               0 bytes read, the stream was over
// err == io.ErrUnexpectedEOF:  1-3 bytes read, then the stream ended
```

**`io.EOF` versus `io.ErrUnexpectedEOF`.** `io.EOF` means "a clean end, there was nothing more". For a record stream that is only true between records: a reader that gets 0 bytes when asking for a header has reached the end and should return `io.EOF` itself, unwrapped, so callers can test `err == io.EOF`. Any end after the first byte of a record is a truncated record, and the answer is `io.ErrUnexpectedEOF`. `io.ReadFull` gives you the header case for free; after a successful header, a short payload read is also `io.ErrUnexpectedEOF`, which `io.ReadFull` again reports for you.

**`encoding/binary`.** Multi-byte integers have to agree on byte order. Network protocols use big-endian (most significant byte first), and `binary.BigEndian` encodes and decodes it:

```go
var hdr [4]byte
binary.BigEndian.PutUint32(hdr[:], 258)   // hdr == [0 0 1 2]
n := binary.BigEndian.Uint32(hdr[:])      // 258

buf := binary.BigEndian.AppendUint32(nil, 2)   // []byte{0, 0, 0, 2}
buf = append(buf, "hi"...)                      // [0 0 0 2 'h' 'i']
```

**Arrays versus slices.** `[4]byte` is an array: a fixed-size value that lives inline, on the stack when local, and copies when assigned. `[]byte` is a slice: a header (pointer, length, capacity) onto some array. `hdr[:]` makes a slice over the array without copying, which is how you pass a stack array to a function that wants a slice. A header read into `var hdr [4]byte` costs no allocation at all.

**Defensive limits.** The length field is whatever the other side sent. `make([]byte, n)` with `n` from the wire is a one-line denial of service: a 4-byte header saying `0xFFFFFFFF` asks for 4 GiB. So check `n` against `MaxRecordSize` before `make`, and return `ErrRecordTooLarge` having allocated nothing. The test measures this with `testing.AllocsPerRun`.

**`append` and capacity.** `append(s, x...)` writes into `s`'s spare capacity if there is any, and otherwise allocates a bigger array and copies. That is how `AppendRecord` extends `buf` in place. It also means two slices that share an array can overwrite each other's appends:

```go
a := make([]byte, 0, 10)
a = append(a, 'x')
b := append(a, 'y')     // fits in a's capacity: shares the array
c := append(a, 'z')     // same slot as 'y'
string(b)               // "xz": c's append overwrote b's
```

`copy(dst, src)` copies bytes between slices, `bytes.Clone(s)` returns a fresh copy, and the three-index slice `s[a:b:c]` sets the capacity to `c-a`, so `s[:len(s):len(s)]` forces the next `append` to allocate rather than share. `ReadRecord` must return a slice that is nobody else's: a fresh `make` filled by `io.ReadFull` does it.

**`io.ErrShortWrite` and one `Write` per record.** `Write` returns how many bytes it accepted; fewer than you gave with no error is `io.ErrShortWrite` by convention. Build header and payload into one slice and call `Write` once, so that when several goroutines share a connection later, one record's header can never land between another's header and payload.

**`testing/iotest`.** The standard library ships readers that misbehave on purpose: `iotest.OneByteReader` returns one byte per `Read`, `iotest.HalfReader` returns half of what you asked, `iotest.DataErrReader` returns the final data together with `io.EOF` in the same call. The tests wrap your input in each; a `ReadRecord` built on `io.ReadFull` passes all three without special cases.

**Writing a fuzz target.** A fuzz target is a test that takes random inputs and checks a property that must hold for all of them, usually "doesn't panic" or "round-trips". `go test -fuzz` mutates the seed inputs you give with `f.Add` for as long as you let it, and any input that fails is saved under `testdata/fuzz/` so it replays as a normal test from then on:

```go
func FuzzParseAge(f *testing.F) {
    f.Add("42")                        // seed corpus
    f.Add("")
    f.Fuzz(func(t *testing.T, s string) {
        n, err := ParseAge(s)          // must never panic
        if err == nil && n < 0 {
            t.Fatalf("ParseAge(%q) = %d", s, n)
        }
    })
}
```

Under a plain `go test` only the seeds run. The round trip is the property worth fuzzing here: anything `ReadRecord` accepts, `WriteRecord` should turn back into a prefix of the original bytes.

## What to do

1. Create `wire/record.go` with `package wire`, `MaxRecordSize`, `ErrRecordTooLarge` and the three functions from the Reference.
2. Write `AppendRecord(buf, payload)`: if `len(payload) > MaxRecordSize` return `buf` unchanged and `ErrRecordTooLarge`; otherwise append the 4-byte big-endian length and then the payload to `buf`, and return the result.
3. Write `WriteRecord(w, payload)` on top of it: build the whole record in one slice, call `w.Write` exactly once, return the writer's error as-is, and return `io.ErrShortWrite` if `n` is less than the slice's length.
4. Write `ReadRecord(r)`:
   - read the 4-byte header with `io.ReadFull` into a `[4]byte`. Return `io.EOF` unchanged if that is what you got; any other error as-is (`io.ReadFull` already gives `io.ErrUnexpectedEOF` for a partial header).
   - decode the length; if it is over `MaxRecordSize`, return `ErrRecordTooLarge` before allocating anything.
   - `make` a slice of that length, fill it with `io.ReadFull`, and return it. A zero-length record returns an empty, non-nil slice.
5. Write your own fuzz target in `wire/`, named without the `FuzzL` prefix, with at least one `f.Add` seed. The round trip through `AppendRecord` and `ReadRecord` is the good one. Check it survives: `go test -fuzz=FuzzYourName -fuzztime=5s ./wire`.

## Example

```go
var buf bytes.Buffer
wire.WriteRecord(&buf, []byte("hi"))
buf.Bytes()                      // [0 0 0 2 'h' 'i'], hex 00000002 6869
wire.WriteRecord(&buf, nil)
buf.Bytes()                      // ...00000000 appended: empty record

p, err := wire.ReadRecord(&buf)  // p == []byte("hi"), err == nil
p, err = wire.ReadRecord(&buf)   // p == []byte{} (len 0, not nil), nil
p, err = wire.ReadRecord(&buf)   // err == io.EOF: clean end

// truncated: header says 7 bytes, only 3 arrive
r := bytes.NewReader([]byte{0, 0, 0, 7, 'p', 'a', 'y'})
_, err = wire.ReadRecord(r)      // io.ErrUnexpectedEOF

// a header alone, cut off
r = bytes.NewReader([]byte{0, 0})
_, err = wire.ReadRecord(r)      // io.ErrUnexpectedEOF

// hostile length, no allocation
r = bytes.NewReader([]byte{0xff, 0xff, 0xff, 0xff})
_, err = wire.ReadRecord(r)      // wire.ErrRecordTooLarge

out, _ := wire.AppendRecord(nil, []byte("a"))
out, _ = wire.AppendRecord(out, []byte("bc"))
// out == [0 0 0 1 'a' 0 0 0 2 'b' 'c']: two records, one slice
```

Fuzzing your own target:

```bash
$ go test -fuzz=FuzzRecord -fuzztime=5s ./wire
fuzz: elapsed: 0s, gathering baseline coverage: 0/4 completed
fuzz: elapsed: 0s, gathering baseline coverage: 4/4 completed, now
fuzzing
fuzz: elapsed: 3s, execs: 412233 (137411/sec), new interesting: 9
fuzz: elapsed: 5s, execs: 701902 (144911/sec), new interesting: 9
PASS
ok      burrow/wire     5.418s
```

## Done when

- [ ] `WriteRecord` of `hi` is exactly `00 00 00 02 68 69`; an empty payload is `00 00 00 00`
- [ ] Payloads of 0 bytes, 1 byte, 1 MiB and exactly `MaxRecordSize` round-trip, and several records in one buffer read back in order
- [ ] `ReadRecord` works over one-byte, half and data-with-EOF readers
- [ ] An empty stream is `io.EOF` itself; a record cut anywhere after its first byte is `io.ErrUnexpectedEOF`
- [ ] A header claiming more than `MaxRecordSize` is `ErrRecordTooLarge` with no allocation
- [ ] `WriteRecord` over the limit writes nothing; it calls `Write` once; a short write is `io.ErrShortWrite`; a writer's own error comes back as-is
- [ ] The slice from `ReadRecord` shares nothing with the reader's buffer
- [ ] `AppendRecord` builds readable records and leaves `buf` untouched on error
- [ ] The provided fuzzer finds nothing, and your own fuzz target exists, passes its seeds and survives 2 s of fuzzing

## Reference

```go
// wire/record.go
package wire

const MaxRecordSize = 16 << 20 // payload bytes

var ErrRecordTooLarge = errors.New("wire: record too large")

// Layout: uint32 big-endian payload length, then the payload.

// WriteRecord writes the length and payload with exactly one call to
// w.Write and returns io.ErrShortWrite if w accepted fewer bytes than
// given.
func WriteRecord(w io.Writer, payload []byte) error

// ReadRecord reads one record. An empty stream returns (nil, io.EOF);
// EOF anywhere after the first byte returns io.ErrUnexpectedEOF. A
// length over MaxRecordSize returns ErrRecordTooLarge without
// allocating. The returned slice is freshly allocated.
func ReadRecord(r io.Reader) ([]byte, error)

// AppendRecord appends the encoded record to buf and returns the
// extended slice, for writers that batch several records into one
// Write.
func AppendRecord(buf []byte, payload []byte) ([]byte, error)
```

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
./burrow watch
go test -fuzz=FuzzL2_1 ./wire                   # as long as you like
go test -fuzz=FuzzRecord -fuzztime=30s ./wire   # yours; crashers go to
                                                # wire/testdata/fuzz/
```

## In the wild

protobuf length-delimited streams, gRPC's 5-byte message frame, Kafka record batches, Redis RESP bulk strings: every binary protocol has this part

## New in Go

io.Reader · io.ReadFull · io.ErrUnexpectedEOF vs io.EOF · encoding/binary · arrays vs slices · defensive limits · append and capacity · copy and bytes.Clone · three-index slicing s[a:b:c] · io.ErrShortWrite · testing/iotest · writing a fuzz target

Standard library: `encoding/binary`, `io`, `bytes`, `testing`, `testing/iotest`
