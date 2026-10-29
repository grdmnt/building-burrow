package store_test

import (
	"testing"
	"time"

	"burrow/internal/testkit"
	"burrow/store"
)

func TestL4_2_RestoreInstallsExactly(t *testing.T) {
	clock := testkit.NewFakeClock()
	m := store.NewMemory(store.WithClock(clock.Now))
	exp := clock.Now().Add(time.Minute)
	if err := m.Restore(store.Entry{Key: "k", Value: []byte("v"), Version: 42, ExpiresAt: exp}); err != nil {
		t.Fatal(err)
	}
	e, err := m.Get(bg, "k")
	if err != nil || e.Version != 42 || !e.ExpiresAt.Equal(exp) || string(e.Value) != "v" {
		t.Errorf("Restore installs the entry as given, version and expiry included: (%+v, %v)", e, err)
	}
	if e, _ := m.Set(bg, "k", []byte("w")); e.Version != 43 {
		t.Errorf("versions continue from a restored entry: Set gave %d, want 43", e.Version)
	}
}

func TestL4_2_RestoreDropsExpired(t *testing.T) {
	clock := testkit.NewFakeClock()
	evicted := 0
	m := store.NewMemory(store.WithClock(clock.Now), store.WithOnEvict(func(store.Entry) { evicted++ }))
	m.Set(bg, "k", []byte("old"))
	if err := m.Restore(store.Entry{Key: "k", Value: []byte("v"), Version: 3, ExpiresAt: clock.Now().Add(-time.Second)}); err != nil {
		t.Errorf("restoring an already-expired entry is not an error: %v", err)
	}
	if m.Len() != 0 {
		t.Errorf("an already-expired entry must not be installed (and replaces what was there)")
	}
	if evicted != 0 {
		t.Errorf("Restore of an expired entry never reaches OnEvict")
	}
}

func TestL4_2_NowUsesTheClock(t *testing.T) {
	clock := testkit.NewFakeClock()
	m := store.NewMemory(store.WithClock(clock.Now))
	if !m.Now().Equal(clock.Now()) {
		t.Errorf("Memory.Now should read the store's clock: %v vs %v", m.Now(), clock.Now())
	}
}
