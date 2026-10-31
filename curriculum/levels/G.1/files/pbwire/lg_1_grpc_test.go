package pbwire_test

import (
	"bytes"
	"errors"
	"io"
	"runtime"
	"testing"
	"testing/iotest"

	"burrow/pbwire"
	"burrow/store"
	"burrow/watch"
)

func TestLG_1_GRPCFrameGolden(t *testing.T) {
	var buf bytes.Buffer
	if err := pbwire.WriteGRPCMessage(&buf, []byte("hi")); err != nil {
		t.Fatal(err)
	}
	if want := h("00 00 00 00 02 68 69"); !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("WriteGRPCMessage(\"hi\") = % x, want % x (flag 0, uint32 big-endian length, the message)",
			buf.Bytes(), want)
	}
	buf.Reset()
	pbwire.WriteGRPCMessage(&buf, nil)
	if want := h("00 00 00 00 00"); !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("an empty message is a five-byte header and nothing else: got % x", buf.Bytes())
	}
	buf.Reset()
	ev := watch.Event{ID: 7, Type: "ready"}
	pbwire.WriteGRPCMessage(&buf, pbwire.MarshalEvent(ev))
	if want := h("00 00 00 00 04 08 07 10 04"); !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("a ready Event in a gRPC frame: got % x, want % x", buf.Bytes(), want)
	}
}

func TestLG_1_GRPCRoundTrip(t *testing.T) {
	msgs := [][]byte{
		{},
		pbwire.MarshalEvent(watch.Event{ID: 1, Type: "put", Entry: store.Entry{Key: "k", Value: []byte("v")}}),
		bytes.Repeat([]byte{0xab}, 70000),
		pbwire.MarshalEntry(store.Entry{Key: "k"}),
	}
	var stream bytes.Buffer
	for _, m := range msgs {
		if err := pbwire.WriteGRPCMessage(&stream, m); err != nil {
			t.Fatal(err)
		}
	}
	raw := stream.Bytes()
	readers := map[string]func() io.Reader{
		"bytes.Reader":        func() io.Reader { return bytes.NewReader(raw) },
		"one byte at a time":  func() io.Reader { return iotest.OneByteReader(bytes.NewReader(raw)) },
		"half reads":          func() io.Reader { return iotest.HalfReader(bytes.NewReader(raw)) },
		"data with final EOF": func() io.Reader { return iotest.DataErrReader(bytes.NewReader(raw)) },
	}
	for name, mk := range readers {
		r := mk()
		for i, want := range msgs {
			got, err := pbwire.ReadGRPCMessage(r, 1<<20)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("%s: message %d = (%d bytes, %v), want %d bytes. Use io.ReadFull.",
					name, i, len(got), err, len(want))
			}
			if got == nil {
				t.Fatalf("%s: message %d: return a non-nil slice, even for an empty message", name, i)
			}
		}
		if _, err := pbwire.ReadGRPCMessage(r, 1<<20); err != io.EOF {
			t.Errorf("%s: after the last message want io.EOF itself, got %v", name, err)
		}
	}
}

func TestLG_1_GRPCEOFRules(t *testing.T) {
	if _, err := pbwire.ReadGRPCMessage(bytes.NewReader(nil), 100); err != io.EOF {
		t.Errorf("an empty stream is a clean end: want io.EOF itself (not wrapped), got %v", err)
	}
	full := h("00 00 00 00 03 61 62 63")
	for cut := 1; cut < len(full); cut++ {
		_, err := pbwire.ReadGRPCMessage(bytes.NewReader(full[:cut]), 100)
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Errorf("a message cut after %d of %d bytes: want io.ErrUnexpectedEOF, got %v", cut, len(full), err)
		}
	}
}

func TestLG_1_GRPCTooLarge(t *testing.T) {
	const max = 8 << 20
	for _, hdr := range []string{"00 00 80 00 01", "00 ff ff ff ff"} {
		in := h(hdr)
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		_, err := pbwire.ReadGRPCMessage(bytes.NewReader(in), max)
		runtime.ReadMemStats(&after)
		if !errors.Is(err, pbwire.ErrTooLarge) {
			t.Errorf("header %s claims more than max (%d): want ErrTooLarge, got %v", hdr, max, err)
		}
		if grew := after.TotalAlloc - before.TotalAlloc; grew > 64<<10 {
			t.Errorf("rejecting header %s allocated %d bytes: check the length against max before make", hdr, grew)
		}
	}
	exact := append(h("00 00 00 00 04"), "abcd"...)
	if m, err := pbwire.ReadGRPCMessage(bytes.NewReader(exact), 4); err != nil || string(m) != "abcd" {
		t.Errorf("a message of exactly max bytes is allowed: (%q, %v)", m, err)
	}
	if _, err := pbwire.ReadGRPCMessage(bytes.NewReader(exact), 3); !errors.Is(err, pbwire.ErrTooLarge) {
		t.Errorf("4 bytes with max 3: want ErrTooLarge, got %v", err)
	}
	if m, err := pbwire.ReadGRPCMessage(bytes.NewReader(h("00 00 00 00 00")), 0); err != nil || len(m) != 0 {
		t.Errorf("max 0 still allows an empty message: (%q, %v)", m, err)
	}
	hostile := h("00 7f ff ff ff")
	rd := bytes.NewReader(hostile)
	allocs := testing.AllocsPerRun(10, func() {
		rd.Reset(hostile)
		pbwire.ReadGRPCMessage(rd, 1<<20)
	})
	if allocs > 2 {
		t.Errorf("rejecting an oversized header made %v allocations; read it into a [5]byte array "+
			"and check the length before make", allocs)
	}
}

func TestLG_1_GRPCCompressed(t *testing.T) {
	for _, in := range []string{"01 00 00 00 01 61", "01 ff ff ff ff", "80 00 00 00 00"} {
		_, err := pbwire.ReadGRPCMessage(bytes.NewReader(h(in)), 100)
		if !errors.Is(err, pbwire.ErrCompressed) {
			t.Errorf("ReadGRPCMessage(%s): a non-zero flag byte means a compressed message, "+
				"which Burrow never asks for: want ErrCompressed (checked before the length), got %v", in, err)
		}
	}
}

func TestLG_1_GRPCWrites(t *testing.T) {
	var w countingWriter
	pbwire.WriteGRPCMessage(&w, []byte("payload"))
	if w.calls != 1 {
		t.Errorf("WriteGRPCMessage made %d Write calls: build header and message in one slice and write once",
			w.calls)
	}
	if err := pbwire.WriteGRPCMessage(shortWriter{}, []byte("x")); !errors.Is(err, io.ErrShortWrite) {
		t.Errorf("a writer that accepts only part without an error: want io.ErrShortWrite, got %v", err)
	}
	if err := pbwire.WriteGRPCMessage(failWriter{}, []byte("x")); !errors.Is(err, errDisk) {
		t.Errorf("WriteGRPCMessage should return the writer's error, got %v", err)
	}
	raw := h("00 00 00 00 03 61 62 63")
	got, err := pbwire.ReadGRPCMessage(bytes.NewReader(raw), 10)
	if err != nil {
		t.Fatal(err)
	}
	raw[5] = 'X'
	if string(got) != "abc" {
		t.Errorf("the slice ReadGRPCMessage returns must not share memory with the reader's buffer")
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
