package store_test

import (
	"errors"
	"strings"
	"testing"

	"burrow/store"
)

func TestL0_2_ValidKeys(t *testing.T) {
	valid := []struct {
		name string
		key  string
	}{
		{"single byte", "a"},
		{"typical", "user:42"},
		{"punctuation", "a.b-c_d/e:f@g#h"},
		{"digits only", "12345"},
		{"exactly 250 bytes", strings.Repeat("k", 250)},
		{"multibyte runes", "日本語のキー"},
		{"125 two-byte runes is 250 bytes", strings.Repeat("é", 125)},
		{"emoji", "🦫"},
	}
	for _, tc := range valid {
		t.Run(tc.name, func(t *testing.T) {
			if err := store.ValidateKey(tc.key); err != nil {
				t.Errorf("ValidateKey(%q) should be valid, got error: %v", short(tc.key), err)
			}
		})
	}
}

func TestL0_2_InvalidKeys(t *testing.T) {
	invalid := []struct {
		name string
		key  string
	}{
		{"empty", ""},
		{"251 bytes", strings.Repeat("k", 251)},
		{"126 two-byte runes is 252 bytes", strings.Repeat("é", 126)},
		{"way too long", strings.Repeat("k", 10_000)},
		{"inner space", "a b"},
		{"leading space", " a"},
		{"trailing space", "a "},
		{"tab", "a\tb"},
		{"newline", "a\nb"},
		{"carriage return", "a\rb"},
		{"no-break space U+00A0", "a b"},
		{"em space U+2003", "a b"},
		{"NUL byte", "a\x00b"},
		{"DEL control char", "a\x7fb"},
		{"bell control char", "a\ab"},
		{"invalid UTF-8", "a\xffb"},
		{"truncated UTF-8 sequence", "a\xe6\x97"},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			err := store.ValidateKey(tc.key)
			if err == nil {
				t.Fatalf("ValidateKey(%q) should be rejected, got nil", short(tc.key))
			}
			if !errors.Is(err, store.ErrInvalidKey) {
				t.Errorf("the error should wrap store.ErrInvalidKey so callers can use errors.Is, got %v", err)
			}
		})
	}
}

func TestL0_2_ErrorHasContext(t *testing.T) {
	err := store.ValidateKey("bad key")
	if err == nil {
		t.Fatal("expected an error for a key with a space")
	}
	if err == store.ErrInvalidKey {
		t.Errorf("return a wrapped error with context (fmt.Errorf with %%w), not the bare sentinel")
	}
	if !strings.Contains(err.Error(), `"bad key"`) {
		t.Errorf("the message should quote the key with %%q so invisible characters show up, got %q", err.Error())
	}
}

func TestL0_2_LongKeyErrorIsReadable(t *testing.T) {
	err := store.ValidateKey(strings.Repeat("x", 5000))
	if err == nil {
		t.Fatal("expected an error for a 5000-byte key")
	}
	if len(err.Error()) > 400 {
		t.Errorf("don't dump a 5000-byte key into the error message: it's %d bytes long. Truncate what you quote.", len(err.Error()))
	}
	if !strings.Contains(err.Error(), "5000") {
		t.Errorf("for an over-long key, say how long it was (5000 bytes), got %q", err.Error())
	}
}

func TestL0_2_SentinelIdentity(t *testing.T) {
	if store.ErrInvalidKey == nil {
		t.Fatal("store.ErrInvalidKey must be a non-nil error value")
	}
	a := store.ValidateKey("")
	b := store.ValidateKey(" ")
	if !errors.Is(a, store.ErrInvalidKey) || !errors.Is(b, store.ErrInvalidKey) {
		t.Errorf("every rejection must wrap the same sentinel")
	}
}

func short(s string) string {
	if len(s) > 40 {
		return s[:20] + "…(" + itoa(len(s)) + " bytes)"
	}
	return s
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
