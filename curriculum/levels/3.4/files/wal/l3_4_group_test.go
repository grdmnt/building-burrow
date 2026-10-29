package wal_test

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"burrow/internal/testkit"
	"burrow/wal"
)

// slowSync makes every fsync cost 2ms, like a real disk, and records how big
// the file was at each sync.
type slowSync struct {
	calls  atomic.Int64
	synced atomic.Int64
	fail   func(n int64) error
}

func (s *slowSync) sync(f *os.File) error {
	n := s.calls.Add(1)
	if s.fail != nil {
		if err := s.fail(n); err != nil {
			return err
		}
	}
	time.Sleep(2 * time.Millisecond)
	if err := f.Sync(); err != nil {
		return err
	}
	if info, err := f.Stat(); err == nil {
		s.synced.Store(info.Size())
	}
	return nil
}

func TestL3_4_EachAppendIsSyncedBeforeReturning(t *testing.T) {
	ss := &slowSync{}
	path := logPath(t)
	l := open(t, path, wal.WithSyncFunc(ss.sync))
	for i := range 30 {
		if err := l.Append([]byte(fmt.Sprintf("rec %d", i))); err != nil {
			t.Fatal(err)
		}
		info, _ := os.Stat(path)
		if ss.synced.Load() < info.Size() {
			t.Fatalf("Append %d returned before its record was synced (synced %d of %d bytes). "+
				"Batching must never skip the wait.", i, ss.synced.Load(), info.Size())
		}
	}
	if n := l.Syncs(); n != 30 {
		t.Errorf("30 appends one after another need 30 syncs (nothing to batch), Syncs() = %d", n)
	}
}

func TestL3_4_ConcurrentAppendsShareSyncs(t *testing.T) {
	ss := &slowSync{}
	l := open(t, logPath(t), wal.WithSyncFunc(ss.sync))
	const workers, each = 100, 20
	start := time.Now()
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range each {
				if err := l.Append([]byte(fmt.Sprintf("%d-%d", w, i))); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	took := time.Since(start)
	if got := len(replay(t, l)); got != workers*each {
		t.Fatalf("%d appends, %d records", workers*each, got)
	}
	syncs := l.Syncs()
	t.Logf("%d appends, %d syncs, %v", workers*each, syncs, took)
	if syncs > 500 {
		t.Errorf("%d concurrent appends used %d syncs. Group commit should let one sync cover many waiting appends.", workers*each, syncs)
	}
	if took > 2*time.Second {
		t.Errorf("2000 appends with a 2ms sync took %v; one sync per append would take 4s or more", took)
	}
	if int64(syncs) != ss.calls.Load() {
		t.Errorf("Syncs() = %d but the sync function ran %d times", syncs, ss.calls.Load())
	}
}

func TestL3_4_SyncFailureIsSticky(t *testing.T) {
	errDisk := errors.New("EIO: disk said no")
	ss := &slowSync{fail: func(n int64) error {
		if n >= 5 {
			return errDisk
		}
		return nil
	}}
	l := open(t, logPath(t), wal.WithSyncFunc(ss.sync))
	var failed int
	for i := range 10 {
		err := l.Append([]byte(fmt.Sprintf("r%d", i)))
		if i < 4 && err != nil {
			t.Fatalf("append %d failed early: %v", i, err)
		}
		if i >= 4 {
			if !errors.Is(err, errDisk) {
				t.Errorf("append %d: after a failed sync every append must fail with the sync error "+
					"(you can't know what reached the disk), got %v", i, err)
			}
			failed++
		}
	}
	if failed != 6 {
		t.Errorf("expected 6 failed appends, got %d", failed)
	}
}

func TestL3_4_FailedBatchFailsEveryone(t *testing.T) {
	errDisk := errors.New("EIO")
	var gate atomic.Bool
	ss := &slowSync{fail: func(int64) error {
		if gate.Load() {
			return errDisk
		}
		return nil
	}}
	l := open(t, logPath(t), wal.WithSyncFunc(ss.sync))
	gate.Store(true)
	var wg sync.WaitGroup
	var ok atomic.Int64
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if l.Append([]byte("x")) == nil {
				ok.Add(1)
			}
		}()
	}
	wg.Wait()
	if n := ok.Load(); n != 0 {
		t.Errorf("every sync failed, yet %d appends reported success", n)
	}
}

func TestL3_4_CloseFlushesAndStops(t *testing.T) {
	testkit.NoLeaks(t)
	ss := &slowSync{}
	path := logPath(t)
	l, err := wal.Open(path, wal.WithSyncFunc(ss.sync))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var acked sync.Map
	for w := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 30 {
				rec := fmt.Sprintf("%d-%d", w, i)
				err := l.Append([]byte(rec))
				if err == nil {
					acked.Store(rec, true)
				} else if !errors.Is(err, wal.ErrClosed) {
					t.Errorf("an Append racing with Close should succeed or return ErrClosed, got %v", err)
				}
			}
		}()
	}
	time.Sleep(20 * time.Millisecond)
	testkit.Within(t, 3*time.Second, "Close with appends in flight", func() { l.Close() })
	testkit.Within(t, 3*time.Second, "appends after Close", wg.Wait)

	l2 := open(t, path)
	have := map[string]bool{}
	for _, r := range replay(t, l2) {
		have[string(r)] = true
	}
	acked.Range(func(k, _ any) bool {
		if !have[k.(string)] {
			t.Errorf("record %q was acknowledged but isn't in the log after Close", k)
		}
		return true
	})
}

func TestL3_4_DefaultSync(t *testing.T) {
	l := open(t, logPath(t))
	appendAll(t, l, []byte("a"), []byte("b"))
	if l.Syncs() < 1 {
		t.Errorf("without WithSyncFunc the log still syncs (File.Sync); Syncs() = %d", l.Syncs())
	}
}

func BenchmarkL3_4_AppendSerial(b *testing.B) {
	l, _ := wal.Open(b.TempDir() + "/wal.log")
	defer l.Close()
	rec := make([]byte, 128)
	for b.Loop() {
		l.Append(rec)
	}
}

func BenchmarkL3_4_AppendParallel(b *testing.B) {
	l, _ := wal.Open(b.TempDir() + "/wal.log")
	defer l.Close()
	rec := make([]byte, 128)
	b.SetParallelism(16)
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			l.Append(rec)
		}
	})
}
