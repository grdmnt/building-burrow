package store_test

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"

	"burrow/internal/testkit"
	"burrow/store"
	"burrow/store/storetest"
)

func TestL1_3_RaceDetectorOn(t *testing.T) {
	testkit.RequireRace(t)
}

func TestL1_3_MemoryConcurrentConformance(t *testing.T) {
	testkit.RequireRace(t)
	storetest.RunConcurrent(t, func(t *testing.T) storetest.Store { return store.NewMemory() })
}

func TestL1_3_LenAndKeysDuringWrites(t *testing.T) {
	testkit.RequireRace(t)
	m := store.NewMemory()
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			_ = m.Set(bg, fmt.Sprintf("k%d", i%500), []byte("v"))
			if i%3 == 0 {
				_ = m.Delete(bg, fmt.Sprintf("k%d", (i+250)%500))
			}
		}
	}()
	for range 2000 {
		n := m.Len()
		if n < 0 || n > 500 {
			t.Fatalf("Len() = %d while writing; that's impossible", n)
		}
		if _, err := m.Keys(bg, "k"); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
}

// Update must be atomic: read-modify-write with no other writer in between.
func TestL1_3_UpdateIsAtomic(t *testing.T) {
	testkit.RequireRace(t)
	m := store.NewMemory()
	const workers, incs = 50, 200
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range incs {
				err := m.Update(bg, "counter", func(old []byte, exists bool) ([]byte, error) {
					n := 0
					if exists {
						n, _ = strconv.Atoi(string(old))
					}
					return []byte(strconv.Itoa(n + 1)), nil
				})
				if err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if got := mustGet(t, m, "counter"); got != strconv.Itoa(workers*incs) {
		t.Errorf("after %d concurrent increments the counter is %s. "+
			"A Get followed by a Set isn't atomic: another goroutine can write in between.", workers*incs, got)
	}
}

func TestL1_3_UpdateMissingKey(t *testing.T) {
	m := store.NewMemory()
	called := false
	err := m.Update(bg, "fresh", func(old []byte, exists bool) ([]byte, error) {
		called = true
		if exists || old != nil {
			t.Errorf("for a missing key, fn should get (nil, false), got (%q, %v)", old, exists)
		}
		return []byte("made"), nil
	})
	if err != nil || !called {
		t.Fatalf("Update on a missing key: err=%v called=%v", err, called)
	}
	if got := mustGet(t, m, "fresh"); got != "made" {
		t.Errorf("Update should create the key, got %q", got)
	}
}

func TestL1_3_UpdateErrorLeavesValueAlone(t *testing.T) {
	m := store.NewMemory()
	mustSet(t, m, "k", "original")
	boom := errors.New("boom")
	err := m.Update(bg, "k", func(old []byte, exists bool) ([]byte, error) {
		old[0] = 'X' // a careless callback scribbles on what it was given...
		return nil, boom
	})
	if !errors.Is(err, boom) {
		t.Errorf("Update should return the callback's error, got %v", err)
	}
	if got := mustGet(t, m, "k"); got != "original" {
		t.Errorf("after a failed Update the value is %q, want %q. "+
			"Hand the callback a copy, so its mistakes can't reach the store.", got, "original")
	}
}

func TestL1_3_UpdateCopiesResult(t *testing.T) {
	m := store.NewMemory()
	var kept []byte
	_ = m.Update(bg, "k", func([]byte, bool) ([]byte, error) {
		kept = []byte("value")
		return kept, nil
	})
	kept[0] = 'X'
	if got := mustGet(t, m, "k"); got != "value" {
		t.Errorf("the slice returned from the callback must be copied; stored value became %q", got)
	}
}

func TestL1_3_UpdateValidation(t *testing.T) {
	m := store.NewMemory()
	called := false
	err := m.Update(bg, "bad key", func([]byte, bool) ([]byte, error) { called = true; return nil, nil })
	if !errors.Is(err, store.ErrInvalidKey) {
		t.Errorf("Update with an invalid key should be ErrInvalidKey, got %v", err)
	}
	if called {
		t.Errorf("don't call the callback for an invalid key")
	}
}

func TestL1_3_ConcurrentReadersSeeWholeValues(t *testing.T) {
	testkit.RequireRace(t)
	m := store.NewMemory()
	a := bytes.Repeat([]byte("a"), 4096)
	b := bytes.Repeat([]byte("b"), 4096)
	mustSet(t, m, "k", string(a))
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			v := a
			if i%2 == 1 {
				v = b
			}
			_ = m.Set(bg, "k", v)
		}
	}()
	for range 3000 {
		v, err := m.Get(bg, "k")
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(v, a) && !bytes.Equal(v, b) {
			t.Fatalf("Get returned a mix of two writes")
		}
	}
	close(stop)
	wg.Wait()
}
