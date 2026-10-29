# 0.2 · Test Bench

Every key in Burrow passes through one validator. Write it, and learn Go's testing loop and error wrapping on the way.

## Build

```go
// store/validate.go
package store

var ErrInvalidKey = errors.New("invalid key")

// ValidateKey returns nil for a usable key, or an error wrapping ErrInvalidKey.
func ValidateKey(key string) error
```

A valid key is:

- non-empty
- at most **250 bytes** (bytes, not characters: `é` is 2 bytes)
- valid UTF-8
- free of whitespace (any `unicode.IsSpace` rune, including U+00A0 and U+2003) and control characters (`unicode.IsControl`, including NUL and DEL)

Also write your own table-driven test in `store/validate_test.go`. It isn't graded, but you'll use the pattern in every level.

## The tests check

- 8 valid and 17 invalid keys, including the 250/251-byte boundary measured in bytes.
- Every rejection matches `errors.Is(err, store.ErrInvalidKey)`.
- The error isn't the bare sentinel: it adds context and quotes the key with `%q`, so invisible characters show up.
- A 5000-byte key produces a short error message (under 400 bytes) that mentions the length, 5000.

## Try it

```bash
go test -v -run TestL0_2 ./store/
```

## New in Go

structs · slices · `for range` over strings (runes vs bytes) · sentinel errors · `%w` wrapping · table-driven tests · subtests
