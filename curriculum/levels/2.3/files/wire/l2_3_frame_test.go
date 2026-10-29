package wire_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math/rand/v2"
	"runtime"
	"strings"
	"testing"
	"testing/iotest"

	"burrow/wire"
)

func TestL2_3_GoldenBytes(t *testing.T) {
	var buf bytes.Buffer
	if err := wire.WriteFrame(&buf, wire.Frame{Op: wire.OpSet, Key: "k", Value: []byte("v")}); err != nil {
		t.Fatal(err)
	}
	want := []byte{0x02, 0, 0, 0, 1, 'k', 0, 0, 0, 1, 'v'}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("SET k=v encoded as % x\nwant              % x\n(op byte, uint32 big-endian key length, key, uint32 big-endian value length, value)", buf.Bytes(), want)
	}
}

func TestL2_3_OpValues(t *testing.T) {
	ops := map[wire.Op]byte{
		wire.OpGet: 0x01, wire.OpSet: 0x02, wire.OpDelete: 0x03, wire.OpPing: 0x04,
		wire.OpOK: 0x80, wire.OpValue: 0x81, wire.OpNotFound: 0x82, wire.OpError: 0x83, wire.OpPong: 0x84,
	}
	for op, b := range ops {
		if byte(op) != b {
			t.Errorf("%v should be 0x%02x, is 0x%02x", op, b, byte(op))
		}
	}
	names := map[wire.Op]string{wire.OpGet: "GET", wire.OpSet: "SET", wire.OpNotFound: "NOT_FOUND", wire.Op(0x7f): "Op(0x7f)"}
	for op, want := range names {
		if got := op.String(); got != want {
			t.Errorf("Op(0x%02x).String() = %q, want %q", byte(op), got, want)
		}
	}
}

func TestL2_3_RoundTrip(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	cases := []wire.Frame{
		{Op: wire.OpGet, Key: "a"},
		{Op: wire.OpSet, Key: "line\nbreak", Value: []byte("nul\x00inside\n")},
		{Op: wire.OpPing},
		{Op: wire.OpValue, Key: "", Value: []byte{}},
		{Op: wire.OpError, Value: []byte("something broke")},
	}
	for range 50 {
		k := make([]byte, r.IntN(200))
		v := make([]byte, r.IntN(5000))
		for i := range k {
			k[i] = byte(r.Uint32())
		}
		for i := range v {
			v[i] = byte(r.Uint32())
		}
		cases = append(cases, wire.Frame{Op: wire.OpSet, Key: string(k), Value: v})
	}
	for i, f := range cases {
		var buf bytes.Buffer
		if err := wire.WriteFrame(&buf, f); err != nil {
			t.Fatalf("case %d: WriteFrame: %v", i, err)
		}
		got, err := wire.ReadFrame(&buf)
		if err != nil {
			t.Fatalf("case %d: ReadFrame: %v", i, err)
		}
		if got.Op != f.Op || got.Key != f.Key || !bytes.Equal(got.Value, f.Value) {
			t.Fatalf("case %d: round trip changed the frame: got %v/%q/%d bytes", i, got.Op, got.Key, len(got.Value))
		}
		if buf.Len() != 0 {
			t.Fatalf("case %d: ReadFrame left %d unread bytes", i, buf.Len())
		}
	}
}

func TestL2_3_BackToBackFrames(t *testing.T) {
	var buf bytes.Buffer
	for i := range 3 {
		wire.WriteFrame(&buf, wire.Frame{Op: wire.OpSet, Key: string(rune('a' + i)), Value: []byte{byte(i)}})
	}
	for i := range 3 {
		f, err := wire.ReadFrame(&buf)
		if err != nil || f.Key != string(rune('a'+i)) {
			t.Fatalf("frame %d from one buffer: (%v, %v)", i, f, err)
		}
	}
	if _, err := wire.ReadFrame(&buf); err != io.EOF {
		t.Errorf("after the last frame, ReadFrame should return exactly io.EOF (a clean end), got %v", err)
	}
}

func TestL2_3_CleanEOF(t *testing.T) {
	_, err := wire.ReadFrame(bytes.NewReader(nil))
	if err != io.EOF {
		t.Errorf("ReadFrame on an empty stream should return io.EOF itself (not wrapped), got %v", err)
	}
}

func TestL2_3_TruncatedFrames(t *testing.T) {
	var buf bytes.Buffer
	wire.WriteFrame(&buf, wire.Frame{Op: wire.OpSet, Key: "key", Value: []byte("value")})
	full := buf.Bytes()
	for cut := 1; cut < len(full); cut++ {
		_, err := wire.ReadFrame(bytes.NewReader(full[:cut]))
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Errorf("a frame cut after %d of %d bytes should give io.ErrUnexpectedEOF, got %v", cut, len(full), err)
		}
	}
}

func TestL2_3_SlowReaders(t *testing.T) {
	var buf bytes.Buffer
	want := wire.Frame{Op: wire.OpSet, Key: "slow", Value: bytes.Repeat([]byte("z"), 1000)}
	wire.WriteFrame(&buf, want)
	raw := buf.Bytes()
	readers := map[string]io.Reader{
		"one byte at a time":  iotest.OneByteReader(bytes.NewReader(raw)),
		"half reads":          iotest.HalfReader(bytes.NewReader(raw)),
		"data with final EOF": iotest.DataErrReader(bytes.NewReader(raw)),
	}
	for name, r := range readers {
		got, err := wire.ReadFrame(r)
		if err != nil || got.Key != want.Key || !bytes.Equal(got.Value, want.Value) {
			t.Errorf("%s: ReadFrame = (%q, %d bytes, %v). A single Read can return fewer bytes than you asked for: use io.ReadFull.",
				name, got.Key, len(got.Value), err)
		}
	}
}

func TestL2_3_UnknownOp(t *testing.T) {
	_, err := wire.ReadFrame(bytes.NewReader([]byte{0x7f, 0, 0, 0, 0, 0, 0, 0, 0}))
	if !errors.Is(err, wire.ErrUnknownOp) {
		t.Errorf("op byte 0x7f isn't defined: want ErrUnknownOp, got %v", err)
	}
	var buf bytes.Buffer
	if err := wire.WriteFrame(&buf, wire.Frame{Op: 0x7f}); !errors.Is(err, wire.ErrUnknownOp) {
		t.Errorf("WriteFrame should refuse an unknown op too, got %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("a rejected WriteFrame must not write anything, wrote %d bytes", buf.Len())
	}
}

func TestL2_3_OversizedLengthsDontAllocate(t *testing.T) {
	cases := map[string][]byte{
		"key length 4 GiB":   {0x02, 0xff, 0xff, 0xff, 0xff},
		"key length 1 MiB":   {0x02, 0x00, 0x10, 0x00, 0x00},
		"value length 4 GiB": {0x02, 0, 0, 0, 1, 'k', 0xff, 0xff, 0xff, 0xff},
		"value length 9 MiB": {0x02, 0, 0, 0, 1, 'k', 0x00, 0x90, 0x00, 0x00},
	}
	for name, raw := range cases {
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		_, err := wire.ReadFrame(bytes.NewReader(raw))
		runtime.ReadMemStats(&after)
		if !errors.Is(err, wire.ErrFrameTooLarge) {
			t.Errorf("%s: want ErrFrameTooLarge, got %v", name, err)
		}
		if grew := after.TotalAlloc - before.TotalAlloc; grew > 1<<20 {
			t.Errorf("%s: ReadFrame allocated %d bytes before rejecting the frame. "+
				"Check the length against the limit before you make([]byte, n).", name, grew)
		}
	}
}

func TestL2_3_Limits(t *testing.T) {
	if wire.MaxKeySize != 1024 || wire.MaxValueSize != 8<<20 {
		t.Fatalf("limits should be MaxKeySize=1024 and MaxValueSize=8 MiB, got %d and %d", wire.MaxKeySize, wire.MaxValueSize)
	}
	ok := []wire.Frame{
		{Op: wire.OpSet, Key: strings.Repeat("k", wire.MaxKeySize)},
		{Op: wire.OpSet, Key: "k", Value: make([]byte, wire.MaxValueSize)},
	}
	for _, f := range ok {
		var buf bytes.Buffer
		if err := wire.WriteFrame(&buf, f); err != nil {
			t.Errorf("a frame exactly at the limit is allowed: %v", err)
			continue
		}
		if _, err := wire.ReadFrame(&buf); err != nil {
			t.Errorf("reading a frame exactly at the limit: %v", err)
		}
	}
	tooBig := []wire.Frame{
		{Op: wire.OpSet, Key: strings.Repeat("k", wire.MaxKeySize+1)},
		{Op: wire.OpSet, Key: "k", Value: make([]byte, wire.MaxValueSize+1)},
	}
	for _, f := range tooBig {
		var buf bytes.Buffer
		if err := wire.WriteFrame(&buf, f); !errors.Is(err, wire.ErrFrameTooLarge) {
			t.Errorf("WriteFrame over the limit: want ErrFrameTooLarge, got %v", err)
		}
		if buf.Len() != 0 {
			t.Errorf("a rejected WriteFrame must not write a partial frame (%d bytes written)", buf.Len())
		}
	}
}

// countingWriter records how many Write calls a frame takes.
type countingWriter struct {
	calls int
	bytes.Buffer
}

func (c *countingWriter) Write(p []byte) (int, error) {
	c.calls++
	return c.Buffer.Write(p)
}

func TestL2_3_OneWritePerFrame(t *testing.T) {
	var w countingWriter
	wire.WriteFrame(&w, wire.Frame{Op: wire.OpSet, Key: "k", Value: []byte("v")})
	if w.calls != 1 {
		t.Errorf("WriteFrame made %d Write calls. Build the whole frame in memory and write it once: "+
			"several goroutines writing to one connection could otherwise interleave their pieces.", w.calls)
	}
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) / 2, nil }

type failWriter struct{}

var errDisk = errors.New("disk on fire")

func (failWriter) Write(p []byte) (int, error) { return 0, errDisk }

func TestL2_3_WriterErrors(t *testing.T) {
	f := wire.Frame{Op: wire.OpGet, Key: "k"}
	if err := wire.WriteFrame(shortWriter{}, f); !errors.Is(err, io.ErrShortWrite) {
		t.Errorf("a writer that accepts only part of the frame without an error should produce io.ErrShortWrite, got %v", err)
	}
	if err := wire.WriteFrame(failWriter{}, f); !errors.Is(err, errDisk) {
		t.Errorf("WriteFrame should return the writer's error, got %v", err)
	}
}

func TestL2_3_HeaderIsBigEndian(t *testing.T) {
	raw := []byte{0x01, 0, 0, 0, 0}
	raw = binary.BigEndian.AppendUint32(raw[:1], 3)
	raw = append(raw, "abc"...)
	raw = binary.BigEndian.AppendUint32(raw, 0)
	f, err := wire.ReadFrame(bytes.NewReader(raw))
	if err != nil || f.Op != wire.OpGet || f.Key != "abc" || len(f.Value) != 0 {
		t.Errorf("decoding a hand-built GET abc frame: (%v, %q, %v)", f.Op, f.Key, err)
	}
}

func FuzzL2_3_ReadFrame(f *testing.F) {
	var buf bytes.Buffer
	wire.WriteFrame(&buf, wire.Frame{Op: wire.OpSet, Key: "k", Value: []byte("v")})
	f.Add(buf.Bytes())
	f.Add([]byte{})
	f.Add([]byte{0x02, 0xff, 0xff, 0xff, 0xff})
	f.Add([]byte{0x81, 0, 0, 0, 0, 0, 0, 0, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		fr, err := wire.ReadFrame(bytes.NewReader(data))
		if err != nil {
			return
		}
		var out bytes.Buffer
		if err := wire.WriteFrame(&out, fr); err != nil {
			t.Fatalf("a frame ReadFrame accepted can't be written back: %v", err)
		}
		if !bytes.HasPrefix(data, out.Bytes()) {
			t.Fatalf("re-encoding a decoded frame gave different bytes")
		}
	})
}
