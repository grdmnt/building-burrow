# P.3 · Collections

> Single values only get you so far. Structs group them, slices line them up, maps look them up, and closures carry them around.

## Your goal

Write package `primer/collections`: a `Point` struct with a function that moves it through a pointer and another that measures the distance between two points with your own `flow.Sqrt`; a `Reverse` that works on the caller's slice in place; a `Chunk` that cuts a slice into a slice of slices; `WordCount`, which counts words in a map; and `Fibonacci`, which returns a function that remembers where it got to.

These are the types every later level uses, and the ones whose behaviour surprises people coming from Ruby: Go copies values unless you ask it not to, a slice is a window onto shared memory, and a map you haven't made yet can be read but not written. This level follows the Tour of Go section "More types: structs, slices, and maps", including its WordCount and Fibonacci closure exercises. P.6's web crawler counts the words of every page it fetches with your `WordCount`.

## What you're using from before

**Flow kit (P.2).** `Dist` takes its square root with `flow.Sqrt`, so `primer/collections` imports `burrow/primer/flow`. `Sqrt` returns two results and `Dist` only needs the first; the blank identifier `_` (topic 16 below) throws the other away.

```go
root, _ := flow.Sqrt(25) // 5; the step count is discarded
```

From P.2 you also use `for` loops, `if` and `+=`; from P.1, functions, `:=` and zero values.

## What's new here

**1. Pointers.** A *pointer* holds the memory address of a value. `&x` gives you a pointer to `x`, and its type is written `*int` for a pointer to an int. Putting `*` in front of a pointer *dereferences* it: `*p` is the value it points to, which you can read or assign. A pointer that points nowhere is `nil`, its zero value. Go has no pointer arithmetic, so you can't walk off into random memory.

```go
x := 10
p := &x        // p has type *int
*p = 20        // writes through the pointer
fmt.Println(x) // 20
```

Why bother? Go passes everything *by value*: calling `f(x)` hands `f` a copy of `x`, and changes to the copy vanish when `f` returns. Passing `&x` hands over the address instead, so `f` can change the original. In Ruby almost everything is a reference to a shared object, so you never choose; in Go you choose every time.

**2. Structs.** A *struct* is a value made of named fields. `type` gives it a name, so you can declare variables of it, pass it to functions and return it. Field names follow the export rule from P.1: capitalised fields are visible to other packages.

```go
type Book struct {
	Title string
	Pages int
}
```

Ruby's nearest thing is `Book = Struct.new(:title, :pages)`, but a Go struct has fixed field types and no methods unless you add them (methods are P.4).

**3. Fields.** Read and write fields with a dot. Assigning one struct variable to another copies every field, and `==` compares two structs field by field.

```go
b := Book{Title: "Dune", Pages: 412}
b.Pages = 896
c := b             // a full copy
c.Title = "Dune Messiah"
fmt.Println(b.Title, c.Title) // Dune Dune Messiah
fmt.Println(b == c)           // false
```

**4. Pointers to structs.** With a pointer `p` to a struct, the field is `(*p).Pages`, and since that is clumsy Go lets you write `p.Pages` and dereferences for you. This is how a function changes a struct you pass it: take a `*Book` parameter and call it with `&b`.

```go
func addChapter(bk *Book, pages int) {
	bk.Pages += pages // same as (*bk).Pages += pages
}

addChapter(&b, 20) // b.Pages is now 916
```

**5. Struct literals.** `Book{Title: "Dune", Pages: 412}` builds a struct by field name; fields you leave out get their zero value, so `Book{}` has an empty title and 0 pages. You can also list values in field order without names (`Book{"Dune", 412}`), but naming them keeps the code working when someone adds a field. `&Book{...}` builds one and gives you a pointer to it.

```go
empty := Book{}                 // {"" 0}
short := Book{Title: "Haiku"}   // {"Haiku" 0}
ptr := &Book{Title: "Dune"}     // *Book
fmt.Println(empty, short, *ptr) // { 0} {Haiku 0} {Dune 0}
```

**6. Arrays.** `[4]int` is an *array* of exactly four ints. The length is part of the type, so `[4]int` and `[5]int` are different types, and an array can never grow. Arrays are values like structs: assigning one copies all its elements. You'll rarely use them directly, but slices are built on them.

```go
var primes [4]int
primes[0] = 2
all := [4]int{2, 3, 5, 7}
fmt.Println(primes, len(all)) // [2 0 0 0] 4
```

**7. Slices.** A *slice*, written `[]int`, is a view of a run of elements in an array, and it is Go's everyday list type. `a[lo:hi]` makes a slice of the elements from index `lo` up to but not including `hi`, the same half-open range as Ruby's `a[lo...hi]` with three dots. Indexing past the end is not `nil` as in Ruby: it causes a *panic*, Go's way of stopping the program with an error message, here "index out of range".

```go
all := [6]int{10, 20, 30, 40, 50, 60}
mid := all[1:4]
fmt.Println(mid, len(mid)) // [20 30 40] 3
```

**8. Slices share memory.** A slice doesn't store any elements of its own. It points into an array, so two slices of the same array see each other's changes, and so does the array. Passing a slice to a function copies the small slice header (where it starts, how long it is), not the elements: the function can change the caller's elements without any pointer.

```go
func zeroAll(xs []int) {
	for i := range xs {
		xs[i] = 0
	}
}

nums := []int{1, 2, 3, 4}
zeroAll(nums[2:])
fmt.Println(nums) // [1 2 0 0]
```

**9. Slice literals.** `[]int{1, 2, 3}` builds an array of three ints and gives you a slice of all of it. It is the Go way to write Ruby's `[1, 2, 3]`. Slices can hold any type, structs included, and inside the braces you can leave the struct's type name off each element.

```go
names := []string{"ada", "grace"}
shelf := []Book{{"Dune", 412}, {"Emma", 474}}
fmt.Println(len(names), shelf[1].Title) // 2 Emma
```

**10. Slice defaults.** Either bound of `a[lo:hi]` can be left out: `lo` defaults to 0 and `hi` to the length. So `a[:2]` is the first two, `a[2:]` everything from index 2, and `a[:]` all of it.

```go
s := []int{1, 2, 3, 4, 5}
fmt.Println(s[:2], s[3:], s[:]) // [1 2] [4 5] [1 2 3 4 5]
```

**11. Length and capacity.** `len(s)` is how many elements the slice shows. `cap(s)` is how many there are in the underlying array from the slice's start to the end of the array, which is how far it could grow without moving. Slicing off the front lowers both; slicing off the end lowers only the length.

```go
s := []int{1, 2, 3, 4, 5}
t := s[1:3]
fmt.Println(len(t), cap(t)) // 2 4
```

**12. Nil slices.** The zero value of a slice is `nil`: no array behind it, length 0, capacity 0. A nil slice works with `len`, `range` and `append` like an empty one, so you almost never need to check for it. `var s []int` is the usual way to start an empty list.

```go
var s []int
fmt.Println(s == nil, len(s)) // true 0
```

**13. `make`.** `make([]int, n)` creates a slice of `n` zero values, and `make([]int, 0, n)` one of length 0 with room for `n`, so filling it doesn't need to move anything. Use it when you know the size up front.

```go
squares := make([]int, 4)
for i := range squares {
	squares[i] = i * i
}
fmt.Println(squares) // [0 1 4 9]
```

**14. Slices of slices.** The elements of a slice can be slices, which gives you a grid or a list of groups. Each inner slice is its own value with its own length, so the rows don't have to be the same size.

```go
board := [][]string{
	{"x", "o", "x"},
	{"o", "x"},
}
board[1] = append(board[1], "o")
fmt.Println(len(board), board[1]) // 2 [o x o]
```

**15. `append`.** `append(s, v1, v2)` returns a slice with the values added at the end. If the array behind `s` has room it writes into it; if not, it allocates a bigger array, copies the elements over and returns a slice of that. Either way you must keep the result, which is why it is always written `s = append(s, ...)`. Ruby's `<<` changes the array in place; Go's `append` gives you a new slice header.

```go
var log []string
log = append(log, "start")
log = append(log, "middle", "end")
fmt.Println(log, len(log)) // [start middle end] 3
```

**16. `range`.** `for i, v := range s` walks a slice, giving the index and a copy of the element on each pass, like Ruby's `each_with_index`. The *blank identifier* `_` stands in for a value you don't want, because Go rejects unused variables: `for _, v := range s`. If you only want indexes, leave the second variable off. `range` also works on strings (by character), maps, and since Go 1.22 plain ints: `for i := range 3` gives 0, 1, 2.

```go
total := 0
for _, v := range []int{5, 10, 15} {
	total += v
}
for i := range 3 {
	fmt.Print(i, " ")
}
fmt.Println(total) // prints 0 1 2 30
```

The multiple assignment from P.1 also swaps two values without a temporary variable: `a, b = b, a`. It works on slice elements too, and in the init and post statements of a `for` loop.

**17. Maps.** A *map* stores values by key: `map[string]int` maps strings to ints, like a Ruby Hash with fixed key and value types. The zero value of a map is `nil`, and a nil map can be read (every key is missing) but writing to it panics. So make one first: `make(map[string]int)`, or a literal.

```go
ages := map[string]int{"ada": 36, "alan": 41}
scores := make(map[string]int)
var broken map[string]int
fmt.Println(ages["ada"], len(scores)) // 36 0
// broken["x"] = 1 would panic: assignment to entry in nil map
_ = broken // assigning to _ counts as a use, so this compiles
```

**18. Using maps.** `m[k] = v` inserts or replaces, and `delete(m, k)` removes. Reading a missing key gives the value type's zero value, not an error and not `nil`, which makes counting easy: `m[k]++` works even the first time, the way Ruby's `Hash.new(0)` does. To tell "missing" from "present and zero", use the two-result form `v, ok := m[k]`. Iterating with `range` gives keys in a random order on purpose, so never rely on it.

```go
seen := make(map[string]int)
seen["go"]++
seen["go"]++
delete(seen, "ruby")       // deleting a missing key is fine
v, ok := seen["rust"]
fmt.Println(seen["go"], v, ok) // 2 0 false
```

`strings.Fields(s)` splits a string around runs of white space (spaces, tabs, newlines) and returns the pieces as a `[]string`; `strings.ToLower` lowercases a string.

**19. Function values.** Functions are values: you can store one in a variable, pass it to another function, and return it. The type of a function value is written like its signature without the name, `func(int) int`. Ruby gets this from lambdas and blocks.

```go
apply := func(f func(int) int, n int) int {
	return f(n)
}
double := func(n int) int { return n * 2 }
fmt.Println(apply(double, 21)) // 42
```

**20. Closures.** A function written inside another function can use the outer function's variables, and it keeps them alive after the outer function returns. Such a function is a *closure*: each call to the outer function makes fresh variables, so each closure it returns has its own private state. Ruby lambdas capture their surroundings the same way.

```go
func adder() func(int) int {
	sum := 0
	return func(n int) int {
		sum += n
		return sum
	}
}

a, b := adder(), adder()
a(10)
fmt.Println(a(5), b(1)) // 15 1
```

## What to do

1. Create `primer/collections/collections.go` with `package collections`, importing `burrow/primer/flow`.
2. Declare `type Point struct { X, Y float64 }`.
3. Write `Move(p *Point, dx, dy float64)`, which adds `dx` to the X and `dy` to the Y of the point `p` points to.
4. Write `Dist(a, b Point) float64`, the straight-line distance between two points: the square root of `dx*dx + dy*dy`, where `dx` and `dy` are the differences between their X and Y fields. Take the root with `flow.Sqrt`.
5. Write `Reverse(xs []int)`, which reverses the elements of `xs` in place and returns nothing. Swap the first and last, then the second and second last, and so on until you reach the middle. Empty and one-element slices are already reversed.
6. Write `Chunk(xs []int, size int) [][]int`, which cuts `xs` into consecutive pieces of `size` elements, the last piece holding whatever is left: `Chunk([]int{1, 2, 3, 4, 5}, 2)` is `[[1 2] [3 4] [5]]`. Return `nil` when `size` is less than 1 or `xs` is empty. Build each piece by slicing `xs` and `append` it to the result; the `min` builtin keeps the last slice inside `xs`.
7. Write `WordCount(s string) map[string]int`: split `s` with `strings.Fields`, lowercase each word with `strings.ToLower`, and count how many times each appears. Punctuation stays part of the word, so `"go,"` and `"go"` are different words. Always return a map you made, never `nil`, even for an empty string.
8. Write `Fibonacci() func() int`. Each function it returns yields the Fibonacci numbers in turn, one per call: 0, 1, 1, 2, 3, 5, 8, and so on, each the sum of the two before it. Two functions from two calls to `Fibonacci` must count independently.
9. Keep `gofmt -l .` and `go vet ./primer/...` quiet.

## Example

```go
p := collections.Point{X: 1, Y: 2}
collections.Move(&p, 3, -4)             // p is now {4 -2}
origin := collections.Point{}
collections.Dist(origin, collections.Point{X: 3, Y: 4}) // 5

xs := []int{1, 2, 3, 4, 5}
collections.Reverse(xs)                 // xs is [5 4 3 2 1]
collections.Chunk(xs, 2)                // [[5 4] [3 2] [1]]

collections.WordCount("The cat and THE hat")
// map[and:1 cat:1 hat:1 the:2]

next := collections.Fibonacci()
next(); next(); next(); next()          // 0, 1, 1, 2
```

## Done when

- [ ] `Move` changes the caller's `Point`, and `Dist` is right to within 1e-9 using `flow.Sqrt`
- [ ] `Reverse` reverses in place, including when it is given a slice of the middle of a bigger slice
- [ ] `Chunk` gives the right pieces for exact and uneven sizes and `nil` for empty input or a size below 1
- [ ] `WordCount` lowercases, splits on any white space, keeps punctuation and never returns `nil`
- [ ] Every function from `Fibonacci()` starts at 0 and keeps its own place

## Reference

```go
// primer/collections/collections.go
package collections   // imports burrow/primer/flow

type Point struct {
	X, Y float64
}

// Move adds dx and dy to the point p points to.
func Move(p *Point, dx, dy float64)

// Dist is the straight-line distance from a to b, via flow.Sqrt.
func Dist(a, b Point) float64

// Reverse reverses xs in place.
func Reverse(xs []int)

// Chunk cuts xs into pieces of size elements, the last possibly
// shorter. nil when xs is empty or size < 1.
func Chunk(xs []int, size int) [][]int

// WordCount counts the words of s, split with strings.Fields and
// lowercased. Punctuation is kept. Never nil. P.6 relies on this.
func WordCount(s string) map[string]int

// Fibonacci returns a generator of 0, 1, 1, 2, 3, 5, ...
func Fibonacci() func() int
```

## The tests check

- `Move(&p, 3, -4)` from `{1 2}` leaves `{4 -2}`; two more moves through another pointer to the same point add up
- `Dist` for a 3-4-5 triangle both ways round, negative coordinates, a diagonal of length √2, and a point to itself
- `primer/collections` imports `burrow/primer/flow`
- `Reverse` on nil, empty, one, two, three and six elements, and `Reverse(xs[1:4])` on `[1 2 3 4 5]` leaves `[1 4 3 2 5]`
- `Chunk` with sizes 1, 2 and 3, a size equal to and bigger than the length, nil and empty input, and sizes 0 and -1
- `WordCount` for the empty string, only white space, one word, mixed case, tabs and newlines and double spaces, and words with punctuation; the result for `""` is a map you can add to
- `Fibonacci`'s first 12 numbers, and a fresh generator starts at 0 even after another one has been used

## Try it

```bash
./burrow watch
go test -v -run TestLP_3_WordCount ./primer/collections
```

For a feel of how slices share memory, add a temporary test that slices a slice, appends to the small one, and prints both. Then do the same when the small one is at the end of the big one's capacity, and see the difference.

## In the wild

`strings.Fields`, which you just used, is itself a loop over a slice filling a pre-sized `make([]string, n)`; `go doc -src strings.Fields` shows it. The standard library's `slices` and `maps` packages (`slices.Reverse`, `slices.Chunk`, `maps.Keys`) do in general form what you wrote by hand here, once you've seen generics in P.5.

## New in Go

pointers (& and *) · pass by value · structs · fields and struct literals · pointers to structs · arrays · slices · slicing and its defaults · shared backing arrays · len and cap · nil slices · make · slices of slices · append · range and the blank identifier · maps · nil maps · missing keys read as zero · v, ok := m[k] · delete · function values · closures

Standard library: `strings`
