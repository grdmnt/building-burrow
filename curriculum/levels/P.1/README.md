# P.1 · Basics

> Before the machine, the alphabet. Packages, names, functions, variables, types and constants: everything else in Go is built from these.

## Your goal

Write your first Go program, `primer/hello`, which prints `hello, burrow`, and your first Go package, `primer/basics`, which that program imports. The package holds five small things, one per idea: a greeting built from a private constant, a bill splitter with two results, an average that has to convert between number types, a set of size constants, and a counter that relies on a variable starting at zero.

None of it is hard. The point is to meet each piece of Go syntax once, in a place where a test tells you straight away whether you got it right. This level follows the first section of the Tour of Go (go.dev/tour, "Packages, variables, and functions"), so you can read the Tour's slide for any topic below if you want a second explanation.

## What you're using from before

Nothing. This is the first page of the Primer, the optional chapter for people who have never written Go. If you already know Go's syntax, skip to 0.1.

## What's new here

**1. Packages.** Every Go file starts with `package <name>`, and every file in the same directory must use the same name. A *package* is that directory: its files are compiled together and share everything they declare, as if they were one file. A program starts running at `func main()` in a package called `main`. In Ruby, a file is loaded by `require` and dumps its definitions into whatever modules it opens; in Go, the directory is the unit and there is no load order to think about.

```go
// primer/demo/main.go
package main

import "fmt"

func main() {
	fmt.Println("one program, one package main")
}
```

Run it with `go run ./primer/demo`. `go run` compiles the package in that directory into a temporary program and runs it.

**2. Modules and imports.** A *module* is a tree of packages with a `go.mod` file at its root. This repository's `go.mod` says `module burrow`, so the package in directory `primer/basics` has the *import path* `burrow/primer/basics`: module name, slash, directory. `import` takes import paths. Standard library packages have short paths like `"fmt"` and `"math"`. Several imports go in one parenthesised block, and you refer to what a package declares by its last path element and a dot.

```go
import (
	"fmt"
	"math"
	"math/rand"
)

fmt.Println(math.Pi)        // 3.141592653589793
fmt.Println(rand.Intn(10))  // a random int from 0 to 9
```

Your own packages are imported the same way: `import "burrow/primer/basics"`, then `basics.Greet(...)`.

Go refuses to compile a file that imports a package and never uses it. Ruby's closest thing is `require "set"`, but Go's import is checked by the compiler, not run.

**3. Exported names.** A name that starts with a capital letter is *exported*: other packages can use it. A name that starts with a lowercase letter is *unexported*: only code inside the same package can see it. That one rule replaces Ruby's `public` and `private`. `math.Pi` works because `Pi` is capitalised; `math.pi` is a compile error, there is no such exported name.

```go
package temps

const freezing = 32.0      // only package temps can use this

func IsFreezing(f float64) bool { // anyone can call temps.IsFreezing
	return f <= freezing
}
```

Test files (names ending in `_test.go`) are the one exception to "one package name per directory". This level's tests include one file that says `package basics`, so it is inside your package and can check the unexported `greeting`; the others say `package basics_test`, a separate package that sees only what you export, the same view `primer/hello` has.

**4. Functions.** `func name(params) resultType { ... }`. Each parameter's type comes *after* its name, and when several neighbours share a type you can write it once: `(x, y int)` means both are ints. Go has no implicit return: if a function has a result, it must `return` it.

```go
func add(x, y int) int {
	return x + y
}

add(2, 3) // 5
```

Ruby would write `def add(x, y) = x + y` and work out types at run time. Go checks at compile time that every call passes ints and uses the result as an int.

**5. Multiple results.** A function can return more than one value. List the result types in parentheses, return them separated by commas, and receive them the same way. This is everywhere in Go: most functions that can fail return a value and an error (the Primer gets to errors in P.4).

```go
func minmax(a, b int) (int, int) {
	return min(a, b), max(a, b)
}

lo, hi := minmax(7, 3) // lo is 3, hi is 7
```

In Ruby you'd return an array, `[lo, hi]`, and destructure it. Go returns two separate values; there is no array in between.

**6. Named results.** You can give the results names. They become variables inside the function, starting at zero, and a bare `return` returns whatever they hold. Use names when they document what each result means; keep bare returns to short functions, because in a long one the reader has to hunt for what is being returned.

```go
func rect(w, h float64) (area, perimeter float64) {
	area = w * h
	perimeter = 2 * (w + h)
	return
}
```

**7. Variables with `var`.** `var name type` declares a variable. `var` works inside a function and at the top level of a file, where the variable lives as long as the program does. With an initial value you can leave the type out and Go takes it from the value.

```go
var count int          // declared, starts at 0
var name = "burrow"    // type string, worked out from the value
var x, y = 1, "two"    // two at once, different types
```

**8. Short declarations with `:=`.** Inside a function, `name := value` declares and assigns in one go, with the type taken from the value. It is how you'll declare most local variables. Outside a function every statement must start with a keyword (`var`, `func`, `const`, `type`, `import`), so `:=` is not allowed at the top level. `=` assigns to a variable that already exists; `:=` makes a new one.

```go
func demo() {
	n := 10     // new variable, type int
	n = 11      // plain assignment
	s, ok := "hi", true
	fmt.Println(n, s, ok) // 11 hi true
}
```

Ruby has only `=`, which creates the variable on first use. Go makes the difference visible, so a typo can't silently create a new variable.

**9. Basic types.** `bool`; `string`; signed integers `int`, `int8`, `int16`, `int32`, `int64`; unsigned `uint`, `uint8` to `uint64`; `float32`, `float64`; and `complex64`, `complex128`. `byte` is another name for `uint8` and `rune` (one Unicode character) is another name for `int32`. Use `int` for counting and `float64` for decimals unless you have a reason not to. `int` is 64 bits on any machine you're likely to use.

```go
var big int64 = 1 << 62
var ratio float64 = 0.75
var initial rune = 'G'     // single quotes: a rune, not a string
fmt.Printf("%T %T %T\n", big, ratio, initial)
// int64 float64 int32
```

`fmt.Printf` prints with a format string: `%v` is any value, `%T` its type, `%d` an integer, `%q` a quoted string, `\n` a newline. `fmt.Sprintf` takes the same arguments and returns the string instead of printing it.

**10. Zero values.** A variable declared without a value is not `nil` and not undefined: it holds its type's *zero value*. That is `0` for numbers, `false` for bool, and `""` (the empty string) for strings. Go code leans on this all the time: a counter needs no initialiser, an empty string means "not set yet".

```go
var n int
var ok bool
var s string
fmt.Printf("%v %v %q\n", n, ok, s) // 0 false ""
```

In Ruby an unset instance variable is `nil`, and `nil + 1` raises. In Go `n + 1` on a fresh int is just `1`.

**11. Type conversions.** Go never converts between number types on its own. `T(v)` converts `v` to type `T`. Dividing two ints gives an int, rounded toward zero, so convert *before* you divide if you want the fraction. Converting a float to an int drops the fraction.

```go
total, n := 7, 2
fmt.Println(total / n)                    // 3
fmt.Println(float64(total) / float64(n))  // 3.5
f := -2.9
fmt.Println(int(f))                       // -2
```

Ruby does this too (`7 / 2` is `3`), but quietly turns `7 / 2.0` into a float. Go never mixes types behind your back: `total / f` with an int `total` and a float64 `f` does not compile. Careful, though: `total / 2.0` compiles and is still `3`, because the constant `2.0` turns into an int to match `total` (topic 13 explains why constants can do that).

**12. Type inference.** With `:=` or `var x = ...`, the variable's type comes from the right-hand side. A plain number written in code gets a default: `42` makes an `int`, `4.2` a `float64`, `'x'` a `rune`.

```go
i := 42      // int
f := 4.2     // float64
g := i + 1   // int, because i is
```

**13. Constants.** `const` declares a value fixed at compile time: a number, string or bool, never something computed at run time. A constant written without a type is *untyped*: it keeps exact precision and takes on whatever type the place it is used needs. That's why `time.Second * 2` and `x / 2.0` work. Untyped numeric constants can even be far bigger than any int, as long as what you finally store fits.

```go
const (
	Hour   = 60 * Minute // at top level, order is free
	Minute = 60
	huge   = 1 << 100 // fine: never stored as an int
)

var h int64 = Hour           // 3600 as an int64
var f float64 = huge >> 98   // 4 as a float64
var g float64 = huge         // 1.2676506002282294e+30
```

Write `const Minute int = 60` and it becomes *typed*: now `var f float64 = Minute` is a compile error. Ruby constants are just variables you promise not to reassign; Go constants really are compile-time values.

**14. Formatting.** `gofmt -w .` rewrites every Go file under the current directory into the one standard layout: tabs, spacing, import order. Nobody argues about style in Go because nobody chooses it. From 0.1 on the tests check that your code is gofmt-clean, so get the habit now.

## What to do

1. Create `primer/basics/basics.go` starting with `package basics`. All the package code below can go in that one file.
2. Declare an unexported constant `greeting` with the value `"hello"`, and an exported function `Greet(name string) string` that returns `greeting`, a comma and a space, then `name`. Strings join with `+`.
3. Write `SplitBill(cents, people int) (each, leftover int)` with named results: `each` is how many cents everyone pays when the bill is shared evenly, and `leftover` is the cents that remain. `/` divides ints and `%` gives the remainder. You can assume `people` is at least 1 (P.2 adds the check for when it isn't).
4. Write `Average(a, b, c int) float64`, the exact mean of three ints, fraction included: the average of 1, 2 and 2 is 1.666..., not 1.
5. Declare the exported, untyped constants `KiB`, `MiB`, `GiB` and `TiB`: 1024 bytes, 1024 KiB, 1024 MiB and 1024 GiB. Build each one from the one before.
6. Declare an unexported package-level variable `calls` of type `int`, with no initial value, and write `Next() int`, which adds one to `calls` and returns it. `calls += 1` or `calls++` both do it.
7. Create `primer/hello/main.go` with `package main`, importing `fmt` and `burrow/primer/basics`, whose `main` prints `basics.Greet("burrow")` with `fmt.Println`.
8. Run `gofmt -w .` and `go vet ./primer/...` (vet flags code that compiles but is almost certainly a mistake) until both are quiet.

## Example

```bash
$ go run ./primer/hello
hello, burrow
```

What calls into your package look like from another package:

```go
basics.Greet("gopher")         // "hello, gopher"
basics.SplitBill(1000, 3)      // 333, 1
basics.Average(1, 2, 2)        // 1.6666666666666667
var f float64 = basics.TiB     // 1.099511627776e+12
basics.Next()                  // 1, then 2, then 3...
basics.greeting                // compile error: unexported
```

## Done when

- [ ] `go run ./primer/hello` prints exactly `hello, burrow` and a newline, by calling `basics.Greet`
- [ ] `Greet`, `SplitBill`, `Average` and `Next` give the results in the reference
- [ ] The size constants are untyped, so the same `TiB` can be stored in an `int64` and a `float64`
- [ ] `gofmt -l .` and `go vet ./primer/...` print nothing

## Reference

The exact contract the tests hold you to.

```go
// primer/basics/basics.go
package basics

const greeting = "hello"   // unexported

// Greet returns greeting + ", " + name.
func Greet(name string) string

// SplitBill shares cents evenly: each*people + leftover == cents,
// with 0 <= leftover < people. people >= 1.
func SplitBill(cents, people int) (each, leftover int)

// Average is (a+b+c)/3 with the fraction kept.
func Average(a, b, c int) float64

const KiB, MiB, GiB, TiB = ... // 1<<10, 1<<20, 1<<30, 1<<40, untyped

var calls int              // unexported, no initial value

// Next increments calls and returns the new value: 1, 2, 3...
func Next() int

// primer/hello/main.go
package main               // imports burrow/primer/basics
func main()                // prints basics.Greet("burrow") + "\n"
```

## The tests check

- The built `primer/hello` program prints exactly `hello, burrow\n`, and its source imports `burrow/primer/basics`
- A test file inside package `basics` reads the unexported `greeting` and checks `Greet` is built from it
- `Greet` for `burrow`, `gopher` and the empty string
- `SplitBill` for even splits, uneven ones, fewer cents than people and zero cents; each answer adds back up to the bill
- `Average` keeps the fraction, including for negative numbers; a result rounded toward zero gets a hint about converting before dividing
- `KiB` to `TiB` have the right values and are untyped: the test stores `TiB` in an `int64` and a `float64`, `GiB` in a `uint32`, and `TiB * TiB` (2^80) in a `float64`. A typed constant makes the test file fail to compile, with an error naming the constant
- The first call to `Next` returns 1 and each later call returns one more

## Try it

```bash
./burrow watch               # re-runs the tests every time you save
go run ./primer/hello
go test -v ./primer/basics   # -v lists every test as it runs
go doc ./primer/basics       # your package's exported API
```

Then try to break the rules on purpose and read the compiler's message: use `basics.greeting` in `main.go`, or write `n := 1` at the top level of `basics.go`, or add an import you don't use.

## In the wild

Any file in the standard library looks like this one: `go doc -src strings.Fields` shows an exported function with a doc comment calling unexported helpers, and `go doc time.Second` shows a block of typed constants built from each other.

## New in Go

packages · package main and func main · modules and import paths · exported vs unexported names · functions · multiple results · named results and bare return · var · := · basic types · zero values · type conversions · type inference · constants (typed and untyped) · fmt.Println, Printf and %v %T · go run · gofmt

Standard library: `fmt`
