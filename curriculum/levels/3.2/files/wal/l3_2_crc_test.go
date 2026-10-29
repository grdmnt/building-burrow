package wal_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"burrow/wal"
)

// buildLog writes n records and returns the file path plus the file size
// after each append, so tests can find record boundaries without knowing
// your on-disk format.
func buildLog(t *testing.T, n int) (string, []int64, [][]byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wal.log")
	l, err := wal.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var bounds []int64
	var recs [][]byte
	for i := range n {
		rec := []byte(fmt.Sprintf("record number %d with some padding to make it longer", i))
		if err := l.Append(rec); err != nil {
			t.Fatal(err)
		}
		info, _ := os.Stat(path)
		bounds = append(bounds, info.Size())
		recs = append(recs, rec)
	}
	l.Close()
	return path, bounds, recs
}

func copyFile(t *testing.T, src string, mutate func([]byte) []byte) string {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "wal.log")
	if err := os.WriteFile(dst, mutate(data), 0o644); err != nil {
		t.Fatal(err)
	}
	return dst
}

func TestL3_2_TornTailAtEveryOffset(t *testing.T) {
	path, bounds, recs := buildLog(t, 5)
	lastGood := bounds[3]
	for cut := lastGood + 1; cut < bounds[4]; cut++ {
		t.Run(fmt.Sprintf("cut at %d", cut), func(t *testing.T) {
			p := copyFile(t, path, func(b []byte) []byte { return b[:cut] })
			l, err := wal.Open(p)
			if err != nil {
				t.Fatalf("a log whose last record was cut short (a torn write from a crash) must still open, got %v", err)
			}
			defer l.Close()
			sameRecords(t, replay(t, l), recs[:4])
			if info, _ := os.Stat(p); info.Size() != lastGood {
				t.Errorf("Open should truncate the torn tail: file is %d bytes, want %d", info.Size(), lastGood)
			}
			if err := l.Append([]byte("after repair")); err != nil {
				t.Fatal(err)
			}
			got := replay(t, l)
			if len(got) != 5 || string(got[4]) != "after repair" {
				t.Errorf("after repairing, new appends must follow the last good record; got %d records", len(got))
			}
		})
	}
}

func TestL3_2_DamagedLastRecordIsATornTail(t *testing.T) {
	path, bounds, recs := buildLog(t, 5)
	mid := (bounds[3] + bounds[4]) / 2
	p := copyFile(t, path, func(b []byte) []byte { b[mid] ^= 0xff; return b })
	l, err := wal.Open(p)
	if err != nil {
		t.Fatalf("a bad final record looks exactly like a torn write: Open should drop it, got %v", err)
	}
	defer l.Close()
	sameRecords(t, replay(t, l), recs[:4])
}

func TestL3_2_CorruptionMidLogIsAnError(t *testing.T) {
	path, bounds, _ := buildLog(t, 5)
	mid := (bounds[1] + bounds[2]) / 2 // inside record 3 of 5
	p := copyFile(t, path, func(b []byte) []byte { b[mid] ^= 0x01; return b })
	before, _ := os.ReadFile(p)
	l, err := wal.Open(p)
	if err == nil {
		l.Close()
		t.Fatalf("a flipped bit in the middle of the log, with good records after it, is real corruption: " +
			"Open must fail rather than silently drop records 3-5")
	}
	if !errors.Is(err, wal.ErrCorrupt) {
		t.Errorf("want an error matching wal.ErrCorrupt, got %v", err)
	}
	after, _ := os.ReadFile(p)
	if !bytes.Equal(before, after) {
		t.Errorf("Open must not modify a corrupt log: someone needs to inspect it")
	}
}

func TestL3_2_EveryBitFlipIsCaught(t *testing.T) {
	path, bounds, recs := buildLog(t, 3)
	start, end := bounds[0], bounds[1] // all of record 2 of 3, header included
	for off := start; off < end; off++ {
		p := copyFile(t, path, func(b []byte) []byte { b[off] ^= 0x10; return b })
		l, err := wal.Open(p)
		if err != nil {
			continue // detected as corruption: good
		}
		got := replay(t, l)
		l.Close()
		for i, r := range got {
			if i < len(recs) && !bytes.Equal(r, recs[i]) {
				t.Fatalf("flipping a bit at offset %d produced a record that doesn't match what was written: %q. "+
					"Your checksum must cover every byte that can change a record, including its length.", off, r)
			}
		}
		if len(got) > len(recs) {
			t.Fatalf("flipping a bit at offset %d produced extra records", off)
		}
	}
}

func TestL3_2_ZeroFilledTail(t *testing.T) {
	path, _, recs := buildLog(t, 4)
	p := copyFile(t, path, func(b []byte) []byte { return append(b, make([]byte, 4096)...) })
	l, err := wal.Open(p)
	if err != nil {
		t.Fatalf("a file with zeros after the last record (preallocated space after a crash) should open, got %v", err)
	}
	defer l.Close()
	got := replay(t, l)
	if len(got) != 4 {
		t.Fatalf("zero bytes after the last record produced %d records, want 4. "+
			"Careful: CRC32 of nothing is 0, so an all-zero header can look like a valid empty record.", len(got))
	}
	sameRecords(t, got, recs)
}

func TestL3_2_GarbageLengthDoesNotAllocate(t *testing.T) {
	path, _, recs := buildLog(t, 2)
	p := copyFile(t, path, func(b []byte) []byte {
		return append(b, bytes.Repeat([]byte{0xff}, 16)...)
	})
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	l, err := wal.Open(p)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatalf("garbage after the last record is a torn tail: %v", err)
	}
	defer l.Close()
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 64<<20 {
		t.Errorf("Open allocated %d MB because of a garbage length field. Check lengths against MaxRecordSize before allocating.", grew>>20)
	}
	sameRecords(t, replay(t, l), recs)
}

func TestL3_2_EmptyRecordsSurvive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.log")
	l := open(t, path)
	appendAll(t, l, []byte{}, []byte("x"), []byte{})
	l.Close()
	l2 := open(t, path)
	sameRecords(t, replay(t, l2), [][]byte{{}, []byte("x"), {}})
}
