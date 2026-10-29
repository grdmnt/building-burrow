package store_test

import (
	"testing"

	"burrow/store"
	"burrow/store/storetest"
)

// These two lines don't run; they only compile if the types line up.
var (
	_ store.Store     = (*store.Memory)(nil) // Memory satisfies your Store interface
	_ storetest.Store = store.Store(nil)     // your Store has every method the suite needs
)

func TestL1_2_MemoryConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T) storetest.Store {
		return store.NewMemory()
	})
}

func TestL1_2_KeysOnEmptyStore(t *testing.T) {
	keys, err := store.NewMemory().Keys(bg, "")
	if err != nil {
		t.Fatalf("Keys on an empty store: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("Keys on an empty store = %q, want none", keys)
	}
}

func TestL1_2_KeysLarge(t *testing.T) {
	m := store.NewMemory()
	for i := 999; i >= 0; i-- {
		mustSet(t, m, "k"+pad(i), "v")
	}
	keys, err := m.Keys(bg, "k")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1000 {
		t.Fatalf("Keys returned %d keys, want 1000", len(keys))
	}
	for i, k := range keys {
		if k != "k"+pad(i) {
			t.Fatalf("Keys()[%d] = %q, want %q: results must be sorted", i, k, "k"+pad(i))
		}
	}
}

func pad(i int) string {
	s := itoa(i)
	for len(s) < 4 {
		s = "0" + s
	}
	return s
}
