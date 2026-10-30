# 2.2 · Frame

> A record says how many bytes. A frame says what they mean.

## Your goal

Define the message that client and server will exchange: a `Frame` with an operation code (`Op`), a key, a value, a version and a TTL, and write `EncodeFrame`/`DecodeFrame` to turn it into bytes and back with a fixed layout. Then wrap each encoded frame in a record from 2.1 to get `WriteFrame` and `ReadFrame`, which is what will go over the network.

The layout has a neat property: the record already says how long the whole payload is, so the value, which comes last, needs no length of its own. It is simply everything after the key.

## What you're using from before

**Record codec (2.1).** `WriteFrame` is `EncodeFrame` followed by `WriteRecord`; `ReadFrame` is `ReadRecord` followed by `DecodeFrame`. The record provides the edges and the EOF rules, the frame provides the meaning:

```go
func WriteFrame(w io.Writer, f Frame) error {
    p, err := EncodeFrame(f)
    if err != nil {
        return err          // nothing written: the record never started
    }
    return WriteRecord(w, p)
}
```

`ReadFrame` at a clean end of stream returns `io.EOF` because `ReadRecord` does; a frame cut short is `io.ErrUnexpectedEOF` for the same reason. `MaxRecordSize` (16 MiB) still bounds the record, so the frame's own limits (1 KiB key, 8 MiB value) always fit inside it.

## What's new here

**Typed constants and `iota`.** `type Op byte` makes a new type with `byte`'s representation, and constants of that type are how Go spells an enum. An `Op` is not interchangeable with a plain `byte` without a conversion, so `OpGet` can't be accidentally passed where a count was meant. `iota` is a counter that increments on each line of a `const` block, handy for enums where the values don't matter:

```go
type Color byte

const (
    Red Color = iota   // 0
    Green              // 1
    Blue               // 2
)
```

Here the values *do* matter, because they go on the wire and the other side has to agree, so the `Op` constants are written out explicitly. Requests are `0x01` to `0x09`, replies are `0x80` and up: the top bit says which direction a frame travels, which is what `IsRequest` checks.

**`fmt.Stringer`.** Any type with a `String() string` method is printed with it by `fmt`'s `%v` and `%s`, by `t.Errorf`, and by the debugger. Give `Op` one and `fmt.Println(wire.OpSet)` prints `SET` instead of `2`. A `map[Op]string` is the simplest way to write it, with a fallback for values the map doesn't know:

```go
func (c Color) String() string {
    if name, ok := colorNames[c]; ok {
        return name
    }
    return fmt.Sprintf("Color(0x%02x)", byte(c))
}
```

**Byte layouts.** A fixed layout says what each byte position means. Ours, inside one record, is:

```text
offset  size  field
0       1     op
1       8     version   uint64, big-endian
9       8     ttl       int64 nanoseconds, big-endian
17      2     key length  uint16, big-endian
19      n     key
19+n    rest  value     to the end of the payload
```

So a frame is at least 19 bytes. `binary.BigEndian.AppendUint64` and `AppendUint16` build the fixed part; the key and value are appended as they are. Decoding is the reverse, and it is the order of checks that matters: is the payload at least 19 bytes, is the key length within `MaxKeySize` and within what is actually left, and only then slice the key out. Checking the length against the limit *before* slicing is what stops a header claiming a 65535-byte key from reading past the end or allocating for nothing.

**Signed in an unsigned field.** `time.Duration` is an `int64`. Storing it in the 8 TTL bytes is `uint64(d)` on the way out and `time.Duration(u)` on the way back; both are plain conversions that keep the bits, so negative durations round-trip unchanged. The codec is not the place to decide whether negative is allowed; it just carries the number.

**Copying out of the payload.** `p[19:19+n]` is a slice *into* `p`. Converting a byte slice to a `string` copies, so `Key` is safe. The value is a `[]byte`, so it has to be copied with `bytes.Clone` or the frame would change whenever the caller reuses the buffer `p` came from.

**Composition of codecs.** A codec that works over any `io.Reader`/`io.Writer` can be stacked: frame inside record, record over connection. Each layer handles one concern and the layers meet at a `[]byte`. Your `ReadFrame` is three lines long because 2.1 did the hard part.

## What to do

1. Create `wire/frame.go` with the `Op` type and all sixteen constants with the exact byte values from the Reference, the two limits, the two errors and the `Frame` struct.
2. Write `Op.String()` returning the names in the Reference (`GET`, `SET`, ..., `KEY_LIST`), and `Op(0x7f)`-style text for anything else. Write `Op.IsRequest()`: true for `0x01` to `0x09` only.
3. Write `EncodeFrame(f)`:
   - reject an `Op` that isn't one of the sixteen with `ErrUnknownOp`
   - reject `len(f.Key) > MaxKeySize` or `len(f.Value) > MaxValueSize` with `ErrFrameTooLarge`
   - append op, version, TTL (as `uint64`), key length (as `uint16`), key, value into one slice and return it
4. Write `DecodeFrame(p)`:
   - fewer than 19 bytes is `io.ErrUnexpectedEOF`
   - an unknown op is `ErrUnknownOp`
   - read the key length; over `MaxKeySize` is `ErrFrameTooLarge`, longer than the bytes remaining is `io.ErrUnexpectedEOF`, both checked before slicing
   - the rest is the value; over `MaxValueSize` is `ErrFrameTooLarge`
   - copy the value out so the frame shares nothing with `p`
5. Write `WriteFrame` and `ReadFrame` by composing with 2.1, as shown above. A `WriteFrame` whose encode fails writes nothing.

## Example

```go
set := wire.Frame{Op: wire.OpSet, Key: "k", Value: []byte("v")}
p, _ := wire.EncodeFrame(set)
// p is 21 bytes:
//   02                        op SET
//   00 00 00 00 00 00 00 00   version 0
//   00 00 00 00 00 00 00 00   ttl 0
//   00 01                     key length 1
//   6b                        "k"
//   76                        "v"

var buf bytes.Buffer
wire.WriteFrame(&buf, set)
// buf is 25 bytes: 00 00 00 15 (record length 21) then the 21 above

f, err := wire.ReadFrame(&buf)
// f.Op == wire.OpSet, f.Key == "k", string(f.Value) == "v", err == nil
_, err = wire.ReadFrame(&buf)       // io.EOF

p, _ = wire.EncodeFrame(wire.Frame{Op: wire.OpCAS, Key: "ab",
    Version: 0x0102, TTL: 3 * time.Nanosecond})
// 05 | 00 00 00 00 00 00 01 02 | 00 00 00 00 00 00 00 03 |
// 00 02 | 61 62

fmt.Println(wire.OpKeyList, wire.Op(0x7f))   // KEY_LIST Op(0x7f)
wire.OpGet.IsRequest()                       // true
wire.OpValue.IsRequest()                     // false

_, err = wire.DecodeFrame([]byte{0x01, 0, 0})     // io.ErrUnexpectedEOF
_, err = wire.EncodeFrame(wire.Frame{Op: 0x0a})   // wire.ErrUnknownOp
hdr := append([]byte{0x01}, make([]byte, 16)...)
_, err = wire.DecodeFrame(append(hdr, 0xff, 0xff))   // ErrFrameTooLarge
```

## Done when

- [ ] `EncodeFrame` of a SET of `k`=`v` is exactly the 21 bytes above, and `WriteFrame` prefixes `00 00 00 15`
- [ ] Every `Op` has its listed byte value, `String()` gives the listed name or `Op(0x7f)`, and `IsRequest` is true for `0x01` to `0x09` only
- [ ] Frames round-trip for every op, with `math.MaxUint64` versions, negative and 90-minute TTLs, and random keys and values
- [ ] Three frames written back to back read back in order, then `io.EOF`; a cut frame is `io.ErrUnexpectedEOF`
- [ ] Short payloads and over-running key lengths are `io.ErrUnexpectedEOF`; unknown ops are `ErrUnknownOp` both ways and a rejected `WriteFrame` writes nothing
- [ ] Oversized keys and values are `ErrFrameTooLarge` on encode; on decode a key length of 65535 is rejected before slicing and an oversized value is rejected
- [ ] A decoded frame shares no memory with its payload
- [ ] The provided fuzzer finds nothing

## Reference

```go
// wire/frame.go
package wire

type Op byte

const (
	OpGet    Op = 0x01 // requests
	OpSet    Op = 0x02
	OpDelete Op = 0x03
	OpKeys   Op = 0x04 // Key holds the prefix
	OpCAS    Op = 0x05 // Version holds ifVersion; TTL 0 =
	                   // CompareAndSwap, TTL > 0 =
	                   // CompareAndSwapWithTTL
	OpCAD    Op = 0x06 // compare-and-delete; Version holds ifVersion
	OpSetTTL Op = 0x07 // TTL holds the ttl
	OpTouch  Op = 0x08 // TTL holds the ttl
	OpPing   Op = 0x09

	OpOK       Op = 0x80 // replies; Version carries the new version
	                     // where there is one
	OpValue    Op = 0x81 // Value, Version, TTL = time remaining
	                     // (0 = never)
	OpNotFound Op = 0x82
	OpMismatch Op = 0x83 // Version carries the current version
	OpError    Op = 0x84 // Value is the error text
	OpPong     Op = 0x85
	OpKeyList  Op = 0x86 // Value is keys joined by \n (keys never
	                     // contain whitespace: 0.2)
)

func (o Op) String() string // "GET", "SET", ... "KEY_LIST",
                            // else "Op(0x7f)"
func (o Op) IsRequest() bool

const (
	MaxKeySize   = 1024
	MaxValueSize = 8 << 20
)

var (
	ErrFrameTooLarge = errors.New("wire: frame too large")
	ErrUnknownOp     = errors.New("wire: unknown op")
)

type Frame struct {
	Op      Op
	Key     string
	Value   []byte
	Version uint64
	TTL     time.Duration
}

// Payload layout (inside a Record), big-endian:
//   op 1B | version uint64 | ttl int64 (nanoseconds) |
//   key length uint16 | key | value (to the end)
// The value has no length field: the record's length already bounds it.
func EncodeFrame(f Frame) ([]byte, error)
func DecodeFrame(p []byte) (Frame, error)
func WriteFrame(w io.Writer, f Frame) error // EncodeFrame + WriteRecord
func ReadFrame(r io.Reader) (Frame, error)  // ReadRecord + DecodeFrame
```

Op names for `String()`: `GET`, `SET`, `DELETE`, `KEYS`, `CAS`, `CAD`, `SET_TTL`, `TOUCH`, `PING`, `OK`, `VALUE`, `NOT_FOUND`, `MISMATCH`, `ERROR`, `PONG`, `KEY_LIST`.

Decisions the tests rely on:

- `DecodeFrame` copies the value out of the input; the returned frame never aliases `p`.
- `TTL` is encoded as int64 nanoseconds. Negative values round-trip through the codec; the server rejects them.

## The tests check

- Golden: EncodeFrame(Frame{Op: OpSet, Key: "k", Value: []byte("v")}) is exactly 02 | 8 zero bytes | 8 zero bytes | 00 01 | 6b | 76 (21 bytes); WriteFrame prefixes 00 00 00 15; a CAS frame with Version 0x0102 and TTL 3ns shows both fields big-endian
- Every Op has the listed byte value; Op.String for every op and `Op(0x7f)` for an unknown one; IsRequest is true for 0x01-0x09, false for replies and unknown ops
- Round trips: Version math.MaxUint64, a negative TTL, a 90-minute TTL, a frame at both limits, and random keys and values for all 16 ops
- Three frames written back to back read back in order, then ReadFrame returns io.EOF; a frame cut short is io.ErrUnexpectedEOF
- DecodeFrame of a payload shorter than the 19-byte header is io.ErrUnexpectedEOF; a key length that runs past the end is io.ErrUnexpectedEOF
- Unknown ops are rejected by both Encode and Decode with ErrUnknownOp; a rejected WriteFrame writes nothing
- MaxKeySize is 1024 and MaxValueSize 8 MiB; a key or value over the limit is ErrFrameTooLarge on encode; on decode a key length of 65535 is rejected before slicing and a value over the limit is rejected
- A decoded frame doesn't share memory with the payload it came from
- FuzzL2_2_DecodeFrame never panics and a successful decode re-encodes to the same bytes

## Try it

```bash
./burrow watch
go test -run TestL2_2_GoldenBytes -v ./wire   # the layout, byte by byte
go test -fuzz=FuzzL2_2 -fuzztime=30s ./wire
```

## In the wild

Redis RESP3 and the memcached binary protocol are this with more ops; gRPC puts the typed message in protobuf inside the same kind of frame

## New in Go

typed constants and iota · fmt.Stringer · byte layouts · signed/unsigned conversions · composition of codecs

Standard library: `encoding/binary`, `io`, `time`, `fmt`
