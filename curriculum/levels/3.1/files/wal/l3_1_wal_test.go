package wal_test

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"burrow/wal"
)

func logPath(t *testing.T) string { return filepath.Join(t.TempDir(), "wal.log") }

func open(t *testing.T, path string, opts ...wal.Option) *wal.Log {
	t.Helper()
	l, err := wal.Open(path, opts...)
	if err != nil {
		t.Fatalf("wal.Open(%s): %v", path, err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}

func appendAll(t *testing.T, l *wal.Log, recs ...[]byte) {
	t.Helper()
	for i, r := range recs {
		if err := l.Append(r); err != nil {
			t.Fatalf("Append #%d: %v", i, err)
		}
	}
}

func replay(t *testing.T, l *wal.Log) [][]byte {
	t.Helper()
	var out [][]byte
	err := l.Replay(func(rec []byte) error {
		out = append(out, append([]byte(nil), rec...))
		return nil
	})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	return out
}

func sameRecords(t *testing.T, got, want [][]byte) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("Replay gave %d records, want %d", len(got), len(want))
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("record %d = %.40q, want %.40q", i, got[i], want[i])
		}
	}
}

func TestL3_1_AppendReplayInOrder(t *testing.T) {
	l := open(t, logPath(t))
	want := [][]byte{
		[]byte("first"),
		{},
		[]byte("nul\x00and\nnewline"),
		bytes.Repeat([]byte{0xab}, 1<<20),
	}
	for i := range 100 {
		want = append(want, []byte(fmt.Sprintf("record-%03d", i)))
	}
	appendAll(t, l, want...)
	sameRecords(t, replay(t, l), want)
}

func TestL3_1_ReplayEmptyLog(t *testing.T) {
	l := open(t, logPath(t))
	calls := 0
	if err := l.Replay(func([]byte) error { calls++; return nil }); err != nil {
		t.Fatalf("Replay on a new log: %v", err)
	}
	if calls != 0 {
		t.Errorf("a new log has no records, but Replay called back %d times", calls)
	}
}

func TestL3_1_ReopenKeepsRecords(t *testing.T) {
	path := logPath(t)
	l, err := wal.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	appendAll(t, l, []byte("a"), []byte("b"), []byte("c"))
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	l2 := open(t, path)
	appendAll(t, l2, []byte("d"), []byte("e"))
	sameRecords(t, replay(t, l2), [][]byte{[]byte("a"), []byte("b"), []byte("c"), []byte("d"), []byte("e")})
}

func TestL3_1_ReplayIsRepeatable(t *testing.T) {
	l := open(t, logPath(t))
	appendAll(t, l, []byte("1"), []byte("2"))
	sameRecords(t, replay(t, l), [][]byte{[]byte("1"), []byte("2")})
	appendAll(t, l, []byte("3"))
	sameRecords(t, replay(t, l), [][]byte{[]byte("1"), []byte("2"), []byte("3")})
	appendAll(t, l, []byte("4"))
	if got := replay(t, l); len(got) != 4 {
		t.Errorf("Replay must always read from the start of the log and see every append; got %d records", len(got))
	}
}

func TestL3_1_ReplayCallbackError(t *testing.T) {
	l := open(t, logPath(t))
	appendAll(t, l, []byte("1"), []byte("2"), []byte("3"))
	stop := errors.New("stop here")
	seen := 0
	err := l.Replay(func(rec []byte) error {
		seen++
		if string(rec) == "2" {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) {
		t.Errorf("Replay should return the callback's error, got %v", err)
	}
	if seen != 2 {
		t.Errorf("Replay should stop at the first callback error; it made %d calls", seen)
	}
}

func TestL3_1_CallbackMayNotKeepBuffer(t *testing.T) {
	// Document the contract both ways: callers copy if they keep a record,
	// and the log must not depend on callers leaving the slice alone.
	l := open(t, logPath(t))
	appendAll(t, l, []byte("aaaa"), []byte("bbbb"))
	_ = l.Replay(func(rec []byte) error {
		for i := range rec {
			rec[i] = 'X'
		}
		return nil
	})
	sameRecords(t, replay(t, l), [][]byte{[]byte("aaaa"), []byte("bbbb")})
}

func TestL3_1_AppendCopiesNothingItShouldnt(t *testing.T) {
	l := open(t, logPath(t))
	buf := []byte("original")
	appendAll(t, l, buf)
	copy(buf, "MUTATED!")
	sameRecords(t, replay(t, l), [][]byte{[]byte("original")})
}

func TestL3_1_RecordTooLarge(t *testing.T) {
	l := open(t, logPath(t))
	if err := l.Append(make([]byte, wal.MaxRecordSize+1)); !errors.Is(err, wal.ErrRecordTooLarge) {
		t.Errorf("a record over MaxRecordSize should be ErrRecordTooLarge, got %v", err)
	}
	if got := replay(t, l); len(got) != 0 {
		t.Errorf("a rejected record must not be written")
	}
}

func TestL3_1_Closed(t *testing.T) {
	l, err := wal.Open(logPath(t))
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
	if err := l.Append([]byte("x")); !errors.Is(err, wal.ErrClosed) {
		t.Errorf("Append after Close should be wal.ErrClosed, got %v", err)
	}
	if err := l.Replay(func([]byte) error { return nil }); !errors.Is(err, wal.ErrClosed) {
		t.Errorf("Replay after Close should be wal.ErrClosed, got %v", err)
	}
	if err := l.Close(); err != nil {
		t.Errorf("closing twice should return nil, got %v", err)
	}
}

func TestL3_1_OpenErrors(t *testing.T) {
	if _, err := wal.Open(filepath.Join(t.TempDir(), "no", "such", "dir", "wal.log")); err == nil {
		t.Errorf("opening a log in a directory that doesn't exist should fail")
	}
	dir := t.TempDir()
	if _, err := wal.Open(dir); err == nil {
		t.Errorf("opening a directory as a log file should fail")
	}
}

func TestL3_1_ConcurrentAppends(t *testing.T) {
	l := open(t, logPath(t))
	const workers, each = 20, 50
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range each {
				body := bytes.Repeat([]byte{byte(w)}, 100+i)
				sum := sha256.Sum256(body)
				rec := append(sum[:], body...)
				if err := l.Append(rec); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	got := replay(t, l)
	if len(got) != workers*each {
		t.Fatalf("%d concurrent appends produced %d records", workers*each, len(got))
	}
	for i, rec := range got {
		if len(rec) < 32 {
			t.Fatalf("record %d is only %d bytes: appends interleaved", i, len(rec))
		}
		if sum := sha256.Sum256(rec[32:]); !bytes.Equal(sum[:], rec[:32]) {
			t.Fatalf("record %d is corrupt: two appends were interleaved in the file. Serialise writes.", i)
		}
	}
}

func TestL3_1_FileIsOnDisk(t *testing.T) {
	path := logPath(t)
	l := open(t, path)
	appendAll(t, l, []byte("hello"))
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() < 5 {
		t.Errorf("after Append returns, the record must already be in the file (size is %d)", info.Size())
	}
}
