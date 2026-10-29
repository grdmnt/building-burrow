package durable_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"burrow/durable"
	"burrow/store"
)

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Size()
}

func TestL3_3_SnapshotShrinksLog(t *testing.T) {
	dir := t.TempDir()
	s, err := durable.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	for i := range 10_000 {
		k := fmt.Sprintf("k%02d", i%100)
		v := strconv.Itoa(i)
		s.Set(bg, k, []byte(v))
		want[k] = v
	}
	logPath := filepath.Join(dir, "wal.log")
	before := fileSize(t, logPath)
	if err := s.Snapshot(); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if after := fileSize(t, logPath); after > 512 {
		t.Errorf("after a snapshot the log should be (nearly) empty: %d bytes before, %d after", before, after)
	}
	if _, err := os.Stat(filepath.Join(dir, "snapshot.db")); err != nil {
		t.Errorf("the snapshot should be at <dir>/snapshot.db: %v", err)
	}
	sameState(t, snapshotOf(t, s), want)
	s.Close()
	sameState(t, snapshotOf(t, openDir(t, dir)), want)
}

func TestL3_3_WritesAfterSnapshot(t *testing.T) {
	dir := t.TempDir()
	s, _ := durable.Open(dir)
	s.Set(bg, "a", []byte("1"))
	s.Set(bg, "b", []byte("2"))
	s.Set(bg, "gone", []byte("x"))
	s.Delete(bg, "gone")
	if err := s.Snapshot(); err != nil {
		t.Fatal(err)
	}
	s.Set(bg, "a", []byte("1b"))
	s.Delete(bg, "b")
	s.Set(bg, "c", []byte("3"))
	s.Close()
	sameState(t, snapshotOf(t, openDir(t, dir)), map[string]string{"a": "1b", "c": "3"})
}

func TestL3_3_RepeatedSnapshots(t *testing.T) {
	dir := t.TempDir()
	s, _ := durable.Open(dir)
	want := map[string]string{}
	for round := range 5 {
		for i := range 20 {
			k := fmt.Sprintf("r%d-%d", round, i)
			s.Set(bg, k, []byte(k))
			want[k] = k
		}
		if err := s.Snapshot(); err != nil {
			t.Fatalf("snapshot %d: %v", round, err)
		}
	}
	if err := s.Snapshot(); err != nil {
		t.Fatalf("a snapshot with nothing new to write should still work: %v", err)
	}
	s.Close()
	sameState(t, snapshotOf(t, openDir(t, dir)), want)
}

func TestL3_3_EmptySnapshot(t *testing.T) {
	dir := t.TempDir()
	s, _ := durable.Open(dir)
	if err := s.Snapshot(); err != nil {
		t.Fatalf("snapshot of an empty store: %v", err)
	}
	s.Close()
	sameState(t, snapshotOf(t, openDir(t, dir)), map[string]string{})
}

func TestL3_3_LeftoverTempFile(t *testing.T) {
	dir := t.TempDir()
	s, _ := durable.Open(dir)
	s.Set(bg, "k", []byte("v"))
	s.Snapshot()
	s.Set(bg, "k2", []byte("v2"))
	s.Close()
	tmp := filepath.Join(dir, "snapshot.tmp")
	os.WriteFile(tmp, []byte("half-written garbage from a crash"), 0o644)

	sameState(t, snapshotOf(t, openDir(t, dir)), map[string]string{"k": "v", "k2": "v2"})
	if _, err := os.Stat(tmp); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Open should ignore and remove a leftover snapshot.tmp")
	}
}

// Crash after the new snapshot was renamed into place but before the log was
// reset: the old log is still there, full of records the snapshot already has.
func TestL3_3_CrashBeforeLogReset(t *testing.T) {
	dir := t.TempDir()
	s, _ := durable.Open(dir)
	s.Set(bg, "a", []byte("1"))
	s.Set(bg, "b", []byte("2"))
	s.Set(bg, "a", []byte("3"))
	s.Delete(bg, "b")
	s.Set(bg, "c", []byte("4"))
	oldLog, _ := os.ReadFile(filepath.Join(dir, "wal.log"))
	if err := s.Snapshot(); err != nil {
		t.Fatal(err)
	}
	s.Close()
	os.WriteFile(filepath.Join(dir, "wal.log"), oldLog, 0o644)

	sameState(t, snapshotOf(t, openDir(t, dir)), map[string]string{"a": "3", "c": "4"})
}

func TestL3_3_CorruptSnapshotIsAnError(t *testing.T) {
	dir := t.TempDir()
	s, _ := durable.Open(dir)
	for i := range 50 {
		s.Set(bg, fmt.Sprintf("k%d", i), []byte("value"))
	}
	s.Snapshot()
	s.Close()
	path := filepath.Join(dir, "snapshot.db")
	data, _ := os.ReadFile(path)
	data[len(data)/2] ^= 0xff
	os.WriteFile(path, data, 0o644)
	s2, err := durable.Open(dir)
	if err == nil {
		keys, _ := s2.Keys(bg, "")
		s2.Close()
		if len(keys) != 50 {
			t.Fatalf("a corrupt snapshot opened silently with %d of 50 keys. Fail loudly instead: checksum the snapshot.", len(keys))
		}
	}
}

func TestL3_3_SnapshotWhileWriting(t *testing.T) {
	dir := t.TempDir()
	s, _ := durable.Open(dir)
	const writers = 8
	last := make([]int, writers)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for w := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				if err := s.Set(bg, fmt.Sprintf("w%d", w), []byte(strconv.Itoa(i))); err != nil {
					t.Error(err)
					return
				}
				last[w] = i
			}
		}()
	}
	for range 10 {
		if err := s.Snapshot(); err != nil {
			t.Fatalf("Snapshot during writes: %v", err)
		}
	}
	close(stop)
	wg.Wait()
	s.Close()
	want := map[string]string{}
	for w := range writers {
		want[fmt.Sprintf("w%d", w)] = strconv.Itoa(last[w])
	}
	sameState(t, snapshotOf(t, openDir(t, dir)), want)
}

func TestL3_3_SnapshotAfterClose(t *testing.T) {
	s, _ := durable.Open(t.TempDir())
	s.Close()
	if err := s.Snapshot(); !errors.Is(err, store.ErrClosed) {
		t.Errorf("Snapshot after Close should be store.ErrClosed, got %v", err)
	}
}
