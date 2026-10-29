# 2.3 · Framing

Text breaks on binary values. Replace it with length-prefixed frames: the codec your client, server, WAL and cluster RPC will all use.

## Build

```go
// wire/frame.go
type Op byte

const (
	OpGet    Op = 0x01 // requests
	OpSet    Op = 0x02
	OpDelete Op = 0x03
	OpPing   Op = 0x04

	OpOK       Op = 0x80 // replies
	OpValue    Op = 0x81
	OpNotFound Op = 0x82
	OpError    Op = 0x83
	OpPong     Op = 0x84
)

func (o Op) String() string // "GET", "SET", "DELETE", "PING", "OK", "VALUE", "NOT_FOUND", "ERROR", "PONG", else "Op(0x7f)"

const (
	MaxKeySize   = 1024
	MaxValueSize = 8 << 20
)

var (
	ErrFrameTooLarge = errors.New("frame too large")
	ErrUnknownOp     = errors.New("unknown op")
)

type Frame struct {
	Op    Op
	Key   string
	Value []byte
}

func WriteFrame(w io.Writer, f Frame) error
func ReadFrame(r io.Reader) (Frame, error)
```

Layout, all integers big-endian:

```
┌────┬──────────────┬─────────┬──────────────┬───────────┐
│ op │ key length   │ key     │ value length │ value     │
│ 1B │ uint32       │ n bytes │ uint32       │ m bytes   │
└────┴──────────────┴─────────┴──────────────┴───────────┘
```

## The tests check

- Golden bytes: `SET k=v` is exactly `02 00000001 6b 00000001 76`.
- Round trips of random binary keys and values, empty ones, and frames back to back in one buffer.
- **EOF rules:** an empty stream gives `io.EOF` itself; a frame cut anywhere after its first byte gives `io.ErrUnexpectedEOF`.
- Readers that return one byte at a time, or data together with EOF.
- Unknown ops are rejected both ways, and a rejected write writes nothing.
- Lengths over the limit give `ErrFrameTooLarge` **without allocating**: a header claiming 4 GiB must not make a 4 GiB slice.
- Frames exactly at the limit work.
- `WriteFrame` calls `Write` **once** per frame, and reports `io.ErrShortWrite` for a writer that silently accepts less.
- A fuzz test: `go test -fuzz=FuzzL2_3 ./wire` for as long as you like. It must never panic.

## New in Go

`encoding/binary` · `io.ReadFull` · defensive limits · `testing/iotest` · fuzzing · `fmt.Stringer`
