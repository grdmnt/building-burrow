package storetest

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"burrow/store"
)

// CASStore is Store plus versioned writes (1.3).
type CASStore interface {
	Store
	CompareAndSwap(ctx context.Context, key string, ifVersion uint64, value []byte) (store.Entry, error)
	CompareAndDelete(ctx context.Context, key string, ifVersion uint64) error
}

// RunCAS checks versions and compare-and-swap.
func RunCAS(t *testing.T, newStore func(t *testing.T) CASStore) {
	ctx := context.Background()

	t.Run("VersionsCount", func(t *testing.T) {
		s := newStore(t)
		for want := uint64(1); want <= 3; want++ {
			e, err := s.Set(ctx, "k", []byte("v"))
			if err != nil || e.Version != want {
				t.Fatalf("Set #%d returned version %d (%v), want %d", want, e.Version, err, want)
			}
		}
		if e, _ := s.Get(ctx, "k"); e.Version != 3 {
			t.Errorf("Get reports version %d, want 3", e.Version)
		}
		if err := s.Delete(ctx, "k"); err != nil {
			t.Fatal(err)
		}
		if e, _ := s.Set(ctx, "k", []byte("v")); e.Version != 1 {
			t.Errorf("a key deleted and set again starts over at version 1, got %d", e.Version)
		}
	})

	t.Run("CompareAndSwap", func(t *testing.T) {
		s := newStore(t)
		s.Set(ctx, "k", []byte("one"))
		e, err := s.CompareAndSwap(ctx, "k", 1, []byte("two"))
		if err != nil || e.Version != 2 || string(e.Value) != "two" {
			t.Fatalf("CAS with the right version: (%+v, %v), want version 2 value two", e, err)
		}
		_, err = s.CompareAndSwap(ctx, "k", 1, []byte("three"))
		if !errors.Is(err, store.ErrVersionMismatch) {
			t.Fatalf("CAS with a stale version should be ErrVersionMismatch, got %v", err)
		}
		if g, _ := s.Get(ctx, "k"); string(g.Value) != "two" || g.Version != 2 {
			t.Errorf("a failed CAS must change nothing: got %q version %d", g.Value, g.Version)
		}
	})

	t.Run("CreateIfAbsent", func(t *testing.T) {
		s := newStore(t)
		e, err := s.CompareAndSwap(ctx, "new", 0, []byte("x"))
		if err != nil || e.Version != 1 {
			t.Fatalf("CAS ifVersion 0 on a missing key creates it with version 1: (%+v, %v)", e, err)
		}
		if _, err := s.CompareAndSwap(ctx, "new", 0, []byte("y")); !errors.Is(err, store.ErrVersionMismatch) {
			t.Errorf("CAS ifVersion 0 on an existing key should be ErrVersionMismatch, got %v", err)
		}
		if _, err := s.CompareAndSwap(ctx, "missing", 7, []byte("y")); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("CAS ifVersion 7 on a missing key should be ErrNotFound, got %v", err)
		}
	})

	t.Run("CompareAndDelete", func(t *testing.T) {
		s := newStore(t)
		s.Set(ctx, "k", []byte("v"))
		s.Set(ctx, "k", []byte("v"))
		if err := s.CompareAndDelete(ctx, "k", 1); !errors.Is(err, store.ErrVersionMismatch) {
			t.Errorf("CompareAndDelete with a stale version should be ErrVersionMismatch, got %v", err)
		}
		if _, err := s.Get(ctx, "k"); err != nil {
			t.Errorf("a failed CompareAndDelete must not delete: %v", err)
		}
		if err := s.CompareAndDelete(ctx, "k", 2); err != nil {
			t.Errorf("CompareAndDelete with the right version: %v", err)
		}
		if err := s.CompareAndDelete(ctx, "k", 2); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("CompareAndDelete of a missing key should be ErrNotFound, got %v", err)
		}
	})

	t.Run("Counter", func(t *testing.T) {
		s := newStore(t)
		const workers, incs = 20, 50
		var mismatches atomic.Int64
		var wg sync.WaitGroup
		for range workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range incs {
					for {
						e, err := s.Get(ctx, "counter")
						n, ver := 0, uint64(0)
						if err == nil {
							n, _ = strconv.Atoi(string(e.Value))
							ver = e.Version
						} else if !errors.Is(err, store.ErrNotFound) {
							t.Error(err)
							return
						}
						_, err = s.CompareAndSwap(ctx, "counter", ver, []byte(strconv.Itoa(n+1)))
						if err == nil {
							break
						}
						if errors.Is(err, store.ErrVersionMismatch) {
							mismatches.Add(1)
							continue
						}
						t.Error(err)
						return
					}
				}
			}()
		}
		wg.Wait()
		e, err := s.Get(ctx, "counter")
		if err != nil || string(e.Value) != strconv.Itoa(workers*incs) {
			t.Fatalf("after %d CAS increments the counter is %q (%v)", workers*incs, e.Value, err)
		}
		if e.Version != workers*incs {
			t.Errorf("the counter's version is %d, want %d (one per successful write)", e.Version, workers*incs)
		}
		t.Logf("%d retries after version mismatches", mismatches.Load())
	})
}
