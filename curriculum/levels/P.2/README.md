# P.2 · Flow Control

> A program that runs top to bottom once is a calculator. Loops, branches and deferred work turn it into a machine.

## Your goal

Write package `primer/flow`: four functions, each built around one way Go decides what runs next. `Sqrt` finds a square root by guessing and improving the guess in a loop, and reports how many guesses it took. `FizzBuzz` picks a word with a `switch`. `Countdown` counts up but writes down, because `defer` reverses the order for it. `Receipt` turns P.1's `SplitBill` into one printed line per person, with an `if` for the cents that don't divide evenly.

Go has exactly one loop keyword, `for`, and its `if` and `switch` are a little different from Ruby's. This level follows the Tour of Go section "Flow control statements: for, if, else, switch and defer", including its Newton's-method exercise.

## What you're using from before

**Basics kit (P.1).** `Receipt` calls your `basics.SplitBill` to do the dividing, so `primer/flow` imports `burrow/primer/basics`. That is the first time one of your packages uses another: the same import line you wrote in `primer/hello`, now from a library package instead of a program.

```go
each, leftover := basics.SplitBill(cents, people)
```

You also use multiple results, `:=`, conversions and `float64` from P.1 throughout.

## What's new here

**1. The `for` loop.** Go's only loop is `for`. Its classic form has three parts separated by semicolons and no parentheses: an *init statement* run once before the loop, a *condition* checked before every pass, and a *post statement* run after every pass. The braces are required, even for one line. Variables declared in the init statement exist only inside the loop.

```go
sum := 0
for i := 1; i <= 4; i++ {
	sum += i
}
fmt.Println(sum) // 10
```

`i++` adds one to `i` (it is a statement, not an expression, so `x = i++` doesn't compile), and `sum += i` is short for `sum = sum + i`. Ruby would say `(1..4).each { |i| sum += i }` or `(1..4).sum`. Go also has `for i := range 4`, which counts 0, 1, 2, 3; P.3 covers `range` properly.

**2. Leaving parts out.** The init and post statements are optional. Drop them and the semicolons too, and `for` is Ruby's `while`: just a condition.

```go
n := 1
for n < 1000 {
	n *= 2
}
fmt.Println(n) // 1024
```

**3. Forever.** Leave out the condition as well and the loop runs until something inside it stops it: `return` leaves the whole function, `break` leaves the loop. This is Ruby's `loop do ... end`.

```go
tries := 0
for {
	tries++
	if tries == 3 {
		break
	}
}
fmt.Println(tries) // 3
```

**4. `if`.** Like `for`, `if` has no parentheses around its condition and always needs braces. The condition must be a `bool`: Go has no "truthy" values, so `if n {` with an int `n` is a compile error, where Ruby would treat any number as true.

```go
temp := 31.5
if temp > 30 {
	fmt.Println("hot")
}
```

**5. `if` with a short statement.** `if` can run a short statement before its condition, separated by a semicolon. A variable declared there exists only in the `if` and its `else` branches, which keeps throwaway values out of the rest of the function. You'll see this constantly with errors in later levels.

```go
if d := math.Abs(-0.003); d < 0.01 {
	fmt.Println("close enough:", d)
}
// d doesn't exist here
```

**6. `if` and `else`.** `else` must sit on the same line as the closing brace before it, and `else if` chains as you'd expect. Go style prefers returning early over long `else` chains: handle the odd case first with an `if` that returns, then write the normal case unindented.

```go
func describe(n int) string {
	if n < 0 {
		return "negative"
	} else if n == 0 {
		return "zero"
	}
	return "positive"
}
```

**7. Math with floats.** The `math` package has what you'd expect: `math.Abs`, `math.Sqrt`, `math.Max`, `math.Pi`. It also has `math.NaN()`, a special float64 meaning "not a number", for answers that don't exist. NaN is not equal to anything, itself included, so you test for it with `math.IsNaN(x)`, never `x == math.NaN()`.

```go
nan := math.NaN()
fmt.Println(nan == nan, math.IsNaN(nan)) // false true
fmt.Println(math.Abs(2.5 - 3))           // 0.5
```

**8. Newton's method.** To find the square root of `x`, guess some `z`, then improve the guess: if `z*z` is too big, `z` is too big, and the amount to move by is `(z*z - x) / (2*z)`. Each new guess is `z - (z*z - x) / (2*z)`. Every step roughly doubles the number of correct digits, so after a handful of steps the guesses stop changing, and that is when you stop. This is the Tour's "Loops and Functions" exercise, and your `Sqrt` is exactly this loop.

```text
x = 2, starting guess z = 1
1  ->  1.5  ->  1.41667  ->  1.4142157  ->  1.41421356237469
```

**9. `switch`.** A `switch` compares one value against a list of cases and runs the first that matches. Unlike C or JavaScript, a case does not fall through into the next one, so there is no `break` to forget. A case can list several values separated by commas, and `default` runs when nothing matches. Ruby's `case ... when` behaves the same way.

```go
switch day := time.Now().Weekday(); day {
case time.Saturday, time.Sunday:
	fmt.Println("weekend")
default:
	fmt.Println("weekday:", day)
}
```

As with `if`, the short statement before the value (`day := ...;`) is optional.

**10. Evaluation order.** Cases are tried top to bottom, and the first match wins: the cases after it aren't even evaluated. When cases overlap, the more specific one has to come first.

```go
n := 100
switch {
case n > 10:
	fmt.Println("big")   // this one runs
case n > 50:
	fmt.Println("huge")  // never reached: n > 10 matched first
}
```

**11. `switch` with no condition.** Leave out the value and each case is a bool condition of its own: the first true one runs. This is Go's tidy replacement for a long `if`/`else if` chain, and it reads like Ruby's `case` with no subject.

```go
func feel(temp float64) string {
	switch {
	case temp < 10:
		return "cold"
	case temp < 25:
		return "mild"
	default:
		return "hot"
	}
}
```

**12. `defer`.** `defer f(args)` schedules the call `f(args)` to run when the surrounding function returns, however it returns. The arguments are evaluated immediately, when the `defer` line runs; only the call itself waits. In real code this is how you make sure cleanup happens: `defer file.Close()` right after opening a file. Ruby gets the same guarantee from `ensure` or a block (`File.open(...) do ... end`).

```go
func show() {
	x := 1
	defer fmt.Println("deferred, x was", x)
	x = 2
	fmt.Println("now x is", x)
}
// now x is 2
// deferred, x was 1
```

**13. Stacking defers.** Deferred calls go on a stack: the last one deferred runs first. A function that defers three calls runs them in the reverse of the order it reached them.

```go
func stack() {
	defer fmt.Println("deferred first, runs last")
	defer fmt.Println("deferred second, runs second")
	defer fmt.Println("deferred last, runs first")
	fmt.Println("body")
}
```

**14. Building strings.** Strings join with `+` and `+=`, and `strconv.Itoa(n)` turns an int into its decimal string (`strconv.Itoa(42)` is `"42"`). Go does not convert numbers to strings for you, and `string(42)` is not `"42"` (it is the character with code 42, `*`, and `go vet` warns about it). For text built a piece at a time, a `strings.Builder` collects writes efficiently; `WriteString` adds to it and `String` gives you the result. Passing `&b` lets a function write into your builder rather than a copy of it (the `&` and the `*` in `*strings.Builder` are pointers, which P.3 explains).

```go
func label(b *strings.Builder, n int) {
	b.WriteString("item " + strconv.Itoa(n) + "; ")
}

var b strings.Builder
label(&b, 1)
label(&b, 2)
fmt.Println(b.String()) // item 1; item 2;
```

## What to do

1. Create `primer/flow/flow.go` with `package flow`.
2. Write `Sqrt(x float64) (z float64, steps int)` with Newton's method:
   - a negative `x` returns `math.NaN()` and 0 steps; `x == 0` returns 0 and 0 steps, without looping
   - otherwise start from `z = 1.0`, and in a loop compute the next guess from `z`, adding one to `steps` each time you do
   - stop as soon as the next guess differs from `z` by less than `1e-10` (use `math.Abs`), and return that next guess and `steps`
   - as a safety net, never compute more than 100 guesses
3. Write `FizzBuzz(n int) string`: `"FizzBuzz"` when `n` is a multiple of both 3 and 5, `"Fizz"` for a multiple of 3, `"Buzz"` for a multiple of 5, and otherwise the number itself as a string. `n%3 == 0` means "n is a multiple of 3". Use a `switch` with no condition.
4. Write `Countdown(b *strings.Builder, n int)`, which writes `3 2 1 liftoff` into `b` for `n = 3` (and just `liftoff` for 0). Do it the backwards way, which is the point: defer the write of `liftoff` first, then loop *up* from 1 to `n`, deferring the write of each number followed by a space. When `Countdown` returns, the defers run in reverse.
5. Write `Receipt(cents, people int) string`, one line per person in the form `person 1: 334` with a newline after each, with every cent paid:
   - if `people` is less than 1, return `"nobody to split with\n"`
   - otherwise get `each` and `leftover` from `basics.SplitBill`; persons 1 to `leftover` pay `each + 1`, everyone else pays `each`
6. Keep `gofmt -l .` and `go vet ./primer/...` quiet.

## Example

```go
flow.Sqrt(2)          // 1.4142135623730951, 5
flow.Sqrt(9)          // 3, 7
flow.Sqrt(-4)         // NaN, 0
flow.FizzBuzz(9)      // "Fizz"
flow.FizzBuzz(30)     // "FizzBuzz"
flow.FizzBuzz(7)      // "7"

var b strings.Builder
flow.Countdown(&b, 3)
b.String()            // "3 2 1 liftoff"

fmt.Print(flow.Receipt(1000, 3))
// person 1: 334
// person 2: 333
// person 3: 333
```

## Done when

- [ ] `Sqrt` agrees with `math.Sqrt` and takes exactly the number of steps the rule above gives (5 for 2, 15 for a million)
- [ ] `FizzBuzz` is right for 1 to 15 and beyond
- [ ] `Countdown` writes with `defer` from inside a loop and gets the order right
- [ ] Every `Receipt` adds up to the bill, and nobody pays more than one cent more than anyone else
- [ ] `primer/flow` imports and uses `burrow/primer/basics`

## Reference

```go
// primer/flow/flow.go
package flow

// Sqrt: Newton's method from z = 1.0, stopping once a guess moves by
// less than 1e-10; steps counts the guesses computed (at most 100).
// Negative x gives (NaN, 0); zero gives (0, 0).
func Sqrt(x float64) (z float64, steps int)

// FizzBuzz returns "FizzBuzz", "Fizz", "Buzz" or strconv.Itoa(n).
func FizzBuzz(n int) string

// Countdown writes "n ... 2 1 liftoff" to b using deferred writes.
func Countdown(b *strings.Builder, n int)

// Receipt returns "person i: amount\n" per person using
// basics.SplitBill; the first leftover people pay one cent more.
// people < 1 gives "nobody to split with\n".
func Receipt(cents, people int) string
```

## The tests check

- `Sqrt` for 11 values from 1e-6 to 1e10 is within 1e-9 (relative) of `math.Sqrt` and reports the exact step count: 1 for 1, 5 for 2, 6 for 4, 7 for 9, 15 for 1e6, 22 for 1e10
- `Sqrt` of negatives is NaN with 0 steps; `Sqrt(0)` is 0 with 0 steps
- `FizzBuzz` for 1 to 15, 30, 45 and 98 to 101; a wrong answer for a multiple of 15 gets a hint about case order
- `Countdown` for 0, 1, 3 and 10, and it appends to a builder that already holds text instead of replacing it
- The source of `Countdown` has a `defer` inside a `for` loop (any loop that counts down would print the same thing, so the test reads your code)
- `Receipt` exact text for even and uneven splits, zero cents, one person, and zero or negative people
- For every bill from 0 to 60 cents and 1 to 7 people: one line per person, numbered from 1, shares adding up to the bill and differing by at most one cent
- `primer/flow` imports `burrow/primer/basics`

## Try it

```bash
./burrow watch
go test -v -run TestLP_2_Sqrt ./primer/flow   # just the Sqrt tests
```

To watch Newton's method converge, add a temporary `fmt.Println(steps, z)` inside your loop and run `go test -v -run 'TestLP_2_Sqrt/^2$' ./primer/flow`. Take it out again afterwards.

## In the wild

`defer f.Close()` and `defer mu.Unlock()` appear in nearly every Go codebase. `math.Sqrt` is a single CPU instruction on most machines, but `math/big`'s `Float.Sqrt` still refines its answer with Newton's method, because arbitrary-precision numbers have no hardware instruction.

## New in Go

for (three-part, condition-only, forever) · break · ++ and += · if without parentheses · if with a short statement · else · no truthiness · math.NaN and math.IsNaN · switch on a value · switch with no condition · no fallthrough · defer · deferred arguments evaluated immediately · stacked defers (last in, first out) · strconv.Itoa · strings.Builder · importing your own package

Standard library: `math`, `strconv`, `strings`
