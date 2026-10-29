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

generics (type parameters, generic structs) · fan-out/fan-in · context cancellation · errors.Join

Standard library: `context`, `sync`, `errors`
