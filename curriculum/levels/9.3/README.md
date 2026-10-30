# 9.3 · Quorum

> Ask five, wait for three, and tell the other two to stop.

## Your goal

Write `quorum.Call`: give it N functions and a number K, and it runs all N at once, returns as soon as K of them have succeeded, and cancels the rest. If so many have failed that K successes can no longer happen, it returns `ErrNoQuorum` right away with every failure attached. *Quorum* is the word for "enough of them to count": a majority of replicas, two out of three acknowledgements. This one function is what a cluster uses for every "wait for a majority" step later on.

It is also your first *generic* function. The functions return some type `T`, and `Call` must work for any `T` without knowing which, so it takes `T` as a type parameter.

## What you're using from before

Nothing from earlier levels. This is a root part, and the only thing it needs is a `context.Context`.

## What's new here

**Type parameters.** A generic function has a second list of parameters in square brackets, for types. `[T any]` says "there is a type `T`, and it can be anything". Inside the function, `T` is a real type you can declare variables of and put in structs; the caller (or the compiler, by inference) picks what it is:

```go
func First[T any](xs []T) (T, bool) {
	var zero T // the zero value of whatever T is
	if len(xs) == 0 {
		return zero, false
	}
	return xs[0], true
}

First([]int{3, 4})        // T inferred as int
First([]string{"a"})      // T inferred as string
```

Structs can be generic too. `Result[T]` below is a struct whose `Value` field is a `T`, and `[]Result[T]` is a slice of them. Writing out `Call[bool](...)` is allowed and sometimes clearer than letting the compiler infer.

**Constraints.** The word after the type parameter is its *constraint*, an interface that says what `T` must be able to do. `any` allows everything but then you can do nothing with a `T` except move it around, which is all `Call` needs. The others you will meet:

- `comparable` is built in: anything that works with `==` and as a map key. `func Dedup[K comparable](xs []K) []K`.
- `cmp.Ordered` (package `cmp`) is every type `<` works on: the integer and float kinds and `string`. `func Max[N cmp.Ordered](a, b N) N`.
- A constraint can list types with `|`, and `~T` means "any type whose *underlying* type is T". `cmp.Ordered` is written with `~`, so a named `type Term uint64` satisfies it, while a constraint of plain `uint64` would reject it. That matters as soon as you name your integer types, which a cluster does a lot.

`slices.Sort`, `slices.Max` and `maps.Keys` are all written this way. Read their signatures in the docs; they will look familiar now. Try one on the side if you like: `func Majority[N cmp.Ordered](votes []N, need N) bool`.

**Fan-out and fan-in.** Fan-out: start a goroutine per function. Fan-in: every goroutine sends its result on one channel, and the caller reads from that channel in a loop. The channel is buffered to N so a goroutine that finishes after `Call` has already returned can still send and exit instead of blocking forever:

```go
results := make(chan Result[T], len(fns))
for i, fn := range fns {
	go func() {
		v, err := fn(ctx)
		results <- Result[T]{Index: i, Value: v, Err: err}
	}()
}
```

**Cancelling the losers.** `ctx, cancel := context.WithCancel(ctx)` at the top and `defer cancel()` mean that the moment `Call` returns, every function still running sees `ctx.Done()`. They must honour it, as every function you've written since 1.1 does. This is why early return doesn't leak goroutines: the losers wake up, return, send into the buffered channel, and exit.

**`errors.Join`.** Joins any number of errors into one whose message lists them all, and `errors.Is` and `errors.As` look inside it. `ErrNoQuorum` is returned wrapped together with every failure, so a caller can both match the sentinel and read what went wrong:

```go
err := errors.Join(ErrNoQuorum, failures...)
errors.Is(err, ErrNoQuorum) // true
fmt.Println(err)            // one line per error
```

## What to do

1. Create `quorum/quorum.go`, `package quorum`, with `Result[T]`, `ErrNoQuorum` and `Call`.
2. At the top of `Call`: derive a cancellable context and `defer cancel()`. Handle the edges: `k <= 0` returns an empty slice and nil at once; `k > len(fns)` returns `ErrNoQuorum` at once.
3. Make the results channel with buffer `len(fns)` and start one goroutine per function.
4. Loop reading results. Keep two slices: successes and failures. After each one:
   - `len(successes) == k`: return the successes (in completion order), nil.
   - `len(fns) - len(failures) < k`: K successes are now impossible. Return nil and `errors.Join(ErrNoQuorum, failures...)`.
   - `ctx.Done()` in the same `select`: return nil, `ctx.Err()`.
5. Do not wait for the losers. The buffer and the cancel are what let them finish on their own.
6. Prove two instantiations compile and run: `Call[bool]` and `Call[store.Entry]`. Then run with `-race` and `testkit.NoLeaks` across 1,000 rounds.

## Example

Five replicas, three fast and two slow; K is 3:

```go
type fn = func(context.Context) (string, error)

replica := func(d time.Duration, v string) fn {
	return func(ctx context.Context) (string, error) {
		select {
		case <-time.After(d):
			return v, nil
		case <-ctx.Done():
			fmt.Println(v, "cancelled")
			return "", ctx.Err()
		}
	}
}
fns := []fn{
	replica(10*time.Millisecond, "a"),
	replica(20*time.Millisecond, "b"),
	replica(30*time.Millisecond, "c"),
	replica(5*time.Second, "d"),
	replica(5*time.Second, "e"),
}
start := time.Now()
rs, err := quorum.Call(ctx, 3, fns)
fmt.Println(len(rs), err, time.Since(start).Round(10*time.Millisecond))
// 3 <nil> 30ms
// d cancelled
// e cancelled
for _, r := range rs {
	fmt.Print(r.Index, ":", r.Value, " ")   // 0:a 1:b 2:c
}
```

Three failures out of five, so three successes are impossible: it does not wait for the slow ones.

```go
fail := func(ctx context.Context) (string, error) {
	return "", errors.New("down")
}
fns = []fn{
	fail, fail, fail,
	replica(5*time.Second, "d"), replica(5*time.Second, "e"),
}
rs, err = quorum.Call(ctx, 3, fns)
fmt.Println(rs == nil, errors.Is(err, quorum.ErrNoQuorum))
// true true, within a millisecond
```

## Done when

- [ ] With mixed delays, `Call` returns the first K successes in completion order as soon as the K-th arrives
- [ ] Once K successes are impossible, `Call` returns `ErrNoQuorum` immediately, joined with every failure
- [ ] Functions still running when `Call` returns see `ctx.Done()`
- [ ] `k <= 0` returns at once with no error; `k > len(fns)` returns `ErrNoQuorum` at once
- [ ] The goroutine count is flat after 1,000 rounds
- [ ] `Call[bool]` and `Call[store.Entry]` both compile and work

## Reference

```go
package quorum

type Result[T any] struct {
	Index int
	Value T
	Err   error
}

var ErrNoQuorum = errors.New("quorum: not enough successes")

// Call runs each fn in its own goroutine with a ctx cancelled when Call
// returns. It returns the first k successful results (in completion
// order) as soon as there are k, or ErrNoQuorum with all errors joined
// once k successes are impossible, or ctx.Err().
func Call[T any](ctx context.Context, k int,
	fns []func(ctx context.Context) (T, error)) ([]Result[T], error)
```

- `Call[T]` with a results channel buffered to `len(fns)`, so late replies never block a goroutine
- Return as soon as there are `k` successes, or as soon as `len(fns) - failures < k`
- `defer cancel()` in `Call`; the losers' functions must honour `ctx` for this to work
- `errors.Join` of the failures, together with `ErrNoQuorum`

## The tests check

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/9.3/files/`, matching the earlier chapters.

Pass when: 3 of 5 slow-fast mixed calls return as soon as 3 succeed, the two slow ones observe cancellation, 3 failures out of 5 give ErrNoQuorum immediately, and the goroutine count is flat after 1,000 rounds.

The suite will cover:

- First k of n with mixed delays
- Early ErrNoQuorum once impossible
- Losers see ctx.Done()
- k=0, k>n edge cases
- No leaks
- Two instantiations compile: Call[bool] and Call[store.Entry]

## Try it

```bash
./burrow watch
go test -race -run . ./quorum
# the example above as a scratch main; change k and the delays and watch
# the timing and the "cancelled" lines follow
```

## In the wild

golang.org/x/sync/errgroup (with a twist); Cassandra's quorum coordinator; this is why Go added generics.

## New in Go

generics (type parameters, generic structs) · constraints: any, comparable, cmp.Ordered and ~T · fan-out/fan-in · context cancellation · errors.Join

Standard library: `context`, `sync`, `errors`, `cmp`
