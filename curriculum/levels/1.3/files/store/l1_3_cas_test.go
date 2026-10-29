package store_test

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"burrow/internal/testkit"
	"burrow/store"
	"burrow/store/storetest"
)

var _ storetest.CASStore = store.Store(nil) // your Store contract includes CAS now

func TestL1_3_CASConformance(t *testing.T) {
	storetest.RunCAS(t, func(t *testing.T) storetest.CASStore { return store.NewMemory() })
}

func TestL1_3_SetReturnsVersion(t *testing.T) {
	m := store.NewMemory()
	for want := uint64(1); want <= 5; want++ {
		if e := mustSet(t, m, "k", "v"); e.Version != want {
			t.Fatalf("Set #%d returned Version %d", want, e.Version)
		}
	}
	other := mustSet(t, m, "other", "v")
	if other.Version != 1 {
		t.Errorf("versions are per key: a new key starts at 1, got %d", other.Version)
	}
}

func TestL1_3_MismatchChangesNothing(t *testing.T) {
	m := store.NewMemory()
	mustSet(t, m, "k", "original")
	mustSet(t, m, "k", "second")
	for _, v := range []uint64{0, 1, 3, 99} {
		_, err := m.CompareAndSwap(bg, "k", v, []byte("nope"))
		if !errors.Is(err, store.ErrVersionMismatch) {
			t.Errorf("CAS ifVersion=%d against version 2: want ErrVersionMismatch, got %v", v, err)
		}
	}
	e, _ := m.Get(bg, "k")
	if string(e.Value) != "second" || e.Version != 2 {
		t.Errorf("after failed CASes: %q version %d, want \"second\" version 2", e.Value, e.Version)
	}
}

func TestL1_3_CASCopiesValue(t *testing.T) {
	m := store.NewMemory()
	buf := []byte("hello")
	if _, err := m.CompareAndSwap(bg, "k", 0, buf); err != nil {
		t.Fatal(err)
	}
	buf[0] = 'J'
	if got := mustGet(t, m, "k"); got != "hello" {
		t.Errorf("the slice passed to CompareAndSwap must be copied; stored value became %q", got)
	}
}

func TestL1_3_ValidationFirst(t *testing.T) {
	m := store.NewMemory()
	if _, err := m.CompareAndSwap(bg, "bad key", 0, []byte("v")); !errors.Is(err, store.ErrInvalidKey) {
		t.Errorf("CAS with an invalid key: want ErrInvalidKey, got %v", err)
	}
	if err := m.CompareAndDelete(bg, "", 1); !errors.Is(err, store.ErrInvalidKey) {
		t.Errorf("CompareAndDelete with an invalid key: want ErrInvalidKey, got %v", err)
	}
	ctx, cancel := context.WithCancel(bg)
	cancel()
	if _, err := m.CompareAndSwap(ctx, "k", 0, []byte("v")); !errors.Is(err, context.Canceled) {
		t.Errorf("CAS with a cancelled context: want context.Canceled, got %v", err)
	}
	if m.Len() != 0 {
		t.Errorf("rejected CASes must not store anything")
	}
}

// The classic lost update: Get, compute, Set. CAS turns it into a retry loop.
func TestL1_3_LostUpdatesPrevented(t *testing.T) {
	testkit.RequireRace(t)
	m := store.NewMemory()
	const workers, incs = 50, 200
	var retries atomic.Int64
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range incs {
				for {
					e, err := m.Get(bg, "counter")
					n := 0
					if err == nil {
						n, _ = strconv.Atoi(string(e.Value))
					}
					if _, err = m.CompareAndSwap(bg, "counter", e.Version, []byte(strconv.Itoa(n+1))); err == nil {
						break
					} else if !errors.Is(err, store.ErrVersionMismatch) {
						t.Error(err)
						return
					}
					retries.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	if got := mustGet(t, m, "counter"); got != strconv.Itoa(workers*incs) {
		t.Errorf("after %d concurrent increments the counter is %s", workers*incs, got)
	}
	if retries.Load() == 0 {
		t.Logf("no version mismatches happened; the race detector slows things enough that this is unusual but possible")
	}
}

func TestL1_3_DeleteResetsVersion(t *testing.T) {
	m := store.NewMemory()
	mustSet(t, m, "k", "v")
	mustSet(t, m, "k", "v")
	if err := m.CompareAndDelete(bg, "k", 2); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CompareAndSwap(bg, "k", 2, []byte("v")); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("CAS with the old version after a delete should be ErrNotFound, got %v", err)
	}
	e, err := m.CompareAndSwap(bg, "k", 0, []byte("fresh"))
	if err != nil || e.Version != 1 {
		t.Errorf("recreating a deleted key with CAS ifVersion 0 gives version 1, got (%d, %v)", e.Version, err)
	}
}
