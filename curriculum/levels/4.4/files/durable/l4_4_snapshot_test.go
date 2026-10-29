package durable_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

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

func TestL4_4_SnapshotShrinksLog(t *testing.T) {
	dir := t.TempDir()
	s, err := durable.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]kv{}
	for i := range 10_000 {
		k := fmt.Sprintf("k%02d", i%100)
		v := strconv.Itoa(i)
		e, _ := s.Set(bg, k, []byte(v))
		want[k] = kv{v, e.Version}
	}
	logPath := filepath.Join(dir, "wal.log")
	before := fileSize(t, logPath)
	if err := s.Snapshot(); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if after := fileSize(t, logPath); after > 64 {
		t.Errorf("after a snapshot the log should be (nearly) empty: %d bytes before, %d after", before, after)
	}
	if _, err := os.Stat(filepath.Join(dir, "snapshot.db")); err != nil {
		t.Errorf("the snapshot should be at <dir>/snapshot.db: %v", err)
	}
	sameState(t, stateOf(t, s), want)
	s.Close()
	sameState(t, stateOf(t, openDir(t, dir)), want)
}

func TestL4_4_WritesAfterSnapshot(t *testing.T) {
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
	sameState(t, stateOf(t, openDir(t, dir)), map[string]kv{"a": {"1b", 2}, "c": {"3", 1}})
}

func TestL4_4_RepeatedSnapshots(t *testing.T) {
	dir := t.TempDir()
	s, _ := durable.Open(dir)
	want := map[string]kv{}
	for round := range 5 {
		for i := range 20 {
			k := fmt.Sprintf("r%d-%d", round, i)
			s.Set(bg, k, []byte(k))
			want[k] = kv{k, 1}
		}
		if err := s.Snapshot(); err != nil {
			t.Fatalf("snapshot %d: %v", round, err)
		}
	}
	if err := s.Snapshot(); err != nil {
		t.Fatalf("a snapshot with nothing new to write should still work: %v", err)
	}
	s.Close()
	sameState(t, stateOf(t, openDir(t, dir)), want)
}

func TestL4_4_EmptySnapshot(t *testing.T) {
	dir := t.TempDir()
	s, _ := durable.Open(dir)
	if err := s.Snapshot(); err != nil {
		t.Fatalf("snapshot of an empty store: %v", err)
	}
	s.Close()
	sameState(t, stateOf(t, openDir(t, dir)), map[string]kv{})
}

func TestL4_4_LeftoverTempFile(t *testing.T) {
	dir := t.TempDir()
	s, _ := durable.Open(dir)
	s.Set(bg, "k", []byte("v"))
	s.Snapshot()
	s.Set(bg, "k2", []byte("v2"))
	s.Close()
	tmp := filepath.Join(dir, "snapshot.tmp")
	os.WriteFile(tmp, []byte("half-written garbage from a crash"), 0o644)

	sameState(t, stateOf(t, openDir(t, dir)), map[string]kv{"k": {"v", 1}, "k2": {"v2", 1}})
	if _, err := os.Stat(tmp); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Open should ignore and remove a leftover snapshot.tmp")
	}
}

// Crash after the new snapshot was renamed into place but before the log was
// reset: the old log is still there, full of records the snapshot already has.
func TestL4_4_CrashBeforeLogReset(t *testing.T) {
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

	sameState(t, stateOf(t, openDir(t, dir)), map[string]kv{"a": {"3", 2}, "c": {"4", 1}})
}

func TestL4_4_CorruptSnapshotIsAnError(t *testing.T) {
	dir := t.TempDir()
	s, _ := durable.Open(dir)
	for i := range 50 {
		s.Set(bg, fmt.Sprintf("k%d", i), []byte("value"))
	}
	s.SetWithTTL(bg, "ttl", []byte("v"), time.Hour)
	s.Snapshot()
	s.Close()
	path := filepath.Join(dir, "snapshot.db")
	data, _ := os.ReadFile(path)
	// Damage a value's bytes, not the encoding's structure: the file still
	// decodes, so only a checksum can tell it was changed.
	i := bytes.LastIndex(data, []byte("value"))
	if i < 0 {
		t.Fatal("couldn't find a stored value in snapshot.db (is it encoded some other way?)")
	}
	data[i] ^= 0x01
	os.WriteFile(path, data, 0o644)
	s2, err := durable.Open(dir)
	if err == nil {
		s2.Close()
		t.Fatalf("Open accepted a snapshot.db with a flipped byte. Fail loudly instead of serving state you can't trust: checksum the snapshot.")
	}
}

func TestL4_4_SnapshotWhileWriting(t *testing.T) {
	dir := t.TempDir()
	s, _ := durable.Open(dir)
	const writers = 8
	last := make([]int, writers)
	lastVer := make([]uint64, writers)
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
				e, err := s.Set(bg, fmt.Sprintf("w%d", w), []byte(strconv.Itoa(i)))
				if err != nil {
					t.Error(err)
					return
				}
				last[w], lastVer[w] = i, e.Version
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
	want := map[string]kv{}
	for w := range writers {
		want[fmt.Sprintf("w%d", w)] = kv{strconv.Itoa(last[w]), lastVer[w]}
	}
	sameState(t, stateOf(t, openDir(t, dir)), want)
}

func TestL4_4_SnapshotAfterClose(t *testing.T) {
	s, _ := durable.Open(t.TempDir())
	s.Close()
	if err := s.Snapshot(); !errors.Is(err, store.ErrClosed) {
		t.Errorf("Snapshot after Close should be store.ErrClosed, got %v", err)
	}
}

func TestL4_4_SnapshotKeepsVersionsAndExpiry(t *testing.T) {
	dir := t.TempDir()
	s, _ := durable.Open(dir)
	s.Set(bg, "k", []byte("1"))
	s.Set(bg, "k", []byte("2"))
	set, _ := s.SetWithTTL(bg, "t", []byte("v"), time.Hour)
	if err := s.Snapshot(); err != nil {
		t.Fatal(err)
	}
	if s.LogLen() != 0 {
		t.Errorf("after Snapshot the log is empty: LogLen = %d", s.LogLen())
	}
	s.Close()
	s2 := openDir(t, dir)
	if e, _ := s2.Get(bg, "k"); e.Version != 2 {
		t.Errorf("the snapshot must keep versions: k has version %d, want 2", e.Version)
	}
	if e, _ := s2.Get(bg, "t"); !e.ExpiresAt.Equal(set.ExpiresAt) {
		t.Errorf("the snapshot must keep expiries: %v, want %v", e.ExpiresAt, set.ExpiresAt)
	}
}

func TestL4_4_MemorySnapshot(t *testing.T) {
	s := openDir(t, t.TempDir())
	s.Set(bg, "b", []byte("2"))
	s.Set(bg, "a", []byte("1"))
	entries := s.Memory.Snapshot()
	if len(entries) != 2 || entries[0].Key != "a" || entries[1].Key != "b" {
		t.Errorf("Memory.Snapshot returns every live entry sorted by key, got %+v", entries)
	}
	entries[0].Value[0] = 'X'
	if e, _ := s.Get(bg, "a"); string(e.Value) != "1" {
		t.Errorf("Memory.Snapshot must return copies")
	}
}
