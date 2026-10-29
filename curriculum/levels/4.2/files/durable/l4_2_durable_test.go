package durable_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"burrow/durable"
	"burrow/internal/testkit"
	"burrow/store"
	"burrow/store/storetest"
)

var bg = context.Background()

var _ store.Store = (*durable.Store)(nil)

func openDir(t *testing.T, dir string, opts ...store.Option) *durable.Store {
	t.Helper()
	s, err := durable.Open(dir, opts...)
	if err != nil {
		t.Fatalf("durable.Open(%s): %v", dir, err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

type kv struct {
	value   string
	version uint64
}

func stateOf(t *testing.T, s *durable.Store) map[string]kv {
	t.Helper()
	keys, err := s.Keys(bg, "")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]kv{}
	for _, k := range keys {
		e, err := s.Get(bg, k)
		if err != nil {
			t.Fatalf("Get(%s): %v", k, err)
		}
		out[k] = kv{string(e.Value), e.Version}
	}
	return out
}

func sameState(t *testing.T, got, want map[string]kv) {
	t.Helper()
	for k, v := range want {
		if g, ok := got[k]; !ok {
			t.Errorf("key %q was lost", k)
		} else if g != v {
			t.Errorf("key %q = %q version %d, want %q version %d", k, g.value, g.version, v.value, v.version)
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("key %q came back but shouldn't exist", k)
		}
	}
}

func TestL4_2_Conformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T) storetest.Store { return openDir(t, t.TempDir()) })
}

func TestL4_2_ConcurrentConformance(t *testing.T) {
	storetest.RunConcurrent(t, func(t *testing.T) storetest.Store { return openDir(t, t.TempDir()) })
}

func TestL4_2_CASConformance(t *testing.T) {
	storetest.RunCAS(t, func(t *testing.T) storetest.CASStore { return openDir(t, t.TempDir()) })
}

func TestL4_2_TTLConformance(t *testing.T) {
	storetest.RunTTL(t, func(t *testing.T) storetest.TTLStore { return openDir(t, t.TempDir()) })
}

func TestL4_2_RestartRestoresState(t *testing.T) {
	dir := t.TempDir()
	s, err := durable.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]kv{}
	for i := range 200 {
		k := fmt.Sprintf("k%03d", i%50)
		e, err := s.Set(bg, k, []byte(fmt.Sprintf("v%d", i)))
		if err != nil {
			t.Fatal(err)
		}
		want[k] = kv{fmt.Sprintf("v%d", i), e.Version}
	}
	for i := 0; i < 50; i += 3 {
		k := fmt.Sprintf("k%03d", i)
		if err := s.Delete(bg, k); err != nil {
			t.Fatal(err)
		}
		delete(want, k)
	}
	s.Set(bg, "empty", nil)
	want["empty"] = kv{"", 1}
	s.Set(bg, "bin", []byte{0, '\n', 0xff})
	want["bin"] = kv{"\x00\n\xff", 1}
	s.Delete(bg, "k003")
	s.Set(bg, "k003", []byte("back"))
	want["k003"] = kv{"back", 1}
	e, _ := s.CompareAndSwap(bg, "k001", want["k001"].version, []byte("swapped"))
	want["k001"] = kv{"swapped", e.Version}
	s.CompareAndDelete(bg, "k002", want["k002"].version)
	delete(want, "k002")
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	s2 := openDir(t, dir)
	sameState(t, stateOf(t, s2), want)
	e, _ = s2.Set(bg, "k001", []byte("after restart"))
	if e.Version != want["k001"].version+1 {
		t.Errorf("versions continue after a restart: Set gave version %d, want %d", e.Version, want["k001"].version+1)
	}
}

func TestL4_2_CounterSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	s, _ := durable.Open(dir)
	for i := range 1000 {
		var ver uint64
		if e, err := s.Get(bg, "counter"); err == nil {
			ver = e.Version
		}
		if _, err := s.CompareAndSwap(bg, "counter", ver, []byte(strconv.Itoa(i+1))); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	e, err := openDir(t, dir).Get(bg, "counter")
	if err != nil || string(e.Value) != "1000" || e.Version != 1000 {
		t.Errorf("after restart the counter reads (%q, version %d, %v), want (1000, 1000)", e.Value, e.Version, err)
	}
}

func TestL4_2_ExpiryIsAbsolute(t *testing.T) {
	dir := t.TempDir()
	clock := testkit.NewFakeClock()
	s, err := durable.Open(dir, store.WithClock(clock.Now), store.WithSweepInterval(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	set, _ := s.SetWithTTL(bg, "session", []byte("v"), 10*time.Second)
	s.SetWithTTL(bg, "short", []byte("v"), time.Second)
	s.Set(bg, "touched", []byte("v"))
	s.Touch(bg, "touched", 20*time.Second)
	s.Close()

	clock.Advance(3 * time.Second)
	var evicted atomic.Int64
	s2 := openDir(t, dir, store.WithClock(clock.Now), store.WithSweepInterval(time.Hour),
		store.WithOnEvict(func(store.Entry) { evicted.Add(1) }))
	e, err := s2.Get(bg, "session")
	if err != nil {
		t.Fatalf("a key with 7s left must survive the restart: %v", err)
	}
	if !e.ExpiresAt.Equal(set.ExpiresAt) {
		t.Errorf("ExpiresAt must be stored as an absolute instant: %v before restart, %v after", set.ExpiresAt, e.ExpiresAt)
	}
	if _, err := s2.Get(bg, "short"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a key that expired while the store was down must stay gone, got %v", err)
	}
	if evicted.Load() != 0 {
		t.Errorf("entries already expired at replay are dropped silently; OnEvict ran %d times", evicted.Load())
	}
	if e, _ := s2.Get(bg, "touched"); e.ExpiresAt.IsZero() {
		t.Errorf("a Touch must be logged too: the expiry it set was lost on restart")
	}
	clock.Advance(7 * time.Second)
	if _, err := s2.Get(bg, "session"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the key should expire at its original instant, got %v", err)
	}
}

func TestL4_2_RestartWithoutClose(t *testing.T) {
	dir := t.TempDir()
	s, err := durable.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.Set(bg, "a", []byte("1"))
	s.Set(bg, "b", []byte("2"))
	s.Delete(bg, "a")
	// No Close: as far as the files know, the process died here.
	sameState(t, stateOf(t, openDir(t, dir)), map[string]kv{"b": {"2", 1}})
}

func dirSize(dir string) int64 {
	var n int64
	filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			n += info.Size()
		}
		return nil
	})
	return n
}

func TestL4_2_OnlyRealChangesAreLogged(t *testing.T) {
	dir := t.TempDir()
	s := openDir(t, dir)
	s.Set(bg, "k", []byte("v"))
	before := dirSize(dir)
	s.Set(bg, "bad key", []byte("v"))
	s.Delete(bg, "missing")
	s.CompareAndSwap(bg, "k", 9, []byte("x"))
	s.CompareAndDelete(bg, "k", 9)
	s.Touch(bg, "missing", time.Second)
	s.SetWithTTL(bg, "k", []byte("x"), 0)
	s.Get(bg, "k")
	s.Keys(bg, "")
	ctx, cancel := context.WithCancel(bg)
	cancel()
	s.Set(ctx, "k2", []byte("v"))
	if after := dirSize(dir); after != before {
		t.Errorf("rejected writes and reads must not append to the log (it grew from %d to %d bytes). Decide first, then log.", before, after)
	}
}

func TestL4_2_OpenAndClose(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "data")
	s := openDir(t, dir)
	s.Set(bg, "k", []byte("v"))
	if _, err := os.Stat(filepath.Join(dir, "wal.log")); err != nil {
		t.Errorf("the log should live at <dir>/wal.log: %v", err)
	}
	if _, err := durable.Open("/dev/null/not-a-dir"); err == nil {
		t.Errorf("Open on a path that can't be a directory should fail")
	}
	s2, _ := durable.Open(t.TempDir())
	s2.Close()
	if err := s2.Close(); err != nil {
		t.Errorf("a second Close should return nil, got %v", err)
	}
	if _, err := s2.Set(bg, "k", []byte("v")); !errors.Is(err, store.ErrClosed) {
		t.Errorf("Set after Close should be store.ErrClosed, got %v", err)
	}
	if _, err := s2.Get(bg, "k"); !errors.Is(err, store.ErrClosed) {
		t.Errorf("Get after Close should be store.ErrClosed, got %v", err)
	}
}

// TestL4_2_KillNine writes through durable.Store in a child process, kills it
// with SIGKILL mid-stream, and checks every acknowledged write survived.
func TestL4_2_KillNine(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a process")
	}
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestL4_2_zHelperWriter$")
	cmd.Env = append(os.Environ(), "BURROW_HELPER_DIR="+dir)
	out, _ := cmd.StdoutPipe()
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	last := -1
	sc := bufio.NewScanner(out)
	deadline := time.Now().Add(20 * time.Second)
	for last < 300 && sc.Scan() && time.Now().Before(deadline) {
		if n, ok := strings.CutPrefix(sc.Text(), "acked "); ok {
			last, _ = strconv.Atoi(n)
		}
	}
	cmd.Process.Kill() // SIGKILL: no deferred code, no Close
	cmd.Wait()
	s := openDir(t, dir)
	for i := 0; i <= last; i++ {
		k := fmt.Sprintf("crash-%05d", i)
		e, err := s.Get(bg, k)
		if err != nil || string(e.Value) != strconv.Itoa(i*i) {
			t.Fatalf("write %d was acknowledged before the crash but came back as (%q, %v). "+
				"Only acknowledge after the record is in the log.", i, e.Value, err)
		}
	}
}

func TestL4_2_zHelperWriter(t *testing.T) {
	dir := os.Getenv("BURROW_HELPER_DIR")
	if dir == "" {
		t.Skip("helper process for TestL4_2_KillNine")
	}
	s, err := durable.Open(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "helper open:", err)
		os.Exit(3)
	}
	for i := 0; ; i++ {
		if _, err := s.Set(bg, fmt.Sprintf("crash-%05d", i), []byte(strconv.Itoa(i*i))); err != nil {
			fmt.Fprintln(os.Stderr, "helper set:", err)
			os.Exit(3)
		}
		fmt.Printf("acked %d\n", i)
	}
}

func TestL4_2_ExpiriesAreNotLogged(t *testing.T) {
	dir := t.TempDir()
	clock := testkit.NewFakeClock()
	var evicted atomic.Int64
	s := openDir(t, dir, store.WithClock(clock.Now), store.WithSweepInterval(time.Millisecond),
		store.WithOnEvict(func(store.Entry) { evicted.Add(1) }))
	for i := range 20 {
		s.SetWithTTL(bg, fmt.Sprintf("t%d", i), []byte("v"), time.Second)
	}
	if n := s.LogLen(); n != 20 {
		t.Fatalf("LogLen after 20 writes = %d", n)
	}
	before := dirSize(dir)
	clock.Advance(time.Minute)
	testkit.Eventually(t, 3*time.Second, func() bool { return evicted.Load() == 20 }, "the janitor to evict 20 keys (your WithOnEvict passed through durable.Open)")
	if after := dirSize(dir); after != before || s.LogLen() != 20 {
		t.Errorf("evictions must not be logged (log grew from %d to %d bytes, LogLen %d). Each put record carries its absolute expiry; replay drops what's expired.", before, after, s.LogLen())
	}
}

func TestL4_2_CASWithTTLSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	s, _ := durable.Open(dir)
	set, err := s.CompareAndSwapWithTTL(bg, "lock", 0, []byte("me"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	e, err := openDir(t, dir).Get(bg, "lock")
	if err != nil || e.Version != 1 || !e.ExpiresAt.Equal(set.ExpiresAt) {
		t.Errorf("a lock taken with CompareAndSwapWithTTL must keep its value, version and expiry across a restart: (%+v, %v)", e, err)
	}
}

func TestL4_2_LogLenFreshDir(t *testing.T) {
	if n := openDir(t, t.TempDir()).LogLen(); n != 0 {
		t.Errorf("LogLen on a fresh directory = %d, want 0", n)
	}
}
