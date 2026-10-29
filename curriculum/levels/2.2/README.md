# 2.2 · Frame

A typed message laid out inside a record. Because the record already carries its length, the value is simply whatever is left, and the frame needs no length field of its own.

## Build

```go
// wire/frame.go
package wire

type Op byte

const (
	OpGet    Op = 0x01 // requests
	OpSet    Op = 0x02
	OpDelete Op = 0x03
	OpKeys   Op = 0x04 // Key holds the prefix
	OpCAS    Op = 0x05 // Version holds ifVersion; TTL 0 = CompareAndSwap, TTL > 0 = CompareAndSwapWithTTL
	OpCAD    Op = 0x06 // compare-and-delete; Version holds ifVersion
	OpSetTTL Op = 0x07 // TTL holds the ttl
	OpTouch  Op = 0x08 // TTL holds the ttl
	OpPing   Op = 0x09

	OpOK       Op = 0x80 // replies; Version carries the new version where there is one
	OpValue    Op = 0x81 // Value, Version, TTL = time remaining (0 = never)
	OpNotFound Op = 0x82
	OpMismatch Op = 0x83 // Version carries the current version
	OpError    Op = 0x84 // Value is the error text
	OpPong     Op = 0x85
	OpKeyList  Op = 0x86 // Value is keys joined by \n (keys never contain whitespace: 0.2)
)

func (o Op) String() string // "GET", "SET", ..., "KEY_LIST", else "Op(0x7f)"
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
//   op 1B | version uint64 | ttl int64 (nanoseconds) | key length uint16 | key | value (to the end)
// The value has no length field: the record's length already bounds it.
func EncodeFrame(f Frame) ([]byte, error)
func DecodeFrame(p []byte) (Frame, error)
func WriteFrame(w io.Writer, f Frame) error  // EncodeFrame + WriteRecord
func ReadFrame(r io.Reader) (Frame, error)   // ReadRecord + DecodeFrame
```

- `Op` constants, `String()`, `IsRequest()`
- `EncodeFrame`/`DecodeFrame` with the exact layout and limits
- `WriteFrame`/`ReadFrame` built on the Record codec

Decisions the tests rely on:

- `DecodeFrame` copies the value out of the input; the returned frame never aliases `p`.
- `TTL` is encoded as int64 nanoseconds. Negative values round-trip through the codec; the server rejects them.

## The tests check

- Golden: EncodeFrame(Frame{Op: OpSet, Key: "k", Value: []byte("v")}) is exactly 02 | 8 zero bytes | 8 zero bytes | 00 01 | 6b | 76 (21 bytes); WriteFrame prefixes 00 00 00 15; a CAS frame with Version 0x0102 and TTL 3ns shows both fields big-endian
- Every Op has the listed byte value; Op.String for every op and `Op(0x7f)` for an unknown one; IsRequest is true for 0x01-0x09, false for replies and unknown ops
- Round trips: Version math.MaxUint64, a negative TTL, a 90-minute TTL, and random keys and values for all 16 ops
- Three frames written back to back read back in order, then ReadFrame returns io.EOF; a frame cut short is io.ErrUnexpectedEOF
- DecodeFrame of a payload shorter than the 19-byte header is io.ErrUnexpectedEOF; a key length that runs past the end is io.ErrUnexpectedEOF
- Unknown ops are rejected by both Encode and Decode with ErrUnknownOp; a rejected WriteFrame writes nothing
- MaxKeySize is 1024 and MaxValueSize 8 MiB; a key or value over the limit is ErrFrameTooLarge on encode; on decode a key length of 65535 is rejected before slicing and a value over the limit is rejected
- A decoded frame doesn't share memory with the payload it came from
- FuzzL2_2_DecodeFrame never panics and a successful decode re-encodes to the same bytes

## Uses

- Record codec (built in 2.1 · Record)

## In the wild

Redis RESP3 and the memcached binary protocol are this with more ops; gRPC puts the typed message in protobuf inside the same kind of frame

## New in Go

typed constants and iota · fmt.Stringer · byte layouts · composition of codecs

Standard library: `encoding/binary`, `io`, `time`, `fmt`
