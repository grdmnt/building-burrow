# G.1 · Wire Format

> Before you let a code generator write your protocol, write it once by hand. Protobuf is varints, tags and lengths, and nothing else.

## Your goal

Write `pbwire`, a hand-made protobuf codec for Burrow's two messages: `Entry` (a key, a value, a version and an expiry) and `Event` (an ID, a type and an `Entry`). The bytes must be exactly what Google's protobuf library writes for the same messages, so that in G.2 the generated code and your codec can be checked against each other byte for byte. Then add gRPC's 5-byte message frame, which is how every protobuf message travels in a gRPC call.

Three things make this more than a byte layout. Integers are variable length, so every read can run off the end or go on for too long. A decoder must skip fields it has never heard of, because a newer writer may add some. And proto3 writes nothing at all for a zero value, so "absent" and "zero" decode the same.

## What you're using from before

**Record codec (2.1 · Record).** A gRPC message frame is a 2.1 record with one extra byte in front: a compressed flag, then a uint32 big-endian length, then the message. The rules carry over unchanged: one `Write` per message, `io.ReadFull` for the header and body, `io.EOF` only on a clean end, `io.ErrUnexpectedEOF` for a cut message, and the length checked against a limit before `make`.

```go
var hdr [5]byte          // flag, then 4 length bytes
_, err := io.ReadFull(r, hdr[:])
n := binary.BigEndian.Uint32(hdr[1:])
```

**KV Frame (2.2 · Frame).** 2.2 gave every field a fixed offset. Protobuf drops fixed offsets: every field carries its own label, so fields can be missing, repeated or unknown. What 2.2 taught still holds: check every length against what is actually left before slicing, and copy bytes out of the input (`bytes.Clone`) so the result doesn't change when the caller reuses its buffer.

**Broker (8.2 · Fan-out).** `MarshalEvent` and `UnmarshalEvent` take and return the `watch.Event` your broker publishes, so G.5 can send broker events over gRPC without another type in between. Its `Type` strings `put`, `delete`, `expire` (and `ready` from 8.3) become a protobuf enum.

## What's new here

**Varints.** Protobuf writes integers in base 128: seven bits per byte, lowest seven bits first, and the top bit of each byte says "another byte follows". Small numbers are small on the wire:

```text
1     = 0000001                  -> 01
300   = 0000010 0101100          -> ac 02
        (low 7 bits first: 0101100 | 0x80 = ac, then 0000010 = 02)
2^64-1                           -> ff ff ff ff ff ff ff ff ff 01
```

A uint64 needs at most 10 bytes, and the 10th can only carry the 64th bit, so it must be `00` or `01`. Encoding is a loop with `&`, `|` and `>>`:

```go
for v >= 0x80 {
	b = append(b, byte(v)|0x80) // low 7 bits, "more follows"
	v >>= 7
}
b = append(b, byte(v))
```

Decoding goes the other way: `v |= uint64(c&0x7f) << (7 * i)` for each byte `c`, stopping at the first byte below `0x80`. Two things can go wrong. The input ends while the top bit still promises more: `ErrTruncated`. Or the varint doesn't fit 64 bits (a 10th byte above `01`, or an 11th byte): `ErrOverflow`. A varint padded with `80` bytes, like `80 00` for 0, is legal as long as it fits in 10 bytes; protobuf accepts it, and so should you.

**Tags and wire types.** Every field starts with a tag, itself a varint: the field number shifted left by 3, OR the wire type in the low 3 bits. The wire type says how long the value is, which is all a decoder needs to step over it:

```text
type  name    value that follows
0     VARINT  one varint                 uint64, int64, enum
1     I64     8 bytes                    fixed64, double
2     LEN     varint length, then bytes  string, bytes, messages
3     SGROUP  fields until a matching    groups (deprecated)
4     EGROUP  end-group tag, same field
5     I32     4 bytes                    fixed32, float
```

Field 1 as LEN is `1<<3 | 2 = 0x0a`, field 3 as VARINT is `3<<3 | 0 = 0x18`. Field numbers 1 to 15 fit in a one-byte tag, which is why a schema puts its common fields there. Wire types 6 and 7 don't exist, and field number 0 is never valid: both are `ErrWireType`.

**Length-delimited fields and nested messages.** A string, a byte slice and a whole message all look the same on the wire: tag, varint length, bytes. An `Event` carries its `Entry` as field 3 holding `MarshalEntry`'s bytes. Decode the nested message from exactly those bytes, a sub-slice `p[:n]`, never from the rest of the input: a key length inside the entry that runs past the entry's end is `ErrTruncated`, even if the outer message has more bytes.

**proto3 omits zero values.** A field holding its zero value (`""`, empty bytes, `0`, the first enum value) is not written at all. `Entry{}` encodes to zero bytes, and so does `Event{}`. The reader fills in zeros for whatever is missing, so a field written out as zero anyway (`18 00`) reads back the same as one that is absent. Fields go out in field number order, which is what makes the bytes match protobuf's exactly.

**Signed numbers.** `expires_at_unix_nano` is an `int64`. protobuf's `int64` is the two's complement bits as a `uint64` varint, so `-1` costs 10 bytes (`ff ff ff ff ff ff ff ff ff 01`). That is `uint64(ns)` out and `int64(v)` back, the same conversion 2.2 used for the TTL. (`sint64` would zigzag-encode instead; the schema says `int64`.) Expiry 0 means no expiry: a zero `time.Time` is not written, and a 0 or a missing field 4 decodes to `time.Time{}`. Anything else is `time.Unix(0, ns)`.

**Enums.** An enum is a varint. `burrow.v1.EventType` numbers the types `UNSPECIFIED` 0, `PUT` 1, `DELETE` 2, `EXPIRE` 3, `READY` 4, and two maps translate between them and the `watch.Event.Type` strings. A type string with no number is `UNSPECIFIED`, which proto3 omits. Going the other way, `UNSPECIFIED` and any number you don't know decode to `Type: ""` without an error: a newer server may add a type, and an older client must keep working.

**Unknown fields.** A decoder that meets a field number it doesn't know reads the wire type and skips the value: a varint, 8 bytes, 4 bytes, or a length and that many bytes. A group (`SGROUP`) is skipped by reading tags until the `EGROUP` with the same field number, skipping whatever is inside, including groups within groups. An `EGROUP` with no start, or one closing a different field, is `ErrWireType`. This is the property that lets protobuf schemas grow: add field 5 to `Entry` next year, and this year's decoder still works.

A known field number with the wrong wire type (a key sent as a varint) is `ErrWireType` in pbwire. protobuf-go is more forgiving and files it with the unknown fields; for Burrow's own schema it can only mean a bug, so we refuse it.

**The last one wins.** A field may appear more than once. For a scalar the last value wins. For a nested message the occurrences are *merged*: decode each one into the message you already have. It sounds odd, but it gives protobuf a neat property: two encodings concatenated decode as the first merged with the second. Decoding into a pointer, with a helper like `mergeEntry(&ev.Entry, p)`, gets this for free; decoding into a fresh `Entry` and assigning it doesn't.

**Walking a message.** Every decoder in this level is the same loop, and the clearest way to write it is once:

```go
for len(b) > 0 {
	num, typ, n, err := consumeTag(b)  // varint >> 3, & 7
	if err != nil {
		return err
	}
	b = b[n:]
	switch {
	case num == 1 && typ == 2:
		// consume a length, then that many bytes
	case num == 1:
		return ErrWireType
	default:
		n, err := skipField(b, num, typ)
		// ...
		b = b[n:]
	}
}
```

**The gRPC message frame.** gRPC sends each protobuf message as a "length-prefixed message": a compressed flag byte, a uint32 big-endian length, the message. Burrow never negotiates compression, so a non-zero flag is `ErrCompressed`. Check the flag first, then the length against `max`, and only then `make`: a header claiming 4 GiB must cost nothing.

```text
00  00 00 00 04  08 07 10 04
^   ^            ^
|   length 4     Event{ID: 7, Type: "ready"}
flag 0: not compressed
```

## What to do

1. Create `pbwire/varint.go` with `package pbwire`, the five errors, and `AppendVarint` and `ConsumeVarint`.
2. Write a tag reader (`num = v >> 3`, `typ = v & 7`, field 0 is `ErrWireType`) and `skipField` for wire types 0, 1, 2, 3 and 5. Anything else, including a stray `EGROUP`, is `ErrWireType`.
3. Create `pbwire/entry.go` with `MarshalEntry`: append each non-zero field in number order. Then `UnmarshalEntry`: walk the fields, decode the four known ones (checking each one's wire type), skip the rest. Copy the value out of the input. On any error return the zero `Entry`.
4. Create `pbwire/event.go` with `MarshalEvent` (the entry as field 3, only when it has a key) and `UnmarshalEvent` (merge every field 3 into the same `Entry`), with the enum maps.
5. Create `pbwire/grpc.go` with `WriteGRPCMessage` (one `Write`) and `ReadGRPCMessage` (header into a `[5]byte`, flag, then length, then `make`).
6. Run `go test -race ./pbwire`, then fuzz: `go test -fuzz=FuzzLG_1 -fuzztime=30s ./pbwire`.

## Example

```go
e := store.Entry{Key: "user:1", Value: []byte("ann"), Version: 3}
fmt.Printf("% x\n", pbwire.MarshalEntry(e))
// 0a 06 75 73 65 72 3a 31  12 03 61 6e 6e  18 03
// key: field 1, LEN 6     value: 2, LEN 3  version: 3, VARINT 3

ev := watch.Event{ID: 2, Type: "delete", Entry: store.Entry{Key: "k"}}
fmt.Printf("% x\n", pbwire.MarshalEvent(ev))
// 08 02  10 02  1a 03 0a 01 6b
// id 2   DELETE entry: 3 bytes, {key "k"}

// an unknown varint field 5 (28 96 01) is stepped over
e, err := pbwire.UnmarshalEntry([]byte{0x28, 0x96, 0x01,
	0x0a, 0x01, 'k'})
// e.Key == "k", err == nil

_, _, err = pbwire.ConsumeVarint([]byte{0xac})
// ErrTruncated: the top bit promised another byte

var buf bytes.Buffer
pbwire.WriteGRPCMessage(&buf, pbwire.MarshalEvent(
	watch.Event{ID: 7, Type: "ready"}))
// buf: 00 00 00 00 04 08 07 10 04
msg, err := pbwire.ReadGRPCMessage(&buf, 4<<20)
// msg == 08 07 10 04
```

If you have `protoc` installed, it can show you any bytes as raw protobuf, with no schema needed:

```bash
$ printf '\x0a\x01k\x18\x03' | protoc --decode_raw
1: "k"
3: 3
```

## Done when

- [ ] Varints encode and decode across the whole uint64 range, padded varints are accepted, and truncated and overlong ones are refused
- [ ] Every `Entry` and `Event` in the table encodes to exactly the bytes protobuf writes, and decodes back
- [ ] Zero values are omitted, and explicit zeros on the wire read as zero
- [ ] Unknown fields of every wire type, including nested groups, are skipped at the top level and inside the nested entry
- [ ] Malformed input (cut short, over 64 bits, bad wire type, field 0, a known field with the wrong wire type) is the right error, never a panic
- [ ] A repeated field: the last value wins; a repeated entry merges
- [ ] The enum maps both ways, and unknown numbers decode to `""`
- [ ] gRPC frames round-trip over slow readers, follow 2.1's EOF rules, and an oversized or compressed header is refused before allocating
- [ ] `FuzzLG_1_RoundTrip` finds nothing

## Reference

```go
// pbwire, stdlib only.
package pbwire

var (
	ErrTruncated  = errors.New("pbwire: truncated")
	ErrOverflow   = errors.New("pbwire: varint overflows 64 bits")
	ErrWireType   = errors.New("pbwire: bad tag or wire type")
	ErrTooLarge   = errors.New("pbwire: message too large")
	ErrCompressed = errors.New("pbwire: compressed message")
)

// AppendVarint appends v as a base-128 varint.
func AppendVarint(b []byte, v uint64) []byte

// ConsumeVarint decodes the varint at the front of b and returns it
// with the number of bytes used. Input that ends mid-varint is
// ErrTruncated; more than 10 bytes or more than 64 bits is
// ErrOverflow.
func ConsumeVarint(b []byte) (v uint64, n int, err error)

// burrow.v1.Entry: 1 key string, 2 value bytes, 3 version uint64,
// 4 expires_at_unix_nano int64 (0 = no expiry). Fields in number
// order, zero values omitted. Unmarshal skips unknown fields of any
// wire type and returns the zero Entry with any error.
func MarshalEntry(e store.Entry) []byte
func UnmarshalEntry(b []byte) (store.Entry, error)

// burrow.v1.Event: 1 id uint64, 2 type EventType, 3 entry Entry
// (written only when the entry has a key).
// EventType: 0 UNSPECIFIED, 1 PUT, 2 DELETE, 3 EXPIRE, 4 READY,
// for the watch.Event.Type strings "put", "delete", "expire",
// "ready". Any other string encodes as UNSPECIFIED; UNSPECIFIED and
// unknown numbers decode as "".
func MarshalEvent(ev watch.Event) []byte
func UnmarshalEvent(b []byte) (watch.Event, error)

// gRPC length-prefixed message: flag byte 0, uint32 big-endian
// length, the message, in one Write.
func WriteGRPCMessage(w io.Writer, msg []byte) error

// ReadGRPCMessage reads one message. An empty stream is io.EOF; a
// cut one io.ErrUnexpectedEOF. A non-zero flag is ErrCompressed and
// a length over max ErrTooLarge, checked in that order and before
// allocating. The returned slice is fresh and non-nil.
func ReadGRPCMessage(r io.Reader, max int) ([]byte, error)
```

The same messages in `.proto` form, for comparison (G.2 writes this file for real):

```proto
syntax = "proto3";
package burrow.v1;

message Entry {
  string key = 1;
  bytes value = 2;
  uint64 version = 3;
  int64 expires_at_unix_nano = 4;
}

enum EventType {
  EVENT_TYPE_UNSPECIFIED = 0;
  EVENT_TYPE_PUT = 1;
  EVENT_TYPE_DELETE = 2;
  EVENT_TYPE_EXPIRE = 3;
  EVENT_TYPE_READY = 4;
}

message Event {
  uint64 id = 1;
  EventType type = 2;
  Entry entry = 3;
}
```

Decisions the tests rely on:

- Errors are checked with `errors.Is`, so wrapping them with more detail is fine.
- An expiry whose `UnixNano` is 0 counts as no expiry.
- A known field with the wrong wire type is `ErrWireType`, not skipped.
- `ConsumeVarint` matches protobuf-go's `protowire.ConsumeVarint`: nine `ff` bytes are `ErrTruncated`, ten are `ErrOverflow`.
- pbwire does not check that keys are valid UTF-8 (protobuf-go does for proto3 strings); `store.ValidateKey` already does.

## The tests check

- Varint golden bytes from 0 to 2^64-1, the byte count `ConsumeVarint` reports, prefix-preserving `AppendVarint`, and 2,000 random round trips
- Padded varints (`80 00`, ten bytes) are accepted; empty, cut and all-continuation inputs are `ErrTruncated`; a 10th byte above `01` or an 11th byte is `ErrOverflow`
- A table of entries and events encodes to the bytes `google.golang.org/protobuf` produces, and decodes back, including a negative int64, a two-byte length and an expiry in another time zone
- proto3 zero omission for every field, the zero `Event` is zero bytes, explicit zeros decode to zero and to a zero `time.Time`
- Unknown fields of wire types 0, 1, 2, 3 (with a group inside a group) and 5, and the largest field number, before, between and after known fields, at the top of an `Event` and inside its `Entry`
- Malformed input: every way to be cut short, varint overflow in a value and in a length, wire types 6 and 7, field 0, stray and mismatched end-groups, a known field with the wrong wire type, and a nested key that runs past its entry; the zero `Entry` comes back with the error
- Repeated fields: last one wins; `Marshal(a)+Marshal(b)` decodes as a merge; two entry fields merge
- The decoded value doesn't share memory with the input
- The enum both ways, unknown strings encode as nothing, and unknown numbers decode to `""` with no error
- Expiry round trips from `math.MinInt64` to `math.MaxInt64` nanoseconds
- 1,000 random entries and events round-trip
- gRPC framing: golden header, round trips over one-byte, half and data-with-EOF readers, `io.EOF` versus `io.ErrUnexpectedEOF` at every cut, `ErrTooLarge` with almost no bytes allocated (measured with `runtime.MemStats`), exactly `max` allowed, `ErrCompressed` checked before the length, one `Write`, short and failing writers, a fresh slice
- FuzzLG_1_RoundTrip: arbitrary bytes never panic and re-encode to a stable form, a gRPC frame that reads re-encodes to a prefix of the input, and any Entry or Event built from fuzzed fields round-trips

## Try it

```bash
./burrow watch
go test -race -run LG_1 -v ./pbwire
go test -fuzz=FuzzLG_1 -fuzztime=1m ./pbwire
# print your bytes and compare with protoc's view of them:
#   printf '\x08\x07\x10\x04' | protoc --decode_raw
```

## In the wild

google.golang.org/protobuf/encoding/protowire is this level as a library (AppendVarint, ConsumeTag, ConsumeFieldValue); gRPC over HTTP/2 frames every message with the same 5-byte prefix; Kafka, SQLite and Go's own encoding/binary.Uvarint use varints too.

## New in Go

bit operators (&, |, <<, >>) · varint loops · uint64/int64 two's complement conversions · decoding into a pointer to merge · recursion for nested groups · time.Unix(0, ns) and UnixNano · encoding/hex for test fixtures · fuzz targets with several arguments

Standard library: `encoding/binary`, `io`, `bytes`, `errors`, `time`, `encoding/hex`, `testing`
