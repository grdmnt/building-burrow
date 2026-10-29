# 0.2 · Test Bench

Every key in Burrow passes through one validator. Write it, and learn Go's testing loop and error wrapping on the way. Because valid keys never contain whitespace, the wire protocol (2.2) and the HTTP API (6.3) can lean on this rule later.

## Build

```go
// store/validate.go
package store

var ErrInvalidKey = errors.New("store: invalid key")

// ValidateKey returns nil for a usable key, or an error wrapping ErrInvalidKey
// that quotes the key with %q and, for long keys, states the length.
// Valid: non-empty, at most 250 bytes, valid UTF-8, no unicode.IsSpace or
// unicode.IsControl runes. Keys therefore never contain newlines or spaces,
// which the wire protocol (2.2) and the HTTP API (6.3) rely on.
func ValidateKey(key string) error
```

- `store.ValidateKey(key string) error`
- `var ErrInvalidKey`, wrapped with context using `%w`
- Your own table-driven test in `store/validate_test.go` (ungraded; the pattern is used in every level)

## The tests check

- Valid: single byte, typical `user:42`, punctuation, digits, exactly 250 bytes, multibyte runes, 125 two-byte runes, emoji
- Invalid: empty, 251 bytes, 126 two-byte runes (252 bytes), 10,000 bytes, inner/leading/trailing space, tab, LF, CR, U+00A0, U+2003, NUL, DEL, BEL, invalid UTF-8, truncated UTF-8 sequence
- Every rejection satisfies errors.Is(err, ErrInvalidKey) and is not the bare sentinel
- The message contains the key quoted with %q so invisible characters show
- A 5000-byte key produces a message under 400 bytes that mentions 5000

## Try it

```bash
go test -v -run TestL0_2 ./store/
```

## Uses

Nothing from earlier levels. This is a root part.

## In the wild

go test and the testing package are the whole story; testify and go-cmp are common add-ons, neither is needed

## New in Go

structs · slices · for range over strings (runes vs bytes) · sentinel errors · %w wrapping · table-driven tests · subtests

Standard library: `testing`, `errors`, `fmt`, `unicode`, `unicode/utf8`
