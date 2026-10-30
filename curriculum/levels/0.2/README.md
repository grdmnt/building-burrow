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
- Your own table-driven tests in `store/` (`store/validate_test.go` is a good name), using `t.Run` subtests and `errors.Is`; they are graded by mutation, see below
- An `ExampleValidateKey` with a `// Output:` comment, so `go doc` shows a worked example and `go test` checks it

Your tests are graded by mutation. The provided `TestL0_2_YourTestsCatchBugs` copies the module to a temp dir, swaps a deliberately broken `ValidateKey` in for yours, runs only your tests, and expects at least one of them to fail. It does this once per planted bug. A test suite that passes on every mutant is not testing anything.

Decisions the tests rely on:

- Your tests live under `store/` (package `store` or `store_test`, your choice) and their names must not start with `TestL` followed by a digit or a capital letter: that prefix is reserved for the provided suites, and the mutation run excludes it with `-skip`. `TestValidateKey`, `TestKeyLength` and friends are fine.
- Your tests pass on your real `ValidateKey`, and at least one of them uses `t.Run`.
- Against each of these five mutants at least one of your tests fails: (1) a 251-byte key is accepted, (2) a key of exactly 250 bytes is rejected, (3) a key containing a tab is accepted, (4) invalid UTF-8 is accepted, (5) the limit is counted in runes instead of bytes, so 126 two-byte runes (252 bytes) are accepted. Cover each case in your table and the mutants fall.
- `ExampleValidateKey` sits in `store/` too, ends with a `// Output:` comment and passes. An example without an `Output` comment is compiled but never run.

Worth knowing now:

- Since Go 1.22 each iteration of a `for` loop gets its own copy of the loop variable, so `t.Run(tc.name, func(t *testing.T) { ... tc ... })` is safe, even with `t.Parallel()` inside. Older Go needed `tc := tc`; you will still meet that line in code written before 1.22.
- `for i := range 250` iterates `0..249`; `strings.Repeat("a", 250)` is the other way to build a 250-byte key. The builtins `min`, `max` and `clear` (Go 1.21) remove three tiny helpers you would otherwise write.

## The tests check

- Valid: single byte, typical `user:42`, punctuation, digits, exactly 250 bytes, multibyte runes, 125 two-byte runes, emoji
- Invalid: empty, 251 bytes, 126 two-byte runes (252 bytes), 10,000 bytes, inner/leading/trailing space, tab, LF, CR, U+00A0, U+2003, NUL, DEL, BEL, invalid UTF-8, truncated UTF-8 sequence
- Every rejection satisfies errors.Is(err, ErrInvalidKey) and is not the bare sentinel
- The message contains the key quoted with %q so invisible characters show
- A 5000-byte key produces a message under 400 bytes that mentions 5000
- TestL0_2_YourTestsCatchBugs: your tests pass on your code, use at least one t.Run, and fail against each of the five mutants above (251 bytes accepted, exactly 250 rejected, tab accepted, invalid UTF-8 accepted, runes counted instead of bytes)
- TestL0_2_Example: an ExampleValidateKey with a `// Output:` comment exists under store/ and passes

## Try it

```bash
go test -v -run TestL0_2 ./store/
go test -v -skip 'TestL[0-9A-Z]' ./store/      # just yours, the way the mutation run selects them
go doc -all ./store ValidateKey                 # your example shows up here
```

## Uses

Nothing from earlier levels. This is a root part.

## In the wild

go test and the testing package are the whole story; testify and go-cmp are common add-ons, neither is needed

## New in Go

structs · slices · for range over strings (runes vs bytes) · for range over int · per-iteration loop variables (Go 1.22) · min, max and clear builtins · sentinel errors · %w wrapping · errors.Is · table-driven tests · subtests · testable examples · mutation testing (why a test must be able to fail)

Standard library: `testing`, `errors`, `fmt`, `strings`, `unicode`, `unicode/utf8`
