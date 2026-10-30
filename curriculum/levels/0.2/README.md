# 0.2 · Test Bench

> Before you build the store, build the bench you'll test it on. The first thing clamped to it is the rule for what a key may look like.

## Your goal

Write `store.ValidateKey`, one function that decides whether a string is allowed to be a key in Burrow. It lives in a new package, `store`, which is where the key-value store itself will go from 1.1 onwards. Every engine and protocol you build later calls this one function, so it has to be exact: at most 250 bytes, valid UTF-8, no whitespace, no control characters.

The function is small. The real lesson is Go's testing loop: how a test file is laid out, how one test covers a whole table of cases, how errors carry a reusable identity, and why a test that can never fail is worthless. The level checks that last point by planting bugs in your code and expecting your own tests to notice.

## What you're using from before

Nothing yet. This is a root part: it stands on its own and does not touch `burrowd` from 0.1.

## What's new here

**Packages and test files.** A Go package is a directory. Every `.go` file in `store/` starts with `package store` and they all share one namespace. Files ending in `_test.go` are compiled only by `go test`. A test file can declare `package store` (it sees unexported names) or `package store_test` (it imports `burrow/store` like any outside caller would). The provided tests use `store_test`; either is fine for yours.

**Sentinel errors and wrapping.** In Go an error is just a value with an `Error() string` method. A *sentinel* is an error value declared once at package level so callers can compare against it:

```go
var ErrInvalidKey = errors.New("store: invalid key")
```

Returning the bare sentinel loses detail, so you *wrap* it with `fmt.Errorf` and the `%w` verb. The result is a new error whose message has context and which still "is" the sentinel underneath:

```go
err := fmt.Errorf("%w: %q", ErrInvalidKey, "user 42")
err.Error()                   // store: invalid key: "user 42"
errors.Is(err, ErrInvalidKey) // true: errors.Is sees through %w
err == ErrInvalidKey          // false: it's a different value
```

`%q` quotes the string with Go escapes, so a tab shows up as `\t` and a stray byte as `\xff` instead of being invisible in the message.

**Strings are bytes; `range` gives you runes.** A Go string is an immutable slice of bytes, usually UTF-8. `len(s)` is the byte count, not the character count, which is exactly what the 250-byte limit is in. A `for range` loop over a string decodes one UTF-8 character (a `rune`, Go's name for a Unicode code point) per iteration:

```go
s := "héllo"
len(s)                       // 6: é is two bytes
for i, r := range s {        // i is the byte offset, r is the rune
    fmt.Println(i, string(r))
}
// 0 h  1 é  3 l  4 l  5 o   (note the jump from 1 to 3)
utf8.ValidString("a\xffb")   // false: 0xff can't start a UTF-8 sequence
unicode.IsSpace(' ')         // true, and so is U+00A0, U+2003, \t, \n
unicode.IsControl('\x00')    // true, and so is DEL (0x7f) and BEL (\a)
```

**Slices of structs for test tables.** A slice is Go's growable list, `[]T`. A struct is a record with named fields, and you can declare one inline without naming the type. Put them together and you get a *table-driven test*: a slice of cases, one loop, one assertion. Each case runs as a *subtest* through `t.Run`, so failures are reported by name and you can run one with `go test -run 'TestDouble/zero'`:

```go
func TestDouble(t *testing.T) {
    cases := []struct {
        name string
        in   int
        want int
    }{
        {"zero", 0, 0},
        {"positive", 3, 6},
        {"negative", -2, -4},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            if got := double(tc.in); got != tc.want {
                t.Errorf("double(%d) = %d, want %d",
                    tc.in, got, tc.want)
            }
        })
    }
}
```

`t.Errorf` records a failure and carries on; `t.Fatalf` records one and stops that test. Since Go 1.22 each loop iteration gets its own copy of `tc`, so the closure passed to `t.Run` sees the right case even if subtests run in parallel. In older code you'll see `tc := tc` at the top of the loop body doing the same job by hand.

**Building test inputs.** `strings.Repeat("k", 250)` makes a 250-byte key. `for i := range 250` counts `0..249` (ranging over an int is Go 1.22 too). When you quote only part of a long key, the `min` builtin keeps you inside the string: `key[:min(len(key), 20)]`.

**Testable examples.** A function named `ExampleValidateKey` in a `_test.go` file is both documentation and a test. `go doc` shows it next to the function, and `go test` runs it and compares what it printed with the `// Output:` comment at the end. Without that comment it is compiled but never run:

```go
func ExampleDouble() {
    fmt.Println(double(21))
    // Output: 42
}
```

**Mutation testing, or why a test must be able to fail.** A test suite that passes is only evidence if it would have failed on wrong code. The provided `TestL0_2_YourTestsCatchBugs` checks that directly: it copies the module to a temp dir, swaps your `ValidateKey` for a version with one planted bug (a *mutant*), runs only your tests, and expects at least one to fail. It does that five times, once per bug. If your tests pass against a mutant, they weren't testing that rule.

## What to do

1. Create `store/validate.go` with `package store`, the `ErrInvalidKey` sentinel and `ValidateKey(key string) error`.
2. Make `ValidateKey` return `nil` for a usable key and a wrapped `ErrInvalidKey` otherwise. A key is usable when all of these hold:
   - it is not empty
   - `len(key)` is at most 250 (bytes, not characters)
   - `utf8.ValidString(key)` is true
   - no rune in it satisfies `unicode.IsSpace` or `unicode.IsControl`
3. Put the key in every error message with `%q`. For an over-long key, quote only a prefix and state the length in bytes, so a 5000-byte key doesn't produce a 5000-byte message.
4. Create your own test file in `store/` (`store/validate_test.go` is a good name) with table-driven tests that use `t.Run` and check rejections with `errors.Is`. Name them anything that doesn't start with `TestL` plus a digit or capital letter; that prefix is reserved for the provided suites. Cover at least:
   - a key of exactly 250 bytes (valid) and 251 bytes (invalid)
   - a key containing a tab
   - invalid UTF-8, such as `"a\xffb"`
   - 126 two-byte runes (`strings.Repeat("é", 126)` is 252 bytes, invalid)
5. Add `func ExampleValidateKey()` to a test file in `store/`, ending with a `// Output:` comment that matches what it prints.
6. Keep `gofmt -l .` and `go vet ./...` quiet.

## Example

Calling it:

```go
store.ValidateKey("user:42")             // nil
store.ValidateKey("日本語のキー")           // nil (18 bytes, all fine)
store.ValidateKey("")                    // error
store.ValidateKey("user 42")             // error, quotes "user 42"
store.ValidateKey("a\tb")                // error, message shows "a\tb"
store.ValidateKey(strings.Repeat("k", 251)) // error, mentions 251

err := store.ValidateKey("bad key")
errors.Is(err, store.ErrInvalidKey)      // true
```

A test run where your own tests don't yet cover the tab rule:

```bash
$ go test ./store/
--- FAIL: TestL0_2_YourTestsCatchBugs (4.21s)
    your tests don't catch this bug: ValidateKey accepts a key
    containing a tab
FAIL
FAIL    burrow/store    4.512s
```

Add a `{"tab", "a\tb"}` row to your invalid table and the mutant falls. When everything passes:

```bash
$ go test ./store/
ok      burrow/store    4.498s
$ go doc ./store ValidateKey
func ValidateKey(key string) error
    ValidateKey returns nil for a usable key, or an error wrapping
    ErrInvalidKey ...
```

## Done when

- [ ] The 8 valid and 17 invalid keys in the provided tests are classified correctly
- [ ] Every rejection satisfies `errors.Is(err, store.ErrInvalidKey)` and is not the bare sentinel
- [ ] Error messages quote the key with `%q`; a 5000-byte key gives a message under 400 bytes that mentions `5000`
- [ ] Your own tests in `store/` use `t.Run`, pass on your code, and fail against each of the five planted bugs
- [ ] `ExampleValidateKey` exists in `store/`, ends with `// Output:` and passes
- [ ] `gofmt -l .` and `go vet ./...` print nothing

## Reference

```go
// store/validate.go
package store

var ErrInvalidKey = errors.New("store: invalid key")

// ValidateKey returns nil for a usable key, or an error wrapping
// ErrInvalidKey that quotes the key with %q and, for long keys, states
// the length. Valid: non-empty, at most 250 bytes, valid UTF-8, no
// unicode.IsSpace or unicode.IsControl runes. Keys therefore never
// contain newlines or spaces, which later protocols rely on.
func ValidateKey(key string) error
```

Your tests are graded by mutation. The provided `TestL0_2_YourTestsCatchBugs` copies the module to a temp dir, swaps a deliberately broken `ValidateKey` in for yours, runs only your tests, and expects at least one of them to fail. It does this once per planted bug. A test suite that passes on every mutant is not testing anything.

Decisions the tests rely on:

- Your tests live under `store/` (package `store` or `store_test`, your choice) and their names must not start with `TestL` followed by a digit or a capital letter: that prefix is reserved for the provided suites, and the mutation run excludes it with `-skip`. `TestValidateKey`, `TestKeyLength` and friends are fine.
- Your tests pass on your real `ValidateKey`, and at least one of them uses `t.Run`.
- Against each of these five mutants at least one of your tests fails: (1) a 251-byte key is accepted, (2) a key of exactly 250 bytes is rejected, (3) a key containing a tab is accepted, (4) invalid UTF-8 is accepted, (5) the limit is counted in runes instead of bytes, so 126 two-byte runes (252 bytes) are accepted. Cover each case in your table and the mutants fall.
- `ExampleValidateKey` sits in `store/` too, ends with a `// Output:` comment and passes. An example without an `Output` comment is compiled but never run.

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
./burrow watch                             # re-runs the tests on save
go test -v -run TestL0_2 ./store/
go test -v -skip 'TestL[0-9A-Z]' ./store/  # just yours, like the grader
go doc -all ./store ValidateKey            # your example shows up here
```

## In the wild

go test and the testing package are the whole story; testify and go-cmp are common add-ons, neither is needed

## New in Go

packages and _test.go files · structs · slices · for range over strings (runes vs bytes) · for range over int · per-iteration loop variables (Go 1.22) · the min builtin · sentinel errors · %w wrapping · errors.Is · table-driven tests · subtests · testable examples · mutation testing (why a test must be able to fail)

Standard library: `testing`, `errors`, `fmt`, `strings`, `unicode`, `unicode/utf8`
