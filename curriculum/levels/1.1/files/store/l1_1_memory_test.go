package store_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"burrow/store"
)

var bg = context.Background()

func mustSet(t *testing.T, m *store.Memory, k, v string) {
	t.Helper()
	if err := m.Set(bg, k, []byte(v)); err != nil {
		t.Fatalf("Set(%q): unexpected error %v", k, err)
	}
}

func mustGet(t *testing.T, m *store.Memory, k string) string {
	t.Helper()
	v, err := m.Get(bg, k)
	if err != nil {
		t.Fatalf("Get(%q): unexpected error %v", k, err)
	}
	return string(v)
}

func TestL1_1_SetThenGet(t *testing.T) {
	m := store.NewMemory()
	mustSet(t, m, "name", "gerard")
	if got := mustGet(t, m, "name"); got != "gerard" {
		t.Errorf("Get(name) = %q, want %q", got, "gerard")
	}
}

func TestL1_1_GetMissing(t *testing.T) {
	m := store.NewMemory()
	v, err := m.Get(bg, "nope")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Get of a missing key should return an error matching store.ErrNotFound, got %v", err)
	}
	if v != nil {
		t.Errorf("Get of a missing key should return a nil value, got %q", v)
	}
}

func TestL1_1_Overwrite(t *testing.T) {
	m := store.NewMemory()
	mustSet(t, m, "k", "one")
	mustSet(t, m, "k", "two")
	if got := mustGet(t, m, "k"); got != "two" {
		t.Errorf("after overwriting, Get = %q, want %q", got, "two")
	}
	if m.Len() != 1 {
		t.Errorf("overwriting must not add a second entry: Len() = %d, want 1", m.Len())
	}
}

func TestL1_1_Delete(t *testing.T) {
	m := store.NewMemory()
	mustSet(t, m, "k", "v")
	if err := m.Delete(bg, "k"); err != nil {
		t.Fatalf("Delete of an existing key: unexpected error %v", err)
	}
	if _, err := m.Get(bg, "k"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Get after Delete should be ErrNotFound, got %v", err)
	}
	if err := m.Delete(bg, "k"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("deleting a key that's already gone should return ErrNotFound, got %v", err)
	}
	mustSet(t, m, "k", "again")
	if got := mustGet(t, m, "k"); got != "again" {
		t.Errorf("a deleted key can be set again: Get = %q, want %q", got, "again")
	}
}

func TestL1_1_EmptyValueIsNotMissing(t *testing.T) {
	m := store.NewMemory()
	for _, tc := range []struct {
		name string
		v    []byte
	}{{"empty slice", []byte{}}, {"nil slice", nil}} {
		t.Run(tc.name, func(t *testing.T) {
			if err := m.Set(bg, "empty", tc.v); err != nil {
				t.Fatalf("Set with %s: %v", tc.name, err)
			}
			v, err := m.Get(bg, "empty")
			if err != nil {
				t.Fatalf("a key set to an empty value exists; Get should succeed, got %v", err)
			}
			if len(v) != 0 {
				t.Errorf("Get = %q, want empty", v)
			}
		})
	}
}

func TestL1_1_SetCopiesInput(t *testing.T) {
	m := store.NewMemory()
	buf := []byte("hello")
	if err := m.Set(bg, "k", buf); err != nil {
		t.Fatal(err)
	}
	buf[0] = 'J'
	if got := mustGet(t, m, "k"); got != "hello" {
		t.Errorf("changing the caller's slice after Set changed the stored value to %q. "+
			"Slices share their backing array: copy the bytes you keep.", got)
	}
}

func TestL1_1_GetReturnsACopy(t *testing.T) {
	m := store.NewMemory()
	mustSet(t, m, "k", "hello")
	v, _ := m.Get(bg, "k")
	v[0] = 'J'
	if got := mustGet(t, m, "k"); got != "hello" {
		t.Errorf("changing the slice returned by Get changed the stored value to %q. Return a copy.", got)
	}
	a, _ := m.Get(bg, "k")
	b, _ := m.Get(bg, "k")
	a[1] = 'X'
	if b[1] == 'X' {
		t.Errorf("two Get calls returned slices sharing memory")
	}
}

func TestL1_1_BinaryAndLargeValues(t *testing.T) {
	m := store.NewMemory()
	bin := []byte{0, 1, 2, '\n', 0xff, 0}
	if err := m.Set(bg, "bin", bin); err != nil {
		t.Fatal(err)
	}
	if v, _ := m.Get(bg, "bin"); !bytes.Equal(v, bin) {
		t.Errorf("binary value round trip: got %v, want %v", v, bin)
	}
	big := bytes.Repeat([]byte("burrow"), 200_000)
	if err := m.Set(bg, "big", big); err != nil {
		t.Fatal(err)
	}
	if v, _ := m.Get(bg, "big"); !bytes.Equal(v, big) {
		t.Errorf("a 1.2 MB value didn't round trip intact (got %d bytes)", len(v))
	}
}

func TestL1_1_InvalidKeys(t *testing.T) {
	m := store.NewMemory()
	for _, k := range []string{"", "has space", strings.Repeat("x", 251), "bad\x00"} {
		if err := m.Set(bg, k, []byte("v")); !errors.Is(err, store.ErrInvalidKey) {
			t.Errorf("Set(%q) should fail with ErrInvalidKey, got %v", short(k), err)
		}
		if _, err := m.Get(bg, k); !errors.Is(err, store.ErrInvalidKey) {
			t.Errorf("Get(%q) should fail with ErrInvalidKey (not ErrNotFound), got %v", short(k), err)
		}
		if err := m.Delete(bg, k); !errors.Is(err, store.ErrInvalidKey) {
			t.Errorf("Delete(%q) should fail with ErrInvalidKey, got %v", short(k), err)
		}
	}
	if m.Len() != 0 {
		t.Errorf("rejected Sets must not store anything: Len() = %d", m.Len())
	}
}

func TestL1_1_CancelledContext(t *testing.T) {
	m := store.NewMemory()
	mustSet(t, m, "k", "v")
	ctx, cancel := context.WithCancel(bg)
	cancel()

	if err := m.Set(ctx, "other", []byte("v")); !errors.Is(err, context.Canceled) {
		t.Errorf("Set with a cancelled context should return context.Canceled, got %v", err)
	}
	if _, err := m.Get(ctx, "k"); !errors.Is(err, context.Canceled) {
		t.Errorf("Get with a cancelled context should return context.Canceled, got %v", err)
	}
	if err := m.Delete(ctx, "k"); !errors.Is(err, context.Canceled) {
		t.Errorf("Delete with a cancelled context should return context.Canceled, got %v", err)
	}
	if _, err := m.Get(bg, "other"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a Set with a cancelled context must not store anything")
	}
	if got := mustGet(t, m, "k"); got != "v" {
		t.Errorf("a Delete with a cancelled context must not delete anything")
	}
}

func TestL1_1_ManyKeys(t *testing.T) {
	m := store.NewMemory()
	const n = 10_000
	for i := range n {
		mustSet(t, m, fmt.Sprintf("key-%05d", i), fmt.Sprintf("value-%d", i))
	}
	if m.Len() != n {
		t.Fatalf("Len() = %d, want %d", m.Len(), n)
	}
	for i := 0; i < n; i += 97 {
		k := fmt.Sprintf("key-%05d", i)
		if got := mustGet(t, m, k); got != fmt.Sprintf("value-%d", i) {
			t.Fatalf("Get(%s) = %q", k, got)
		}
	}
	for i := 0; i < n; i += 2 {
		if err := m.Delete(bg, fmt.Sprintf("key-%05d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if m.Len() != n/2 {
		t.Errorf("after deleting half, Len() = %d, want %d", m.Len(), n/2)
	}
}

func TestL1_1_IndependentStores(t *testing.T) {
	a, b := store.NewMemory(), store.NewMemory()
	mustSet(t, a, "k", "a")
	if _, err := b.Get(bg, "k"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("two stores from NewMemory must not share data (package-level map?)")
	}
}
