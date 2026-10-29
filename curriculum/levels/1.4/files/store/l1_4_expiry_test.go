package store_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"burrow/internal/testkit"
	"burrow/store"
	"burrow/store/storetest"
)

var _ storetest.TTLStore = store.Store(nil) // your Store contract includes expiry now

func newTTLStore(t *testing.T) (*store.Memory, *testkit.FakeClock) {
	t.Helper()
	clock := testkit.NewFakeClock()
	return store.NewMemory(store.WithClock(clock.Now)), clock
}

func TestL1_4_TTLConformance(t *testing.T) {
	storetest.RunTTL(t, func(t *testing.T) storetest.TTLStore { return store.NewMemory() })
}

func TestL1_4_ExpiresAtExactlyTTL(t *testing.T) {
	m, clock := newTTLStore(t)
	e, err := m.SetWithTTL(bg, "session", []byte("abc"), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if want := clock.Now().Add(10 * time.Second); !e.ExpiresAt.Equal(want) {
		t.Errorf("ExpiresAt = %v, want %v (now from WithClock + ttl)", e.ExpiresAt, want)
	}
	clock.Advance(10*time.Second - time.Nanosecond)
	if g, err := m.Get(bg, "session"); err != nil {
		t.Fatalf("1ns before the TTL ends the key should still exist, got %v", err)
	} else if !g.ExpiresAt.Equal(e.ExpiresAt) {
		t.Errorf("Get should report the same ExpiresAt: %v vs %v", g.ExpiresAt, e.ExpiresAt)
	}
	clock.Advance(time.Nanosecond)
	if _, err := m.Get(bg, "session"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("at exactly 10s the key has expired (now >= ExpiresAt): Get should be ErrNotFound, got %v", err)
	}
}

func TestL1_4_NoTTLMeansZero(t *testing.T) {
	m, _ := newTTLStore(t)
	if e := mustSet(t, m, "k", "v"); !e.ExpiresAt.IsZero() {
		t.Errorf("a key without a TTL has a zero ExpiresAt, got %v", e.ExpiresAt)
	}
}

func TestL1_4_ExpiredIsInvisibleEverywhere(t *testing.T) {
	m, clock := newTTLStore(t)
	mustSet(t, m, "stays", "v")
	for _, k := range []string{"a", "b", "c", "d"} {
		if _, err := m.SetWithTTL(bg, k, []byte("v"), time.Second); err != nil {
			t.Fatal(err)
		}
	}
	clock.Advance(time.Second)
	if n := m.Len(); n != 1 {
		t.Errorf("Len should not count expired keys: got %d, want 1", n)
	}
	if keys, _ := m.Keys(bg, ""); !slices.Equal(keys, []string{"stays"}) {
		t.Errorf("Keys should skip expired keys: got %q", keys)
	}
	if err := m.Delete(bg, "a"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Delete of an expired key: want ErrNotFound, got %v", err)
	}
	if _, err := m.Touch(bg, "b", time.Hour); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Touch can't revive an expired key: want ErrNotFound, got %v", err)
	}
	if err := m.CompareAndDelete(bg, "c", 1); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("CompareAndDelete of an expired key: want ErrNotFound, got %v", err)
	}
	if _, err := m.CompareAndSwap(bg, "d", 1, []byte("x")); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("CAS with the old version on an expired key: want ErrNotFound, got %v", err)
	}
	e, err := m.CompareAndSwap(bg, "d", 0, []byte("fresh"))
	if err != nil || e.Version != 1 {
		t.Errorf("CAS ifVersion 0 on an expired key creates it at version 1, got (%d, %v)", e.Version, err)
	}
	e, err = m.Set(bg, "a", []byte("new"))
	if err != nil || e.Version != 1 {
		t.Errorf("Set on an expired key starts over at version 1, got (%d, %v)", e.Version, err)
	}
}

func TestL1_4_TouchKeepsVersion(t *testing.T) {
	m, clock := newTTLStore(t)
	mustSet(t, m, "k", "v")
	mustSet(t, m, "k", "v2")
	e, err := m.Touch(bg, "k", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if e.Version != 2 || string(e.Value) != "v2" {
		t.Errorf("Touch keeps value and version (a lease keepalive must not change the fencing token): %+v", e)
	}
	if want := clock.Now().Add(time.Minute); !e.ExpiresAt.Equal(want) {
		t.Errorf("Touch on a key without expiry adds one: ExpiresAt = %v, want %v", e.ExpiresAt, want)
	}
	clock.Advance(50 * time.Second)
	e, _ = m.Touch(bg, "k", time.Minute)
	clock.Advance(50 * time.Second)
	if _, err := m.Get(bg, "k"); err != nil {
		t.Errorf("Touch restarts the countdown from now: %v", err)
	}
	if !e.ExpiresAt.Equal(clock.Now().Add(10 * time.Second)) {
		t.Errorf("ExpiresAt after the second Touch = %v", e.ExpiresAt)
	}
}

func TestL1_4_SetWithTTLRestarts(t *testing.T) {
	m, clock := newTTLStore(t)
	m.SetWithTTL(bg, "k", []byte("1"), 10*time.Second)
	clock.Advance(8 * time.Second)
	e, _ := m.SetWithTTL(bg, "k", []byte("2"), 10*time.Second)
	if e.Version != 2 {
		t.Errorf("SetWithTTL on a live key bumps the version: got %d", e.Version)
	}
	clock.Advance(8 * time.Second)
	if got := mustGet(t, m, "k"); got != "2" {
		t.Errorf("setting again with a TTL restarts the countdown; got %q", got)
	}
}

func TestL1_4_CASWithTTLMismatch(t *testing.T) {
	m, clock := newTTLStore(t)
	m.SetWithTTL(bg, "lock", []byte("holder-1"), 10*time.Second)
	if _, err := m.CompareAndSwapWithTTL(bg, "lock", 0, []byte("holder-2"), 10*time.Second); !errors.Is(err, store.ErrVersionMismatch) {
		t.Errorf("acquiring a held lock: want ErrVersionMismatch, got %v", err)
	}
	g, _ := m.Get(bg, "lock")
	if string(g.Value) != "holder-1" || !g.ExpiresAt.Equal(clock.Now().Add(10*time.Second)) {
		t.Errorf("a failed CompareAndSwapWithTTL must not change the value or the expiry: %+v", g)
	}
}

func TestL1_4_TTLValidation(t *testing.T) {
	m, _ := newTTLStore(t)
	if _, err := m.SetWithTTL(bg, "bad key", []byte("v"), time.Second); !errors.Is(err, store.ErrInvalidKey) {
		t.Errorf("SetWithTTL validates keys first: got %v", err)
	}
	if _, err := m.SetWithTTL(bg, "k", []byte("v"), 0); !errors.Is(err, store.ErrInvalidTTL) {
		t.Errorf("ttl 0: want ErrInvalidTTL, got %v", err)
	}
	if m.Len() != 0 {
		t.Errorf("rejected writes must not store anything")
	}
}

func TestL1_4_DefaultClockIsReal(t *testing.T) {
	m := store.NewMemory()
	e, _ := m.SetWithTTL(bg, "k", []byte("v"), time.Hour)
	if d := time.Until(e.ExpiresAt); d < 59*time.Minute || d > 61*time.Minute {
		t.Errorf("without WithClock, NewMemory uses time.Now: ExpiresAt is %v away", d)
	}
}
