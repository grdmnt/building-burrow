package wal_test

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"burrow/internal/testkit"
	"burrow/wal"
)

// Your own benchmark in wal/ is graded here: it must exist and run cleanly.
// What it measures is up to you; run it before and after group commit with
// -benchmem and -cpuprofile and compare.
func TestL4_5_YourBenchmark(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped in -short mode")
	}
	names := testkit.YourBenchmarks(t, "wal")
	if len(names) == 0 {
		t.Fatal("no benchmark of your own in wal/. Write one, such as BenchmarkAppend, using for b.Loop() { ... }")
	}
	for _, name := range names {
		out := testkit.RunBenchmark(t, "wal", name, 10)
		if !strings.Contains(out, name) {
			t.Errorf("%s didn't report a result:\n%s", name, out)
		}
	}
}

// Syncs is read by other goroutines (stats, tests) while the writer goroutine
// updates it. Under -race this fails unless the counter is safe to share.
func TestL4_5_SyncsConcurrentRead(t *testing.T) {
	testkit.RequireRace(t)
	l, err := wal.Open(filepath.Join(t.TempDir(), "wal.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	stop := make(chan struct{})
	var readers sync.WaitGroup
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			var last uint64
			for {
				select {
				case <-stop:
					return
				default:
				}
				n := l.Syncs()
				if n < last {
					t.Errorf("Syncs went backwards: %d after %d", n, last)
					return
				}
				last = n
			}
		}()
	}
	var writers sync.WaitGroup
	for range 8 {
		writers.Add(1)
		go func() {
			defer writers.Done()
			for range 20 {
				if err := l.Append([]byte("x")); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	writers.Wait()
	close(stop)
	readers.Wait()
	if l.Syncs() == 0 {
		t.Errorf("Syncs is 0 after 160 appends")
	}
}
