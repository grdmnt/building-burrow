package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"burrow/client"
	"burrow/durable"
	"burrow/internal/testkit"
	"burrow/wal"
)

func dialProc(t *testing.T, p *proc) *client.Client {
	t.Helper()
	c, err := client.Dial(testkit.Ctx(t), p.addr)
	if err != nil {
		t.Fatalf("dialing %s: %v", p.addr, err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func stopProc(t *testing.T, p *proc, sig syscall.Signal) {
	t.Helper()
	p.cmd.Process.Signal(sig)
	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()
	select {
	case err := <-done:
		if sig == syscall.SIGTERM && err != nil {
			t.Fatalf("burrowd should exit 0 on SIGTERM, got %v\nstderr:\n%s", err, p.stderr)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("burrowd didn't exit within 10s of %v", sig)
	}
}

type want struct {
	value   string
	version uint64
	expires time.Time
}

func TestL5_1_SurvivesKillNine(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary")
	}
	bin := buildBurrowd(t)
	dir := t.TempDir()
	p := startBurrowd(t, bin, "-data", dir)
	c := dialProc(t, p)
	ctx := testkit.Ctx(t)
	wants := map[string]want{}
	for i := range 500 {
		k := fmt.Sprintf("k%03d", i)
		var w want
		switch i % 3 {
		case 0:
			e, err := c.Set(ctx, k, []byte(k))
			if err != nil {
				t.Fatal(err)
			}
			e, err = c.Set(ctx, k, []byte(k+"!"))
			if err != nil {
				t.Fatal(err)
			}
			w = want{k + "!", e.Version, time.Time{}}
		case 1:
			e, err := c.SetWithTTL(ctx, k, []byte(k), time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			w = want{k, e.Version, e.ExpiresAt}
		case 2:
			e, err := c.CompareAndSwap(ctx, k, 0, []byte(k))
			if err != nil {
				t.Fatal(err)
			}
			w = want{k, e.Version, time.Time{}}
		}
		wants[k] = w
	}
	stopProc(t, p, syscall.SIGKILL)

	p2 := startBurrowd(t, bin, "-data", dir)
	c2 := dialProc(t, p2)
	for k, w := range wants {
		e, err := c2.Get(ctx, k)
		if err != nil {
			t.Fatalf("%s was acknowledged before kill -9 but is gone: %v", k, err)
		}
		if string(e.Value) != w.value || e.Version != w.version {
			t.Fatalf("%s came back as %q version %d, want %q version %d", k, e.Value, e.Version, w.value, w.version)
		}
		if w.expires.IsZero() != e.ExpiresAt.IsZero() {
			t.Fatalf("%s: expiry lost or invented across the restart", k)
		}
		if !w.expires.IsZero() {
			if d := e.ExpiresAt.Sub(w.expires); d > 2*time.Second || d < -2*time.Second {
				t.Fatalf("%s: ExpiresAt moved by %v across a restart; it must be absolute", k, d)
			}
		}
	}
}

func TestL5_1_CleanShutdownSnapshots(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary")
	}
	bin := buildBurrowd(t)
	dir := t.TempDir()
	p := startBurrowd(t, bin, "-data", dir)
	c := dialProc(t, p)
	ctx := testkit.Ctx(t)
	for i := range 50 {
		c.Set(ctx, fmt.Sprintf("k%d", i), []byte("v"))
	}
	stopProc(t, p, syscall.SIGTERM)
	if _, err := os.Stat(filepath.Join(dir, "snapshot.db")); err != nil {
		t.Errorf("a clean shutdown should leave snapshot.db: %v", err)
	}
	if info, err := os.Stat(filepath.Join(dir, "wal.log")); err == nil && info.Size() > 64 {
		t.Errorf("after the shutdown snapshot the log should be nearly empty, it's %d bytes", info.Size())
	}
	p2 := startBurrowd(t, bin, "-data", dir)
	keys, err := dialProc(t, p2).Keys(ctx, "k")
	if err != nil || len(keys) != 50 {
		t.Errorf("after restart %d keys are served (%v), want 50", len(keys), err)
	}
}

func logRecords(t *testing.T, dir string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "wal.log"))
	if err != nil {
		return 0
	}
	cp := filepath.Join(t.TempDir(), "wal.log")
	os.WriteFile(cp, data, 0o644)
	l, err := wal.Open(cp)
	if err != nil {
		t.Fatalf("reading a copy of the live log: %v", err)
	}
	defer l.Close()
	return l.Len()
}

func TestL5_1_SnapshotEvery(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary")
	}
	bin := buildBurrowd(t)
	dir := t.TempDir()
	p := startBurrowd(t, bin, "-data", dir, "-snapshot-every", "100")
	c := dialProc(t, p)
	ctx := testkit.Ctx(t)
	for i := range 350 {
		if _, err := c.Set(ctx, fmt.Sprintf("k%03d", i%60), []byte(fmt.Sprint(i))); err != nil {
			t.Fatal(err)
		}
	}
	testkit.Eventually(t, 5*time.Second, func() bool {
		_, err := os.Stat(filepath.Join(dir, "snapshot.db"))
		return err == nil && logRecords(t, dir) <= 100
	}, "snapshot.db to exist and the log to hold at most 100 records with -snapshot-every 100")
	stopProc(t, p, syscall.SIGKILL)
	p2 := startBurrowd(t, bin, "-data", dir)
	e, err := dialProc(t, p2).Get(ctx, "k049")
	if err != nil || string(e.Value) != "349" {
		t.Errorf("snapshot plus log must restore the latest value: (%q, %v)", e.Value, err)
	}
}

func TestL5_1_Compact(t *testing.T) {
	dir := t.TempDir()
	ds, err := durable.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 300 {
		ds.Set(context.Background(), fmt.Sprintf("k%d", i%30), []byte(fmt.Sprint(i)))
	}
	ds.Close()
	var out, errOut bytes.Buffer
	if code := run([]string{"compact", "-data", dir}, &out, &errOut); code != 0 {
		t.Fatalf("burrowd compact exited %d: %s", code, errOut.String())
	}
	if n := logRecords(t, dir); n != 0 {
		t.Errorf("after compact the log should be empty, has %d records", n)
	}
	ds2, err := durable.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ds2.Close()
	if e, _ := ds2.Get(context.Background(), "k29"); string(e.Value) != "299" {
		t.Errorf("compact must preserve state: k29 = %q, want 299", e.Value)
	}
	if code := run([]string{"compact"}, &out, &errOut); code != 2 {
		t.Errorf("compact without -data is a usage error (exit 2), got %d", code)
	}
}

func TestL5_1_ListensOnlyAfterReplay(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary")
	}
	dir := t.TempDir()
	ds, err := durable.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for w := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 1000 {
				ds.Set(context.Background(), fmt.Sprintf("w%02d-%04d", w, i), []byte("v"))
			}
		}()
	}
	wg.Wait()
	ds.Close()
	p := startBurrowd(t, buildBurrowd(t), "-data", dir)
	keys, err := dialProc(t, p).Keys(testkit.Ctx(t), "w")
	if err != nil || len(keys) != 50_000 {
		t.Errorf("a client that connects right after `listening on` must see every key: got %d (%v). Replay before you listen.", len(keys), err)
	}
}
