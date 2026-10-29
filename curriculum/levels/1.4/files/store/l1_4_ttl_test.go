package store_test

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"burrow/internal/testkit"
	"burrow/store"
)

// newTTLStore builds a Memory on a fake clock with no background sweeping,
// so these tests check expiry on read.
func newTTLStore(t *testing.T) (*store.Memory, *testkit.FakeClock) {
	t.Helper()
	clock := testkit.NewFakeClock()
	m := store.NewMemory(store.WithClock(clock.Now), store.WithSweepInterval(time.Hour))
	t.Cleanup(func() { m.Close() })
	return m, clock
}

func TestL1_4_ExpiresAtExactlyTTL(t *testing.T) {
	m, clock := newTTLStore(t)
	if err := m.SetWithTTL(bg, "session", []byte("abc"), 10*time.Second); err != nil {
		t.Fatal(err)
	}
	clock.Advance(10*time.Second - time.Nanosecond)
	if _, err := m.Get(bg, "session"); err != nil {
		t.Fatalf("1ns before the TTL ends the key should still exist, got %v", err)
	}
	clock.Advance(time.Nanosecond)
	if _, err := m.Get(bg, "session"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("at exactly 10s the key has expired (expired means now >= expiry), Get should be ErrNotFound, got %v", err)
	}
}

func TestL1_4_TTLRemaining(t *testing.T) {
	m, clock := newTTLStore(t)
	_ = m.SetWithTTL(bg, "k", []byte("v"), time.Minute)
	clock.Advance(15 * time.Second)
	got, err := m.TTL(bg, "k")
	if err != nil || got != 45*time.Second {
		t.Errorf("TTL after 15s of a 1m TTL = (%v, %v), want (45s, nil)", got, err)
	}
	mustSet(t, m, "forever", "v")
	if got, err := m.TTL(bg, "forever"); err != nil || got != 0 {
		t.Errorf("TTL of a key without expiry = (%v, %v), want (0, nil)", got, err)
	}
	if _, err := m.TTL(bg, "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("TTL of a missing key should be ErrNotFound, got %v", err)
	}
	clock.Advance(time.Hour)
	if _, err := m.TTL(bg, "k"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("TTL of an expired key should be ErrNotFound, got %v", err)
	}
}

func TestL1_4_PlainSetClearsTTL(t *testing.T) {
	m, clock := newTTLStore(t)
	_ = m.SetWithTTL(bg, "k", []byte("temp"), time.Second)
	mustSet(t, m, "k", "permanent")
	clock.Advance(time.Hour)
	if got := mustGet(t, m, "k"); got != "permanent" {
		t.Errorf("a plain Set replaces the value and removes any TTL, got %q", got)
	}
}

func TestL1_4_SetWithTTLResetsExpiry(t *testing.T) {
	m, clock := newTTLStore(t)
	_ = m.SetWithTTL(bg, "k", []byte("1"), 10*time.Second)
	clock.Advance(8 * time.Second)
	_ = m.SetWithTTL(bg, "k", []byte("2"), 10*time.Second)
	clock.Advance(8 * time.Second)
	if got := mustGet(t, m, "k"); got != "2" {
		t.Errorf("setting again with a TTL restarts the countdown; got %q", got)
	}
}

func TestL1_4_InvalidTTL(t *testing.T) {
	m, _ := newTTLStore(t)
	mustSet(t, m, "k", "keep")
	for _, ttl := range []time.Duration{0, -time.Second} {
		err := m.SetWithTTL(bg, "k", []byte("new"), ttl)
		if !errors.Is(err, store.ErrInvalidTTL) {
			t.Errorf("SetWithTTL with ttl=%v should be ErrInvalidTTL, got %v", ttl, err)
		}
	}
	if got := mustGet(t, m, "k"); got != "keep" {
		t.Errorf("a rejected SetWithTTL must not change the value, got %q", got)
	}
	if err := m.SetWithTTL(bg, "bad key", []byte("v"), time.Second); !errors.Is(err, store.ErrInvalidKey) {
		t.Errorf("SetWithTTL validates keys too, got %v", err)
	}
}

func TestL1_4_ExpiredKeysAreInvisible(t *testing.T) {
	m, clock := newTTLStore(t)
	mustSet(t, m, "a", "stays")
	_ = m.SetWithTTL(bg, "b", []byte("goes"), time.Second)
	_ = m.SetWithTTL(bg, "c", []byte("goes"), time.Second)
	clock.Advance(time.Second)

	if n := m.Len(); n != 1 {
		t.Errorf("Len() should not count expired keys: got %d, want 1", n)
	}
	keys, _ := m.Keys(bg, "")
	if !slices.Equal(keys, []string{"a"}) {
		t.Errorf("Keys should skip expired keys: got %q", keys)
	}
	if err := m.Delete(bg, "b"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("deleting an expired key should be ErrNotFound, got %v", err)
	}
	_ = m.Update(bg, "c", func(old []byte, exists bool) ([]byte, error) {
		if exists {
			t.Errorf("Update should see an expired key as missing, got exists=true old=%q", old)
		}
		return []byte("reborn"), nil
	})
	clock.Advance(time.Hour)
	if got := mustGet(t, m, "c"); got != "reborn" {
		t.Errorf("Update on an expired key creates a fresh key with no TTL, got %q", got)
	}
}

func TestL1_4_JanitorSweepsWithoutReads(t *testing.T) {
	clock := testkit.NewFakeClock()
	var mu sync.Mutex
	evicted := map[string]int{}
	m := store.NewMemory(
		store.WithClock(clock.Now),
		store.WithSweepInterval(5*time.Millisecond),
		store.WithOnEvict(func(key string) {
			mu.Lock()
			evicted[key]++
			mu.Unlock()
		}),
	)
	defer m.Close()

	for i := range 100 {
		_ = m.SetWithTTL(bg, fmt.Sprintf("t%03d", i), []byte("v"), time.Second)
	}
	mustSet(t, m, "keeper", "v")
	_ = m.SetWithTTL(bg, "later", []byte("v"), time.Hour)
	clock.Advance(2 * time.Second)

	testkit.Eventually(t, 3*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(evicted) == 100
	}, "the janitor to evict all 100 expired keys without anyone reading them")

	time.Sleep(30 * time.Millisecond) // a few more sweeps
	mu.Lock()
	defer mu.Unlock()
	for k, n := range evicted {
		if n != 1 {
			t.Errorf("OnEvict(%q) was called %d times, want exactly once", k, n)
		}
	}
	if evicted["keeper"] > 0 || evicted["later"] > 0 {
		t.Errorf("only expired keys may be evicted, got %v", evicted)
	}
}

func TestL1_4_EvictedOnceEvenWithReaders(t *testing.T) {
	clock := testkit.NewFakeClock()
	var calls atomic.Int64
	m := store.NewMemory(
		store.WithClock(clock.Now),
		store.WithSweepInterval(time.Millisecond),
		store.WithOnEvict(func(string) { calls.Add(1) }),
	)
	defer m.Close()
	for i := range 200 {
		_ = m.SetWithTTL(bg, fmt.Sprintf("k%d", i), []byte("v"), time.Second)
	}
	clock.Advance(time.Minute)
	var wg sync.WaitGroup
	for w := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := w; i < 200; i += 4 {
				_, _ = m.Get(bg, fmt.Sprintf("k%d", i))
			}
		}()
	}
	wg.Wait()
	testkit.Eventually(t, 3*time.Second, func() bool { return calls.Load() >= 200 }, "200 evictions")
	time.Sleep(20 * time.Millisecond)
	if n := calls.Load(); n != 200 {
		t.Errorf("200 keys expired but OnEvict ran %d times. Readers and the janitor both noticed; only one may evict.", n)
	}
}

func TestL1_4_NoEvictForDeleteOrOverwrite(t *testing.T) {
	clock := testkit.NewFakeClock()
	var calls atomic.Int64
	m := store.NewMemory(store.WithClock(clock.Now), store.WithSweepInterval(time.Millisecond),
		store.WithOnEvict(func(string) { calls.Add(1) }))
	defer m.Close()
	_ = m.SetWithTTL(bg, "a", []byte("v"), time.Second)
	_ = m.SetWithTTL(bg, "b", []byte("v"), time.Second)
	_ = m.Delete(bg, "a")
	mustSet(t, m, "b", "no ttl now")
	clock.Advance(time.Hour)
	time.Sleep(30 * time.Millisecond)
	if n := calls.Load(); n != 0 {
		t.Errorf("OnEvict is for expiry only; explicit Delete or overwrite must not trigger it (got %d calls)", n)
	}
}

func TestL1_4_OnEvictMayUseTheStore(t *testing.T) {
	clock := testkit.NewFakeClock()
	var m *store.Memory
	done := make(chan struct{}, 1)
	m = store.NewMemory(store.WithClock(clock.Now), store.WithSweepInterval(time.Millisecond),
		store.WithOnEvict(func(key string) {
			_ = m.Set(bg, "tombstone:"+key, []byte("expired")) // calls back into the store
			done <- struct{}{}
		}))
	defer closeSoon(m)
	_ = m.SetWithTTL(bg, "k", []byte("v"), time.Second)
	clock.Advance(2 * time.Second)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("OnEvict never finished. If you call it while holding the store's lock, a callback that " +
			"uses the store deadlocks. Collect the keys, unlock, then call OnEvict.")
	}
	testkit.Eventually(t, time.Second, func() bool {
		_, err := m.Get(bg, "tombstone:k")
		return err == nil
	}, "the callback's own Set to land")
}

func TestL1_4_RealClock(t *testing.T) {
	m := store.NewMemory(store.WithSweepInterval(5 * time.Millisecond))
	defer m.Close()
	_ = m.SetWithTTL(bg, "k", []byte("v"), 30*time.Millisecond)
	if _, err := m.Get(bg, "k"); err != nil {
		t.Fatalf("key should exist right after SetWithTTL: %v", err)
	}
	testkit.Eventually(t, 2*time.Second, func() bool {
		_, err := m.Get(bg, "k")
		return errors.Is(err, store.ErrNotFound)
	}, "a 30ms TTL to expire on the real clock")
}

func TestL1_4_CloseStopsJanitor(t *testing.T) {
	testkit.NoLeaks(t)
	m := store.NewMemory(store.WithSweepInterval(time.Millisecond))
	_ = m.SetWithTTL(bg, "k", []byte("v"), time.Hour)
	testkit.Within(t, 2*time.Second, "Close", func() {
		if err := m.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
}

func TestL1_4_CloseIsIdempotentAndFinal(t *testing.T) {
	testkit.NoLeaks(t)
	m := store.NewMemory()
	mustSet(t, m, "k", "v")
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := m.Close(); err != nil {
				t.Errorf("Close from many goroutines at once should all return nil, got %v", err)
			}
		}()
	}
	wg.Wait()
	if err := m.Close(); err != nil {
		t.Errorf("Close a second time should return nil, got %v", err)
	}
	if _, err := m.Get(bg, "k"); !errors.Is(err, store.ErrClosed) {
		t.Errorf("Get after Close should be store.ErrClosed, got %v", err)
	}
	if err := m.Set(bg, "k", []byte("v")); !errors.Is(err, store.ErrClosed) {
		t.Errorf("Set after Close should be store.ErrClosed, got %v", err)
	}
	if err := m.SetWithTTL(bg, "k", []byte("v"), time.Second); !errors.Is(err, store.ErrClosed) {
		t.Errorf("SetWithTTL after Close should be store.ErrClosed, got %v", err)
	}
}

// closeSoon closes m but gives up after a second, so a deadlocked store fails
// the test instead of hanging the whole run.
func closeSoon(m *store.Memory) {
	done := make(chan struct{})
	go func() {
		m.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
	}
}
