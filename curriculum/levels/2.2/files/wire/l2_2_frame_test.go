package wire_test

import (
	"bytes"
	"errors"
	"io"
	"math"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"burrow/wire"
)

var allOps = []wire.Op{
	wire.OpGet, wire.OpSet, wire.OpDelete, wire.OpKeys, wire.OpCAS, wire.OpCAD, wire.OpSetTTL, wire.OpTouch, wire.OpPing,
	wire.OpOK, wire.OpValue, wire.OpNotFound, wire.OpMismatch, wire.OpError, wire.OpPong, wire.OpKeyList,
}

func TestL2_2_GoldenBytes(t *testing.T) {
	p, err := wire.EncodeFrame(wire.Frame{Op: wire.OpSet, Key: "k", Value: []byte("v")})
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0x02, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x00, 0x01, 'k', 'v'}
	if !bytes.Equal(p, want) {
		t.Errorf("EncodeFrame(SET k=v) = % x\nwant                   % x\n(op 1B | version 8B | ttl 8B | key length 2B | key | value to the end)", p, want)
	}
	var buf bytes.Buffer
	wire.WriteFrame(&buf, wire.Frame{Op: wire.OpSet, Key: "k", Value: []byte("v")})
	if !bytes.Equal(buf.Bytes(), append([]byte{0, 0, 0, 21}, want...)) {
		t.Errorf("WriteFrame should be one Record holding the encoded frame: got % x", buf.Bytes())
	}
	p, _ = wire.EncodeFrame(wire.Frame{Op: wire.OpCAS, Key: "ab", Version: 0x0102, TTL: 3 * time.Nanosecond})
	want = []byte{0x05, 0, 0, 0, 0, 0, 0, 0x01, 0x02, 0, 0, 0, 0, 0, 0, 0, 0x03, 0x00, 0x02, 'a', 'b'}
	if !bytes.Equal(p, want) {
		t.Errorf("version and TTL are big-endian: got % x\nwant % x", p, want)
	}
}

func TestL2_2_OpValues(t *testing.T) {
	want := map[wire.Op]byte{
		wire.OpGet: 0x01, wire.OpSet: 0x02, wire.OpDelete: 0x03, wire.OpKeys: 0x04, wire.OpCAS: 0x05, wire.OpCAD: 0x06,
		wire.OpSetTTL: 0x07, wire.OpTouch: 0x08, wire.OpPing: 0x09,
		wire.OpOK: 0x80, wire.OpValue: 0x81, wire.OpNotFound: 0x82, wire.OpMismatch: 0x83, wire.OpError: 0x84,
		wire.OpPong: 0x85, wire.OpKeyList: 0x86,
	}
	for op, b := range want {
		if byte(op) != b {
			t.Errorf("%v should be 0x%02x, is 0x%02x", op, b, byte(op))
		}
	}
	names := map[wire.Op]string{
		wire.OpGet: "GET", wire.OpSet: "SET", wire.OpDelete: "DELETE", wire.OpKeys: "KEYS", wire.OpCAS: "CAS", wire.OpCAD: "CAD",
		wire.OpSetTTL: "SET_TTL", wire.OpTouch: "TOUCH", wire.OpPing: "PING", wire.OpOK: "OK", wire.OpValue: "VALUE",
		wire.OpNotFound: "NOT_FOUND", wire.OpMismatch: "MISMATCH", wire.OpError: "ERROR", wire.OpPong: "PONG",
		wire.OpKeyList: "KEY_LIST", wire.Op(0x7f): "Op(0x7f)",
	}
	for op, n := range names {
		if got := op.String(); got != n {
			t.Errorf("Op(0x%02x).String() = %q, want %q", byte(op), got, n)
		}
	}
	for _, op := range allOps {
		if op.IsRequest() != (byte(op) < 0x80) {
			t.Errorf("%v.IsRequest() = %v", op, op.IsRequest())
		}
	}
	if wire.Op(0x7f).IsRequest() || wire.Op(0).IsRequest() {
		t.Errorf("unknown ops are not requests")
	}
}

func TestL2_2_RoundTrip(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))
	frames := []wire.Frame{
		{Op: wire.OpPing},
		{Op: wire.OpGet, Key: ""},
		{Op: wire.OpValue, Key: "k", Value: []byte{}},
		{Op: wire.OpOK, Version: math.MaxUint64},
		{Op: wire.OpSetTTL, Key: "k", Value: []byte("v"), TTL: -time.Second},
		{Op: wire.OpTouch, Key: "k", TTL: 90 * time.Minute},
		{Op: wire.OpError, Value: []byte("something broke")},
		{Op: wire.OpKeyList, Value: []byte("a\nb\nc")},
		{Op: wire.OpSet, Key: strings.Repeat("k", wire.MaxKeySize), Value: make([]byte, wire.MaxValueSize)},
	}
	for _, op := range allOps {
		k := make([]byte, r.IntN(100))
		v := make([]byte, r.IntN(3000))
		for i := range k {
			k[i] = byte(r.Uint32())
		}
		for i := range v {
			v[i] = byte(r.Uint32())
		}
		frames = append(frames, wire.Frame{Op: op, Key: string(k), Value: v, Version: r.Uint64(), TTL: time.Duration(r.Int64())})
	}
	for i, f := range frames {
		p, err := wire.EncodeFrame(f)
		if err != nil {
			t.Fatalf("frame %d (%v): EncodeFrame: %v", i, f.Op, err)
		}
		got, err := wire.DecodeFrame(p)
		if err != nil {
			t.Fatalf("frame %d (%v): DecodeFrame: %v", i, f.Op, err)
		}
		if got.Op != f.Op || got.Key != f.Key || !bytes.Equal(got.Value, f.Value) || got.Version != f.Version || got.TTL != f.TTL {
			t.Fatalf("frame %d changed in a round trip:\n got %v %q %d bytes v%d ttl %v\nwant %v %q %d bytes v%d ttl %v",
				i, got.Op, got.Key, len(got.Value), got.Version, got.TTL, f.Op, f.Key, len(f.Value), f.Version, f.TTL)
		}
	}
}

func TestL2_2_StreamOfFrames(t *testing.T) {
	var buf bytes.Buffer
	for i := range 3 {
		wire.WriteFrame(&buf, wire.Frame{Op: wire.OpSet, Key: string(rune('a' + i)), Value: []byte{byte(i)}, Version: uint64(i)})
	}
	for i := range 3 {
		f, err := wire.ReadFrame(&buf)
		if err != nil || f.Key != string(rune('a'+i)) || f.Version != uint64(i) {
			t.Fatalf("frame %d from one buffer: (%+v, %v)", i, f, err)
		}
	}
	if _, err := wire.ReadFrame(&buf); err != io.EOF {
		t.Errorf("ReadFrame at a clean end: want io.EOF, got %v", err)
	}
	var cut bytes.Buffer
	wire.WriteFrame(&cut, wire.Frame{Op: wire.OpGet, Key: "key"})
	if _, err := wire.ReadFrame(bytes.NewReader(cut.Bytes()[:cut.Len()-1])); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("a frame cut short: want io.ErrUnexpectedEOF, got %v", err)
	}
}

func TestL2_2_ShortPayloads(t *testing.T) {
	for n := range 19 {
		_, err := wire.DecodeFrame(append([]byte{0x01}, make([]byte, n)...)[:n])
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Errorf("a %d-byte payload is shorter than the 19-byte header: want io.ErrUnexpectedEOF, got %v", n, err)
		}
	}
	p, _ := wire.EncodeFrame(wire.Frame{Op: wire.OpGet, Key: "abcdef"})
	if _, err := wire.DecodeFrame(p[:len(p)-2]); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("a key length that runs past the end: want io.ErrUnexpectedEOF, got %v", err)
	}
}

func TestL2_2_UnknownOps(t *testing.T) {
	for _, b := range []byte{0x00, 0x0a, 0x7f, 0x87, 0xff} {
		if _, err := wire.EncodeFrame(wire.Frame{Op: wire.Op(b)}); !errors.Is(err, wire.ErrUnknownOp) {
			t.Errorf("EncodeFrame with op 0x%02x: want ErrUnknownOp, got %v", b, err)
		}
		raw := append([]byte{b}, make([]byte, 18)...)
		if _, err := wire.DecodeFrame(raw); !errors.Is(err, wire.ErrUnknownOp) {
			t.Errorf("DecodeFrame with op 0x%02x: want ErrUnknownOp, got %v", b, err)
		}
	}
	var buf bytes.Buffer
	if err := wire.WriteFrame(&buf, wire.Frame{Op: 0x7f}); !errors.Is(err, wire.ErrUnknownOp) || buf.Len() != 0 {
		t.Errorf("a rejected WriteFrame must not write anything: (%v, %d bytes)", err, buf.Len())
	}
}

func TestL2_2_Limits(t *testing.T) {
	if wire.MaxKeySize != 1024 || wire.MaxValueSize != 8<<20 {
		t.Fatalf("limits should be MaxKeySize=1024 and MaxValueSize=8 MiB, got %d and %d", wire.MaxKeySize, wire.MaxValueSize)
	}
	tooBig := []wire.Frame{
		{Op: wire.OpSet, Key: strings.Repeat("k", wire.MaxKeySize+1)},
		{Op: wire.OpSet, Key: "k", Value: make([]byte, wire.MaxValueSize+1)},
	}
	for _, f := range tooBig {
		if _, err := wire.EncodeFrame(f); !errors.Is(err, wire.ErrFrameTooLarge) {
			t.Errorf("EncodeFrame over a limit: want ErrFrameTooLarge, got %v", err)
		}
	}
	raw := []byte{0x01, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xff, 0xff}
	if _, err := wire.DecodeFrame(raw); !errors.Is(err, wire.ErrFrameTooLarge) {
		t.Errorf("a key length of 65535 is over MaxKeySize: want ErrFrameTooLarge (checked before slicing), got %v", err)
	}
	big := append(append([]byte{0x02}, make([]byte, 16)...), 0, 1, 'k')
	big = append(big, make([]byte, wire.MaxValueSize+1)...)
	if _, err := wire.DecodeFrame(big); !errors.Is(err, wire.ErrFrameTooLarge) {
		t.Errorf("a value over MaxValueSize: want ErrFrameTooLarge, got %v", err)
	}
}

func TestL2_2_DecodeCopies(t *testing.T) {
	p, _ := wire.EncodeFrame(wire.Frame{Op: wire.OpSet, Key: "k", Value: []byte("value")})
	f, _ := wire.DecodeFrame(p)
	for i := range p {
		p[i] = 0
	}
	if string(f.Value) != "value" || f.Key != "k" {
		t.Errorf("a decoded frame must not share memory with the payload it came from")
	}
}

func FuzzL2_2_DecodeFrame(f *testing.F) {
	for _, fr := range []wire.Frame{{Op: wire.OpSet, Key: "k", Value: []byte("v")}, {Op: wire.OpPing}, {Op: wire.OpCAS, Key: "x", Version: 9, TTL: 5}} {
		p, _ := wire.EncodeFrame(fr)
		f.Add(p)
	}
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, p []byte) {
		fr, err := wire.DecodeFrame(p)
		if err != nil {
			return
		}
		out, err := wire.EncodeFrame(fr)
		if err != nil {
			t.Fatalf("a frame DecodeFrame accepted can't be encoded: %v", err)
		}
		if !bytes.Equal(out, p) {
			t.Fatalf("decode then encode changed the bytes")
		}
	})
}
