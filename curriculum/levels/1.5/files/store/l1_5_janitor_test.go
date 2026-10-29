package store_test

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"burrow/internal/testkit"
	"burrow/store"
)

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

func TestL1_5_JanitorSweepsWithoutReads(t *testing.T) {
	clock := testkit.NewFakeClock()
	var mu sync.Mutex
	evicted := map[string]int{}
	m := store.NewMemory(
		store.WithClock(clock.Now),
		store.WithSweepInterval(5*time.Millisecond),
		store.WithOnEvict(func(e store.Entry) {
			mu.Lock()
			evicted[e.Key]++
			mu.Unlock()
		}),
	)
	defer closeSoon(m)

	for i := range 100 {
		m.SetWithTTL(bg, fmt.Sprintf("t%03d", i), []byte("v"), time.Second)
	}
	mustSet(t, m, "keeper", "v")
	m.SetWithTTL(bg, "later", []byte("v"), time.Hour)
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

func TestL1_5_OnEvictGetsTheEntry(t *testing.T) {
	clock := testkit.NewFakeClock()
	got := make(chan store.Entry, 1)
	m := store.NewMemory(store.WithClock(clock.Now), store.WithSweepInterval(time.Millisecond),
		store.WithOnEvict(func(e store.Entry) { got <- e }))
	defer closeSoon(m)
	set, _ := m.SetWithTTL(bg, "k", []byte("payload"), time.Second)
	m.SetWithTTL(bg, "k", []byte("payload2"), time.Second) // version 2
	clock.Advance(time.Minute)
	select {
	case e := <-got:
		if e.Key != "k" || string(e.Value) != "payload2" || e.Version != set.Version+1 || e.ExpiresAt.IsZero() {
			t.Errorf("OnEvict should receive the expired Entry with its key, value, version and expiry, got %+v", e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("OnEvict was never called")
	}
}

func TestL1_5_EvictedOnceEvenWithReaders(t *testing.T) {
	clock := testkit.NewFakeClock()
	var calls atomic.Int64
	m := store.NewMemory(
		store.WithClock(clock.Now),
		store.WithSweepInterval(time.Millisecond),
		store.WithOnEvict(func(store.Entry) { calls.Add(1) }),
	)
	defer closeSoon(m)
	for i := range 200 {
		m.SetWithTTL(bg, fmt.Sprintf("k%d", i), []byte("v"), time.Second)
	}
	clock.Advance(time.Minute)
	var wg sync.WaitGroup
	for w := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := w; i < 200; i += 4 {
				m.Get(bg, fmt.Sprintf("k%d", i))
				m.Touch(bg, fmt.Sprintf("k%d", i), time.Hour)
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

func TestL1_5_NoEvictForExplicitChanges(t *testing.T) {
	clock := testkit.NewFakeClock()
	var calls atomic.Int64
	m := store.NewMemory(store.WithClock(clock.Now), store.WithSweepInterval(time.Millisecond),
		store.WithOnEvict(func(store.Entry) { calls.Add(1) }))
	defer closeSoon(m)
	m.SetWithTTL(bg, "a", []byte("v"), time.Second)
	m.SetWithTTL(bg, "b", []byte("v"), time.Second)
	m.SetWithTTL(bg, "c", []byte("v"), time.Second)
	m.Delete(bg, "a")
	mustSet(t, m, "b", "no ttl now")
	if err := m.CompareAndDelete(bg, "c", 1); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Hour)
	time.Sleep(30 * time.Millisecond)
	if n := calls.Load(); n != 0 {
		t.Errorf("OnEvict is for expiry only; Delete, overwrite and CompareAndDelete must not trigger it (got %d calls)", n)
	}
}

func TestL1_5_OnEvictMayUseTheStore(t *testing.T) {
	clock := testkit.NewFakeClock()
	var m *store.Memory
	done := make(chan struct{}, 1)
	m = store.NewMemory(store.WithClock(clock.Now), store.WithSweepInterval(time.Millisecond),
		store.WithOnEvict(func(e store.Entry) {
			m.Set(bg, "tombstone:"+e.Key, []byte("expired")) // calls back into the store
			done <- struct{}{}
		}))
	defer closeSoon(m)
	m.SetWithTTL(bg, "k", []byte("v"), time.Second)
	clock.Advance(2 * time.Second)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("OnEvict never finished. If you call it while holding the store's lock, a callback that " +
			"uses the store deadlocks. Collect the entries, unlock, then call OnEvict.")
	}
	testkit.Eventually(t, time.Second, func() bool {
		_, err := m.Get(bg, "tombstone:k")
		return err == nil
	}, "the callback's own Set to land")
}

func TestL1_5_RealClockSweep(t *testing.T) {
	m := store.NewMemory(store.WithSweepInterval(5 * time.Millisecond))
	defer closeSoon(m)
	m.SetWithTTL(bg, "k", []byte("v"), 30*time.Millisecond)
	testkit.Eventually(t, 2*time.Second, func() bool { return m.Len() == 0 }, "a 30ms TTL to be swept on the real clock")
}

func TestL1_5_CloseStopsJanitor(t *testing.T) {
	testkit.NoLeaks(t)
	m := store.NewMemory(store.WithSweepInterval(time.Millisecond))
	m.SetWithTTL(bg, "k", []byte("v"), time.Hour)
	testkit.Within(t, 2*time.Second, "Close", func() {
		if err := m.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
}

func TestL1_5_CloseIsIdempotentAndFinal(t *testing.T) {
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
	checks := map[string]error{}
	_, checks["Get"] = m.Get(bg, "k")
	_, checks["Set"] = m.Set(bg, "k", []byte("v"))
	checks["Delete"] = m.Delete(bg, "k")
	_, checks["Keys"] = m.Keys(bg, "")
	_, checks["CompareAndSwap"] = m.CompareAndSwap(bg, "k", 1, []byte("v"))
	checks["CompareAndDelete"] = m.CompareAndDelete(bg, "k", 1)
	_, checks["SetWithTTL"] = m.SetWithTTL(bg, "k", []byte("v"), time.Second)
	_, checks["Touch"] = m.Touch(bg, "k", time.Second)
	for name, err := range checks {
		if !errors.Is(err, store.ErrClosed) {
			t.Errorf("%s after Close should be store.ErrClosed, got %v", name, err)
		}
	}
}
