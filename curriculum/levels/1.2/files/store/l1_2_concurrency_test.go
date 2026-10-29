package store_test

import (
	"bytes"
	"fmt"
	"sync"
	"testing"

	"burrow/internal/testkit"
	"burrow/store"
	"burrow/store/storetest"
)

func TestL1_2_RaceDetectorOn(t *testing.T) {
	testkit.RequireRace(t)
}

func TestL1_2_ConcurrentConformance(t *testing.T) {
	testkit.RequireRace(t)
	storetest.RunConcurrent(t, func(t *testing.T) storetest.Store { return store.NewMemory() })
}

func TestL1_2_LenAndKeysDuringWrites(t *testing.T) {
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
			_, _ = m.Set(bg, fmt.Sprintf("k%d", i%500), []byte("v"))
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

func TestL1_2_ReadersSeeWholeValues(t *testing.T) {
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
			_, _ = m.Set(bg, "k", v)
		}
	}()
	for range 3000 {
		e, err := m.Get(bg, "k")
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(e.Value, a) && !bytes.Equal(e.Value, b) {
			t.Fatalf("Get returned a mix of two writes")
		}
	}
	close(stop)
	wg.Wait()
}

func TestL1_2_ManyStoresInParallel(t *testing.T) {
	testkit.RequireRace(t)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m := store.NewMemory()
			for j := range 200 {
				_, _ = m.Set(bg, fmt.Sprintf("s%d-%d", i, j), []byte("v"))
			}
			if m.Len() != 200 {
				t.Errorf("store %d has %d keys, want 200: stores must not share state", i, m.Len())
			}
		}()
	}
	wg.Wait()
}
