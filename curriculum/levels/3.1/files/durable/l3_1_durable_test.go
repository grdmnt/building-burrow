package durable_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"burrow/durable"
	"burrow/store"
	"burrow/store/storetest"
)

var bg = context.Background()

func openDir(t *testing.T, dir string) *durable.Store {
	t.Helper()
	s, err := durable.Open(dir)
	if err != nil {
		t.Fatalf("durable.Open(%s): %v", dir, err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func snapshotOf(t *testing.T, s *durable.Store) map[string]string {
	t.Helper()
	keys, err := s.Keys(bg, "")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, k := range keys {
		v, err := s.Get(bg, k)
		if err != nil {
			t.Fatalf("Get(%s): %v", k, err)
		}
		out[k] = string(v)
	}
	return out
}

func sameState(t *testing.T, got, want map[string]string) {
	t.Helper()
	for k, v := range want {
		if g, ok := got[k]; !ok {
			t.Errorf("key %q was lost", k)
		} else if g != v {
			t.Errorf("key %q = %q, want %q", k, g, v)
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("key %q came back but shouldn't exist", k)
		}
	}
}

var _ store.Store = (*durable.Store)(nil)

func TestL3_1_DurableConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T) storetest.Store { return openDir(t, t.TempDir()) })
}

func TestL3_1_DurableConcurrentConformance(t *testing.T) {
	storetest.RunConcurrent(t, func(t *testing.T) storetest.Store { return openDir(t, t.TempDir()) })
}

func TestL3_1_RestartRestoresState(t *testing.T) {
	dir := t.TempDir()
	s, err := durable.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	for i := range 200 {
		k := fmt.Sprintf("k%03d", i%50)
		v := fmt.Sprintf("v%d", i)
		if err := s.Set(bg, k, []byte(v)); err != nil {
			t.Fatal(err)
		}
		want[k] = v
	}
	for i := 0; i < 50; i += 3 {
		k := fmt.Sprintf("k%03d", i)
		if err := s.Delete(bg, k); err != nil {
			t.Fatal(err)
		}
		delete(want, k)
	}
	s.Set(bg, "empty", nil)
	want["empty"] = ""
	s.Set(bg, "bin", []byte{0, '\n', 0xff})
	want["bin"] = "\x00\n\xff"
	s.Delete(bg, "k003")
	s.Set(bg, "k003", []byte("back"))
	want["k003"] = "back"
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	sameState(t, snapshotOf(t, openDir(t, dir)), want)
}

func TestL3_1_RestartWithoutClose(t *testing.T) {
	dir := t.TempDir()
	s, err := durable.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.Set(bg, "a", []byte("1"))
	s.Set(bg, "b", []byte("2"))
	s.Delete(bg, "a")
	// No Close: as far as the files know, the process died here.
	sameState(t, snapshotOf(t, openDir(t, dir)), map[string]string{"b": "2"})
}

func TestL3_1_OpenCreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "data")
	s := openDir(t, dir)
	if err := s.Set(bg, "k", []byte("v")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "wal.log")); err != nil {
		t.Errorf("the log should live at <dir>/wal.log: %v", err)
	}
}

func dirSize(t *testing.T, dir string) int64 {
	var n int64
	filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			n += info.Size()
		}
		return nil
	})
	return n
}

func TestL3_1_OnlyRealChangesAreLogged(t *testing.T) {
	dir := t.TempDir()
	s := openDir(t, dir)
	s.Set(bg, "k", []byte("v"))
	before := dirSize(t, dir)
	s.Set(bg, "bad key", []byte("v"))
	s.Delete(bg, "missing")
	s.Get(bg, "k")
	s.Keys(bg, "")
	ctx, cancel := context.WithCancel(bg)
	cancel()
	s.Set(ctx, "k2", []byte("v"))
	if after := dirSize(t, dir); after != before {
		t.Errorf("rejected writes, reads and deletes of missing keys must not append to the log "+
			"(it grew from %d to %d bytes). Validate first, then log.", before, after)
	}
}

func TestL3_1_Closed(t *testing.T) {
	s, err := durable.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if err := s.Set(bg, "k", []byte("v")); !errors.Is(err, store.ErrClosed) {
		t.Errorf("Set after Close should be store.ErrClosed, got %v", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("a second Close should return nil, got %v", err)
	}
}

// TestL3_1_KillNine runs a writer in a child process, kills it with SIGKILL
// mid-stream, and checks that every write the child saw acknowledged survived.
func TestL3_1_KillNine(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a process")
	}
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestL3_1_zHelperWriter$")
	cmd.Env = append(os.Environ(), "BURROW_HELPER_DIR="+dir)
	out, _ := cmd.StdoutPipe()
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	lastAcked := -1
	sc := bufio.NewScanner(out)
	deadline := time.After(20 * time.Second)
	for lastAcked < 300 {
		lines := make(chan string, 1)
		go func() {
			if sc.Scan() {
				lines <- sc.Text()
			} else {
				close(lines)
			}
		}()
		select {
		case line, ok := <-lines:
			if !ok {
				cmd.Process.Kill()
				t.Fatalf("the helper stopped early after ack %d", lastAcked)
			}
			if n, ok := strings.CutPrefix(line, "acked "); ok {
				lastAcked, _ = strconv.Atoi(n)
			}
		case <-deadline:
			cmd.Process.Kill()
			t.Fatalf("helper too slow: only %d acks in 20s", lastAcked+1)
		}
	}
	cmd.Process.Kill() // SIGKILL: no deferred code, no Close
	cmd.Wait()

	s := openDir(t, dir)
	for i := 0; i <= lastAcked; i++ {
		k := fmt.Sprintf("crash-%05d", i)
		v, err := s.Get(bg, k)
		if err != nil || string(v) != strconv.Itoa(i*i) {
			t.Fatalf("write %d was acknowledged before the crash but came back as (%q, %v). "+
				"Only reply after the record is in the log.", i, v, err)
		}
	}
	keys, _ := s.Keys(bg, "crash-")
	if !slices.IsSorted(keys) || len(keys) < lastAcked+1 {
		t.Errorf("found %d keys after the crash, want at least %d", len(keys), lastAcked+1)
	}
}

func TestL3_1_zHelperWriter(t *testing.T) {
	dir := os.Getenv("BURROW_HELPER_DIR")
	if dir == "" {
		t.Skip("helper process for TestL3_1_KillNine")
	}
	s, err := durable.Open(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "helper open:", err)
		os.Exit(3)
	}
	for i := 0; ; i++ {
		if err := s.Set(bg, fmt.Sprintf("crash-%05d", i), []byte(strconv.Itoa(i*i))); err != nil {
			fmt.Fprintln(os.Stderr, "helper set:", err)
			os.Exit(3)
		}
		fmt.Printf("acked %d\n", i)
	}
}
