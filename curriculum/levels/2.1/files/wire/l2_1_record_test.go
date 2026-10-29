package wire_test

import (
	"bytes"
	"errors"
	"io"
	"math/rand/v2"
	"testing"
	"testing/iotest"

	"burrow/wire"
)

func TestL2_1_GoldenBytes(t *testing.T) {
	var buf bytes.Buffer
	if err := wire.WriteRecord(&buf, []byte("hi")); err != nil {
		t.Fatal(err)
	}
	if want := []byte{0, 0, 0, 2, 'h', 'i'}; !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("WriteRecord(\"hi\") = % x, want % x (uint32 big-endian length, then the payload)", buf.Bytes(), want)
	}
	buf.Reset()
	wire.WriteRecord(&buf, nil)
	if want := []byte{0, 0, 0, 0}; !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("an empty record is just a zero length: got % x", buf.Bytes())
	}
}

func TestL2_1_RoundTrip(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	payload := func(n int) []byte {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte(r.Uint32())
		}
		return b
	}
	cases := [][]byte{{}, {0}, payload(1 << 20), payload(wire.MaxRecordSize)}
	for range 20 {
		cases = append(cases, payload(r.IntN(5000)))
	}
	var stream bytes.Buffer
	for i, p := range cases {
		if err := wire.WriteRecord(&stream, p); err != nil {
			t.Fatalf("case %d (%d bytes): WriteRecord: %v", i, len(p), err)
		}
	}
	for i, p := range cases {
		got, err := wire.ReadRecord(&stream)
		if err != nil {
			t.Fatalf("case %d: ReadRecord: %v", i, err)
		}
		if !bytes.Equal(got, p) {
			t.Fatalf("case %d: %d bytes came back as %d bytes (or changed)", i, len(p), len(got))
		}
	}
	if _, err := wire.ReadRecord(&stream); err != io.EOF {
		t.Errorf("after the last record, ReadRecord should return exactly io.EOF (a clean end), got %v", err)
	}
}

func TestL2_1_EOFRules(t *testing.T) {
	if _, err := wire.ReadRecord(bytes.NewReader(nil)); err != io.EOF {
		t.Errorf("an empty stream is a clean end: want io.EOF itself (not wrapped), got %v", err)
	}
	var buf bytes.Buffer
	wire.WriteRecord(&buf, []byte("payload"))
	full := buf.Bytes()
	for cut := 1; cut < len(full); cut++ {
		_, err := wire.ReadRecord(bytes.NewReader(full[:cut]))
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Errorf("a record cut after %d of %d bytes: want io.ErrUnexpectedEOF, got %v", cut, len(full), err)
		}
	}
}

func TestL2_1_SlowReaders(t *testing.T) {
	var buf bytes.Buffer
	want := bytes.Repeat([]byte("z"), 1000)
	wire.WriteRecord(&buf, want)
	raw := buf.Bytes()
	readers := map[string]io.Reader{
		"one byte at a time":  iotest.OneByteReader(bytes.NewReader(raw)),
		"half reads":          iotest.HalfReader(bytes.NewReader(raw)),
		"data with final EOF": iotest.DataErrReader(bytes.NewReader(raw)),
	}
	for name, r := range readers {
		got, err := wire.ReadRecord(r)
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s: ReadRecord = (%d bytes, %v). A single Read can return fewer bytes than you asked for: use io.ReadFull.",
				name, len(got), err)
		}
	}
}

func TestL2_1_HugeLengthDoesNotAllocate(t *testing.T) {
	for _, hdr := range [][]byte{{0xff, 0xff, 0xff, 0xff}, {0x01, 0x00, 0x00, 0x01}} {
		allocs := testing.AllocsPerRun(5, func() {
			_, err := wire.ReadRecord(bytes.NewReader(hdr))
			if !errors.Is(err, wire.ErrRecordTooLarge) {
				t.Errorf("header % x claims more than MaxRecordSize: want ErrRecordTooLarge, got %v", hdr, err)
			}
		})
		if allocs > 5 {
			t.Errorf("rejecting header % x made %v allocations; check the length before make([]byte, n)", hdr, allocs)
		}
	}
	var big bytes.Buffer
	big.Write([]byte{0x01, 0x00, 0x00, 0x00}) // exactly MaxRecordSize (16 MiB)
	big.Write(make([]byte, wire.MaxRecordSize))
	if p, err := wire.ReadRecord(&big); err != nil || len(p) != wire.MaxRecordSize {
		t.Errorf("a record of exactly MaxRecordSize is allowed: (%d, %v)", len(p), err)
	}
}

func TestL2_1_WriteLimits(t *testing.T) {
	var buf bytes.Buffer
	if err := wire.WriteRecord(&buf, make([]byte, wire.MaxRecordSize+1)); !errors.Is(err, wire.ErrRecordTooLarge) {
		t.Errorf("WriteRecord over the limit: want ErrRecordTooLarge, got %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("a rejected WriteRecord must not write anything (wrote %d bytes)", buf.Len())
	}
}

type countingWriter struct {
	calls int
	bytes.Buffer
}

func (c *countingWriter) Write(p []byte) (int, error) {
	c.calls++
	return c.Buffer.Write(p)
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) / 2, nil }

type failWriter struct{}

var errDisk = errors.New("disk on fire")

func (failWriter) Write(p []byte) (int, error) { return 0, errDisk }

func TestL2_1_OneWritePerRecord(t *testing.T) {
	var w countingWriter
	wire.WriteRecord(&w, []byte("payload"))
	if w.calls != 1 {
		t.Errorf("WriteRecord made %d Write calls. Build header and payload in one slice and write once: "+
			"goroutines sharing a connection would otherwise interleave pieces.", w.calls)
	}
	if err := wire.WriteRecord(shortWriter{}, []byte("x")); !errors.Is(err, io.ErrShortWrite) {
		t.Errorf("a writer that accepts only part without an error: want io.ErrShortWrite, got %v", err)
	}
	if err := wire.WriteRecord(failWriter{}, []byte("x")); !errors.Is(err, errDisk) {
		t.Errorf("WriteRecord should return the writer's error, got %v", err)
	}
}

func TestL2_1_FreshSlice(t *testing.T) {
	raw := []byte{0, 0, 0, 3, 'a', 'b', 'c'}
	got, err := wire.ReadRecord(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	raw[4] = 'X'
	if string(got) != "abc" {
		t.Errorf("the slice ReadRecord returns must not share memory with the reader's buffer")
	}
}

func TestL2_1_AppendRecord(t *testing.T) {
	var buf []byte
	var err error
	for _, p := range []string{"one", "", "three"} {
		if buf, err = wire.AppendRecord(buf, []byte(p)); err != nil {
			t.Fatal(err)
		}
	}
	r := bytes.NewReader(buf)
	for _, want := range []string{"one", "", "three"} {
		got, err := wire.ReadRecord(r)
		if err != nil || string(got) != want {
			t.Fatalf("records built with AppendRecord: got (%q, %v), want %q", got, err, want)
		}
	}
	prefix := []byte("keep")
	out, err := wire.AppendRecord(prefix, make([]byte, wire.MaxRecordSize+1))
	if !errors.Is(err, wire.ErrRecordTooLarge) || !bytes.Equal(out, []byte("keep")) {
		t.Errorf("AppendRecord over the limit returns ErrRecordTooLarge and leaves buf unchanged, got (%q, %v)", out, err)
	}
}

func FuzzL2_1_ReadRecord(f *testing.F) {
	f.Add([]byte{0, 0, 0, 2, 'h', 'i'})
	f.Add([]byte{})
	f.Add([]byte{0xff, 0xff, 0xff, 0xff})
	f.Add([]byte{0, 0, 0, 0, 0, 0, 0, 1, 'x'})
	f.Fuzz(func(t *testing.T, data []byte) {
		p, err := wire.ReadRecord(bytes.NewReader(data))
		if err != nil {
			return
		}
		var out bytes.Buffer
		if err := wire.WriteRecord(&out, p); err != nil {
			t.Fatalf("a record ReadRecord accepted can't be written back: %v", err)
		}
		if !bytes.HasPrefix(data, out.Bytes()) {
			t.Fatalf("re-encoding a decoded record gave different bytes")
		}
	})
}
