# P.5 · Generics

> Some parts don't care what flows through them. A pipe is a pipe, whether it carries water or oil.

## Your goal

Write package `primer/generic`: functions and a type that work for many element types while the compiler still checks every use. `Index` finds a value in a slice of anything comparable, `Map` transforms every element of a slice, `Max` finds the largest of anything orderable, and `Largest` finds the biggest of any kind of P.4 shape and hands it back as that kind. Then `Set[T]`, a generic type: a collection of distinct values of whatever type you ask for. P.6's crawler uses a `Set[string]` to remember which URLs it has already taken.

This level follows the Tour of Go section "Generics", which has two slides: type parameters and generic types. The standard library's `slices` and `maps` packages are built from exactly these pieces, so after this level you can read their source.

## What you're using from before

**Interfaces kit (P.4 · Methods and Interfaces).** `Largest` uses your `shapes.Shape` interface as the *constraint* on its type parameter, so `primer/generic` imports `burrow/primer/shapes`. The tests also use `shapes.Rect` as an element type, because a struct whose fields are all comparable is itself comparable:

```go
func Largest[S shapes.Shape](items []S) (S, bool)
```

You also use slices, maps, `range` and `append` from P.3, and method receivers from P.4.

## What's new here

**1. The problem.** Without generics, a function that finds a value in a slice has to be written once per element type (`IndexInts`, `IndexStrings`, ...), or take `[]any` and lose all type checking. Ruby never has this problem because it checks nothing until run time: `[1, 2].index(2)` and `["a"].index("a")` are the same method. Go wants both: one function, and the compiler still rejecting `Index([]int{1}, "a")`.

**2. Type parameters.** A *type parameter* is a placeholder for a type, written in square brackets after the function name. Each one has a *constraint*, which says what types are allowed. Inside the function the parameter is used like any other type name. `comparable` is a built-in constraint meaning "values of this type can be compared with `==`", which numbers, strings, pointers and structs of comparable fields all are (slices and maps are not).

```go
func Count[T comparable](s []T, x T) int {
	n := 0
	for _, v := range s {
		if v == x {
			n++
		}
	}
	return n
}
```

**3. Calling a generic function.** You can name the type explicitly, `Count[string](words, "go")`, but usually the compiler *infers* it from the arguments, so the call looks like any other. Each different type makes the compiler check the call again with that type filled in.

```go
fmt.Println(Count([]int{1, 2, 1}, 1))          // 2
fmt.Println(Count([]string{"a", "b"}, "z"))    // 0
// Count([]int{1}, "a") doesn't compile: T can't be int and string
```

**4. Constraints are interfaces.** A constraint is an interface type. `any` (from P.4) allows every type, but then you can do almost nothing with the values: no `==`, no `<`, no `+`, because some types don't support them. The constraint is the contract: it tells the compiler which operations every allowed type has, and only those compile inside the function.

**5. Type sets and `~`.** An interface used as a constraint can also list types, separated by `|`: "any of these". A `~` in front of a type means "this type, or any type defined on top of it", so `~float64` also allows a `type Miles float64`. The standard package `cmp` defines `cmp.Ordered`, every type that supports `<` and `>`: all the integer and float types and strings, each with a `~`.

```go
type Number interface {
	~int | ~int64 | ~float64
}

func Sum[T Number](s []T) T {
	var total T
	for _, v := range s {
		total += v
	}
	return total
}

type Miles float64

fmt.Println(Sum([]Miles{1.5, 2})) // 3.5
```

An interface with a type list like `Number` can only be used as a constraint, never as the type of an ordinary variable.

**6. The zero value of T.** Sometimes a generic function must return "nothing" of type `T`, for example when the slice it was given is empty. You can't write `0` or `""` because you don't know which `T` it will be. `var zero T` declares a variable of type `T` holding its zero value (P.1), whatever `T` turns out to be.

```go
func First[T any](s []T) (T, bool) {
	if len(s) == 0 {
		var zero T
		return zero, false
	}
	return s[0], true
}
```

**7. Several type parameters.** A function can have more than one, each with its own constraint. Parameters that share a constraint can share it in the list, as in `[K, V any]`. A function value can be a parameter as well, which is how you pass in "what to do with each element", much like a Ruby block.

```go
func Keys[K comparable, V any](m map[K]V) []K {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
```

`make([]K, 0, len(m))` makes a slice of length 0 with room for `len(m)` items (P.3), so the appends never have to grow it. Note `make([]K, len(m))` would be a slice already holding `len(m)` zero values, and appending would add after them.

**8. Method interfaces as constraints.** An ordinary interface like `fmt.Stringer` or your `shapes.Shape` works as a constraint too, and then the function can call its methods. Why not just take `[]fmt.Stringer`? Because Go won't convert a `[]time.Duration` to a `[]fmt.Stringer`: the two slices are laid out differently in memory, so you'd have to copy every element by hand. A type parameter accepts the `[]time.Duration` as it is, and it can hand back a `time.Duration` instead of a `fmt.Stringer` the caller would need to assert back.

```go
func Labels[S fmt.Stringer](items []S) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.String())
	}
	return out
}

fmt.Println(Labels([]time.Duration{time.Second, time.Minute}))
// [1s 1m0s]
```

**9. Generic types.** A type can have type parameters too. Inside its definition and its methods, the parameter is just a type. A method's receiver repeats the parameter's name, as in `(s *Stack[T])`, but not its constraint, and methods cannot add type parameters of their own. Users of the type name the element type, `Stack[int]`, when they make one.

```go
type Stack[T any] struct {
	items []T
}

func (s *Stack[T]) Push(v T) { s.items = append(s.items, v) }

func (s *Stack[T]) Pop() (T, bool) {
	var zero T
	if len(s.items) == 0 {
		return zero, false
	}
	v := s.items[len(s.items)-1]
	s.items = s.items[:len(s.items)-1]
	return v, true
}

var s Stack[string]
s.Push("a")
s.Push("b")
fmt.Println(s.Pop()) // b true
```

**10. Constructors need the type spelled out.** `NewStack[T any]() *Stack[T]` takes no arguments, so there is nothing to infer `T` from: callers write `NewStack[int]()`. That is normal for constructors of generic types.

**11. A set is a map with no values.** Go has no built-in set type. The idiom is a map whose keys are the members, and whose value type is `struct{}`, the *empty struct*: a struct with no fields, which takes no memory at all. Its only value is written `struct{}{}` (the type, then an empty literal). Use the two-result lookup from P.3, `_, ok := m[k]`, to test membership; a set is the case where the stored value tells you nothing. Ruby's `Set` is built the same way, on a Hash.

```go
seen := map[string]struct{}{}
seen["go"] = struct{}{}
_, ok := seen["go"]
fmt.Println(ok, len(seen)) // true 1
```

**12. When to reach for generics.** Use a type parameter when the code is the same for every type and only the element type changes: containers, and helpers over slices and maps. When you just need to call methods on a value, a plain interface parameter (P.4) is simpler and is still the Go default. Since Go 1.21 the `slices` and `maps` packages already have most helpers you'd write (`slices.Index`, `slices.Max`, `slices.Contains`), and from here on you can use them.

## What to do

1. Create `primer/generic/generic.go` with `package generic`.
2. Write `Index[T comparable](s []T, x T) int`: the position of the first element equal to `x`, or -1 if there is none.
3. Write `Map[T, U any](s []T, f func(T) U) []U`: a new slice holding `f` of each element, in order. Don't change `s`. An empty `s` gives an empty result.
4. Write `Max[T cmp.Ordered](s []T) (T, bool)`: the largest element and true, or the zero value of `T` and false for an empty slice. Import `cmp`.
5. Write `Largest[S shapes.Shape](items []S) (S, bool)`: the item with the biggest `Area()`, the first one if several tie, and true; the zero value of `S` and false when `items` is empty.
6. Create `primer/generic/set.go` with `type Set[T comparable] struct { ... }` holding a `map[T]struct{}`, and:
   - `NewSet[T comparable]() *Set[T]`, which makes the map
   - `Add(v T) bool`, which adds `v` and returns true if it wasn't there already, false if it was
   - `Has(v T) bool` and `Len() int`
   - pointer receivers for all three
7. Keep `gofmt -l .` and `go vet ./primer/...` quiet.

## Example

```go
generic.Index([]string{"foo", "bar", "baz"}, "baz") // 2
generic.Index([]int{1, 2}, 7)                       // -1

generic.Map([]int{1, 2, 3}, strconv.Itoa)       // ["1" "2" "3"]
generic.Map([]shapes.Rect{{W: 2, H: 3}}, shapes.Rect.Area) // [6]

generic.Max([]int{4, 9, 2})          // 9, true
generic.Max([]string{"b", "c", "a"}) // "c", true
generic.Max([]float64{})             // 0, false

r, ok := generic.Largest([]shapes.Rect{{W: 1, H: 1}, {W: 3, H: 4}})
// r == shapes.Rect{W: 3, H: 4} (a Rect, not a Shape), ok == true

s := generic.NewSet[string]()
s.Add("a")   // true
s.Add("a")   // false: already there
s.Has("a")   // true
s.Len()      // 1
```

`shapes.Rect.Area` is a method expression (P.4): the `Area` method as a plain `func(Rect) float64`, which is exactly the `f` that `Map` wants.

## Done when

- [ ] `Index` works for ints, strings and structs, returns the first match, and -1 for none
- [ ] `Map` returns a new slice of the right length and order and leaves its input alone
- [ ] `Max` handles negatives, strings, floats and named number types, and says false for an empty slice
- [ ] `Largest` returns the concrete type it was given, keeps the first of a tie, and works for values, pointers and `[]shapes.Shape`
- [ ] `Set` counts each value once, treats zero values as real members, and two sets share nothing
- [ ] `primer/generic` imports `burrow/primer/shapes`

## Reference

```go
// primer/generic
package generic

// The first i with s[i] == x, or -1.
func Index[T comparable](s []T, x T) int

// A new slice of f(v) for each v in s, in order.
func Map[T, U any](s []T, f func(T) U) []U

// The largest element and true; the zero T and false if s is empty.
func Max[T cmp.Ordered](s []T) (T, bool)

// The item with the largest Area (the first on a tie) and true; the
// zero S and false if items is empty.
func Largest[S shapes.Shape](items []S) (S, bool)

// Set is a collection of distinct values. Not safe for concurrent
// use: guard it with a mutex if goroutines share one (P.6).
type Set[T comparable] struct{ /* a map[T]struct{} */ }

func NewSet[T comparable]() *Set[T]
func (s *Set[T]) Add(v T) bool // true if v was newly added
func (s *Set[T]) Has(v T) bool
func (s *Set[T]) Len() int
```

## The tests check

- Compile-time: each function and `NewSet` has the generic signature in the Reference (the test file assigns instantiations such as `generic.Index[string]` to function variables)
- `Index` over ints (every position and a missing value), strings with a duplicate, `shapes.Rect` structs, and nil
- `Map` with `strconv.Itoa`, a method expression and a closure; the result length is exact, the input unchanged, nil gives an empty result
- `Max` of ints, all-negative ints (a hint if you started from the zero value), one element, strings, floats, a named `celsius` float type, and empty int and string slices
- `Largest` of rects (the result is assignable to a `shapes.Rect`), a tie, circles, a mixed `[]shapes.Shape`, `[]*shapes.Rect` (the same pointer comes back), and empty slices
- `Set`: Add's result the first and second time, Has, Len, 0 as a member, a `Set[shapes.Rect]`, independent sets, 3000 Adds of 1000 values
- `primer/generic` imports `burrow/primer/shapes`

## Try it

```bash
./burrow watch
go test -v -run TestLP_5_Max ./primer/generic
go doc slices.Index      # the standard library's version of yours
```

Try calling `generic.Index([]int{1}, "a")` somewhere and read the compile error: the type parameter can't be both. Delete it again afterwards.

## In the wild

The standard library's `slices` package (`slices.Index`, `slices.Max`, `slices.Contains`) and `maps` package are written exactly like this level, and so are `sync/atomic.Pointer[T]` and `iter.Seq[V]`. Generic sets show up all over Go code bases, usually as a `map[T]struct{}`, because the standard library doesn't ship one.

## New in Go

type parameters · constraints · comparable · any as a constraint · type inference · explicit instantiation · cmp.Ordered · type sets with | and ~ · var zero T · several type parameters · function values as parameters · method interfaces as constraints · why []T doesn't convert to []Interface · generic types · methods on generic types · the empty struct · map[T]struct{} as a set

Standard library: `cmp`
