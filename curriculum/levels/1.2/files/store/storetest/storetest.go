// Package storetest is a conformance suite that every Burrow storage engine
// must pass. It's provided by the curriculum: you don't edit it, you make
// your engines pass it.
//
//	func TestMyEngine(t *testing.T) {
//		storetest.Run(t, func(t *testing.T) storetest.Store { return NewMyEngine() })
//	}
package storetest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"burrow/store"
)

// Store is the subset of behaviour the suite checks. Your store.Store
// interface should include at least these methods.
type Store interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte) error
	Delete(ctx context.Context, key string) error
	Keys(ctx context.Context, prefix string) ([]string, error)
}

// Factory returns a new, empty store. Use t.Cleanup for teardown.
type Factory func(t *testing.T) Store

// Run checks the basic contract every engine must meet.
func Run(t *testing.T, newStore Factory) {
	ctx := context.Background()

	t.Run("SetGet", func(t *testing.T) {
		s := newStore(t)
		must(t, s.Set(ctx, "a", []byte("1")))
		expect(t, s, "a", "1")
	})

	t.Run("MissingIsErrNotFound", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.Get(ctx, "missing"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("Get(missing) should match store.ErrNotFound, got %v", err)
		}
		if err := s.Delete(ctx, "missing"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("Delete(missing) should match store.ErrNotFound, got %v", err)
		}
	})

	t.Run("OverwriteAndDelete", func(t *testing.T) {
		s := newStore(t)
		must(t, s.Set(ctx, "k", []byte("one")))
		must(t, s.Set(ctx, "k", []byte("two")))
		expect(t, s, "k", "two")
		must(t, s.Delete(ctx, "k"))
		if _, err := s.Get(ctx, "k"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("Get after Delete should be ErrNotFound, got %v", err)
		}
		must(t, s.Set(ctx, "k", []byte("three")))
		expect(t, s, "k", "three")
	})

	t.Run("EmptyValue", func(t *testing.T) {
		s := newStore(t)
		must(t, s.Set(ctx, "e", nil))
		v, err := s.Get(ctx, "e")
		if err != nil || len(v) != 0 {
			t.Errorf("a key set to an empty value exists: Get = (%q, %v), want (empty, nil)", v, err)
		}
	})

	t.Run("BinaryValue", func(t *testing.T) {
		s := newStore(t)
		val := []byte{0, '\n', '\r', 0xff, 0xfe, 0, 'x'}
		must(t, s.Set(ctx, "bin", val))
		got, err := s.Get(ctx, "bin")
		if err != nil || !bytes.Equal(got, val) {
			t.Errorf("binary round trip: got (%v, %v), want %v", got, err, val)
		}
	})

	t.Run("NoAliasing", func(t *testing.T) {
		s := newStore(t)
		in := []byte("hello")
		must(t, s.Set(ctx, "k", in))
		in[0] = 'X'
		out, _ := s.Get(ctx, "k")
		if string(out) != "hello" {
			t.Fatalf("mutating the slice passed to Set changed the stored value to %q", out)
		}
		out[0] = 'Y'
		expect(t, s, "k", "hello")
	})

	t.Run("InvalidKey", func(t *testing.T) {
		s := newStore(t)
		for _, k := range []string{"", "a b", strings.Repeat("x", 251)} {
			if err := s.Set(ctx, k, []byte("v")); !errors.Is(err, store.ErrInvalidKey) {
				t.Errorf("Set(%.20q) should be ErrInvalidKey, got %v", k, err)
			}
			if _, err := s.Get(ctx, k); !errors.Is(err, store.ErrInvalidKey) {
				t.Errorf("Get(%.20q) should be ErrInvalidKey, got %v", k, err)
			}
			if err := s.Delete(ctx, k); !errors.Is(err, store.ErrInvalidKey) {
				t.Errorf("Delete(%.20q) should be ErrInvalidKey, got %v", k, err)
			}
		}
		keys, err := s.Keys(ctx, "")
		if err != nil || len(keys) != 0 {
			t.Errorf("rejected writes must not store anything; Keys = %v, %v", keys, err)
		}
	})

	t.Run("CancelledContext", func(t *testing.T) {
		s := newStore(t)
		must(t, s.Set(ctx, "k", []byte("v")))
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		if err := s.Set(cctx, "x", []byte("v")); !errors.Is(err, context.Canceled) {
			t.Errorf("Set with cancelled ctx should be context.Canceled, got %v", err)
		}
		if _, err := s.Get(cctx, "k"); !errors.Is(err, context.Canceled) {
			t.Errorf("Get with cancelled ctx should be context.Canceled, got %v", err)
		}
		if err := s.Delete(cctx, "k"); !errors.Is(err, context.Canceled) {
			t.Errorf("Delete with cancelled ctx should be context.Canceled, got %v", err)
		}
		if _, err := s.Keys(cctx, ""); !errors.Is(err, context.Canceled) {
			t.Errorf("Keys with cancelled ctx should be context.Canceled, got %v", err)
		}
		expect(t, s, "k", "v")
	})

	t.Run("KeysSortedAndFiltered", func(t *testing.T) {
		s := newStore(t)
		for _, k := range []string{"user:2", "order:1", "user:10", "user:1", "users", "u", "日本"} {
			must(t, s.Set(ctx, k, []byte("v")))
		}
		cases := []struct {
			prefix string
			want   []string
		}{
			{"", []string{"order:1", "u", "user:1", "user:10", "user:2", "users", "日本"}},
			{"user:", []string{"user:1", "user:10", "user:2"}},
			{"user", []string{"user:1", "user:10", "user:2", "users"}},
			{"u", []string{"u", "user:1", "user:10", "user:2", "users"}},
			{"users", []string{"users"}},
			{"usersx", nil},
			{"日", []string{"日本"}},
			{"zzz", nil},
		}
		for _, c := range cases {
			got, err := s.Keys(ctx, c.prefix)
			if err != nil {
				t.Errorf("Keys(%q): %v", c.prefix, err)
				continue
			}
			if len(got) == 0 && len(c.want) == 0 {
				continue
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("Keys(%q) = %q, want %q (sorted by byte order, prefix match)", c.prefix, got, c.want)
			}
		}
	})

	t.Run("KeysReflectDeletes", func(t *testing.T) {
		s := newStore(t)
		for i := range 5 {
			must(t, s.Set(ctx, fmt.Sprintf("k%d", i), []byte("v")))
		}
		must(t, s.Delete(ctx, "k2"))
		got, _ := s.Keys(ctx, "k")
		if want := []string{"k0", "k1", "k3", "k4"}; !slices.Equal(got, want) {
			t.Errorf("Keys after delete = %q, want %q", got, want)
		}
	})

	t.Run("KeysIsASnapshot", func(t *testing.T) {
		s := newStore(t)
		must(t, s.Set(ctx, "a", []byte("1")))
		must(t, s.Set(ctx, "b", []byte("2")))
		got, _ := s.Keys(ctx, "")
		must(t, s.Set(ctx, "c", []byte("3")))
		if len(got) != 2 {
			t.Errorf("a slice returned by Keys must not change after later writes: %q", got)
		}
	})
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func expect(t *testing.T, s Store, key, want string) {
	t.Helper()
	got, err := s.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get(%q): unexpected error %v", key, err)
	}
	if string(got) != want {
		t.Errorf("Get(%q) = %q, want %q", key, got, want)
	}
}
