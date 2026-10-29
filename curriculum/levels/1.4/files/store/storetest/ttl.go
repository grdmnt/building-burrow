package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"burrow/store"
)

// TTLStore is CASStore plus expiry (1.4).
type TTLStore interface {
	CASStore
	SetWithTTL(ctx context.Context, key string, value []byte, ttl time.Duration) (store.Entry, error)
	Touch(ctx context.Context, key string, ttl time.Duration) (store.Entry, error)
	CompareAndSwapWithTTL(ctx context.Context, key string, ifVersion uint64, value []byte, ttl time.Duration) (store.Entry, error)
}

// RunTTL checks expiry on the real clock, with short TTLs, so it works for any
// engine, local or remote.
func RunTTL(t *testing.T, newStore func(t *testing.T) TTLStore) {
	ctx := context.Background()
	const ttl = 60 * time.Millisecond

	gone := func(t *testing.T, s TTLStore, key string) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for {
			_, err := s.Get(ctx, key)
			if errors.Is(err, store.ErrNotFound) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%q should have expired by now, Get returned err=%v", key, err)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}

	t.Run("Expires", func(t *testing.T) {
		s := newStore(t)
		before := time.Now()
		e, err := s.SetWithTTL(ctx, "k", []byte("v"), ttl)
		if err != nil {
			t.Fatal(err)
		}
		if e.ExpiresAt.Before(before.Add(ttl-20*time.Millisecond)) || e.ExpiresAt.After(time.Now().Add(ttl+20*time.Millisecond)) {
			t.Errorf("ExpiresAt = %v, want about now+%v", e.ExpiresAt, ttl)
		}
		if _, err := s.Get(ctx, "k"); err != nil {
			t.Fatalf("the key should exist right after SetWithTTL: %v", err)
		}
		gone(t, s, "k")
		keys, _ := s.Keys(ctx, "")
		if len(keys) != 0 {
			t.Errorf("Keys lists an expired key: %q", keys)
		}
		if err := s.Delete(ctx, "k"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("Delete of an expired key should be ErrNotFound, got %v", err)
		}
	})

	t.Run("PlainSetClearsTTL", func(t *testing.T) {
		s := newStore(t)
		s.SetWithTTL(ctx, "k", []byte("temp"), ttl)
		e, err := s.Set(ctx, "k", []byte("forever"))
		if err != nil || !e.ExpiresAt.IsZero() {
			t.Fatalf("a plain Set clears the expiry: ExpiresAt = %v (%v)", e.ExpiresAt, err)
		}
		time.Sleep(2 * ttl)
		if g, err := s.Get(ctx, "k"); err != nil || string(g.Value) != "forever" {
			t.Errorf("the key expired even though Set cleared its TTL: (%q, %v)", g.Value, err)
		}
	})

	t.Run("TouchExtends", func(t *testing.T) {
		s := newStore(t)
		set, _ := s.SetWithTTL(ctx, "k", []byte("v"), ttl)
		time.Sleep(ttl / 2)
		e, err := s.Touch(ctx, "k", 10*time.Second)
		if err != nil {
			t.Fatalf("Touch: %v", err)
		}
		if e.Version != set.Version || string(e.Value) != "v" {
			t.Errorf("Touch must not change the value or version: got %q version %d, want v version %d", e.Value, e.Version, set.Version)
		}
		time.Sleep(ttl)
		if _, err := s.Get(ctx, "k"); err != nil {
			t.Errorf("after Touch the key should outlive its original TTL: %v", err)
		}
		if _, err := s.Touch(ctx, "missing", ttl); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("Touch of a missing key should be ErrNotFound, got %v", err)
		}
	})

	t.Run("InvalidTTL", func(t *testing.T) {
		s := newStore(t)
		s.Set(ctx, "k", []byte("keep"))
		for _, d := range []time.Duration{0, -time.Second} {
			if _, err := s.SetWithTTL(ctx, "k", []byte("x"), d); !errors.Is(err, store.ErrInvalidTTL) {
				t.Errorf("SetWithTTL ttl=%v: want ErrInvalidTTL, got %v", d, err)
			}
			if _, err := s.Touch(ctx, "k", d); !errors.Is(err, store.ErrInvalidTTL) {
				t.Errorf("Touch ttl=%v: want ErrInvalidTTL, got %v", d, err)
			}
			if _, err := s.CompareAndSwapWithTTL(ctx, "k", 1, []byte("x"), d); !errors.Is(err, store.ErrInvalidTTL) {
				t.Errorf("CompareAndSwapWithTTL ttl=%v: want ErrInvalidTTL, got %v", d, err)
			}
		}
		if g, _ := s.Get(ctx, "k"); string(g.Value) != "keep" || g.Version != 1 || !g.ExpiresAt.IsZero() {
			t.Errorf("rejected TTL operations must change nothing: %+v", g)
		}
	})

	t.Run("CASWithTTL", func(t *testing.T) {
		s := newStore(t)
		e, err := s.CompareAndSwapWithTTL(ctx, "lock", 0, []byte("me"), ttl)
		if err != nil || e.Version != 1 || e.ExpiresAt.IsZero() {
			t.Fatalf("create-if-absent with a TTL: (%+v, %v)", e, err)
		}
		if _, err := s.CompareAndSwapWithTTL(ctx, "lock", 0, []byte("you"), ttl); !errors.Is(err, store.ErrVersionMismatch) {
			t.Errorf("a second create-if-absent should be ErrVersionMismatch, got %v", err)
		}
		gone(t, s, "lock")
		e, err = s.CompareAndSwapWithTTL(ctx, "lock", 0, []byte("you"), ttl)
		if err != nil || e.Version != 1 {
			t.Errorf("once expired the key is absent again: create-if-absent should succeed with version 1, got (%d, %v)", e.Version, err)
		}
		c, err := s.CompareAndSwap(ctx, "lock", e.Version, []byte("plain"))
		if err != nil || !c.ExpiresAt.IsZero() {
			t.Errorf("a plain CompareAndSwap clears the expiry, like Set: (%v, %v)", c.ExpiresAt, err)
		}
	})
}
