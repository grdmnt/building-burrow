# 9.3 · Quorum

Generics where two instantiations exist. `Call[T]` fans out N functions, returns at the first K successes, cancels the rest and leaks nothing. It will count votes, acks, read-index confirmations and replica reads.

## Build

```go
package quorum

type Result[T any] struct { Index int; Value T; Err error }
// Call runs each fn in its own goroutine with a ctx cancelled when Call returns. It returns the
// first k successful results (in completion order) as soon as there are k, or ErrNoQuorum
// with all errors joined once k successes are impossible, or ctx.Err().
func Call[T any](ctx context.Context, k int, fns []func(ctx context.Context) (T, error)) ([]Result[T], error)
var ErrNoQuorum = errors.New("quorum: not enough successes")
```

- `Call[T]` with a buffered results channel
- Early return and cancellation
- errors.Join of failures

Constraints, since `any` is only the first one you will reach for: a type parameter's constraint is an interface, and the interface can list types as well as methods. `comparable` is the built-in constraint for anything usable as a map key or with `==`; `cmp.Ordered` (package `cmp`, Go 1.21) is `~int | ~int8 | ... | ~float64 | ~string`, the types `<` works on; and the `~T` in those unions means "any type whose underlying type is T", so a `type Term uint64` satisfies `~uint64` while a plain `uint64` constraint would reject it. Try it on the side: `func Majority[N cmp.Ordered](votes []N, need N) bool` or a `Set[K comparable]`. Raft's terms and indexes are exactly the kind of named integer types `~` exists for. `slices.Sort`, `slices.Max` and `maps.Keys` are all written this way; read their signatures.

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/9.3/files/`, matching the earlier chapters.

## Pass when

3 of 5 slow-fast mixed calls return as soon as 3 succeed, the two slow ones observe cancellation, 3 failures out of 5 give ErrNoQuorum immediately, and the goroutine count is flat after 1,000 rounds.

The suite will cover:

- First k of n with mixed delays
- Early ErrNoQuorum once impossible
- Losers see ctx.Done()
- k=0, k>n edge cases
- No leaks
- Two instantiations compile: Call[bool] and Call[store.Entry]

## Uses

Nothing from earlier levels. This is a root part.

## In the wild

golang.org/x/sync/errgroup (with a twist); Cassandra's quorum coordinator; this is why Go added generics

## New in Go

generics (type parameters, generic structs) · constraints: any, comparable, cmp.Ordered and ~T · fan-out/fan-in · context cancellation · errors.Join

Standard library: `context`, `sync`, `errors`, `cmp`
