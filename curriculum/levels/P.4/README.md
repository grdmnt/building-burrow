# P.4 · Methods and Interfaces

> A part is only useful if other parts know how to plug into it. Methods give your types behaviour; interfaces are the sockets.

## Your goal

Write package `primer/shapes`. It has two shapes, `Rect` and `Circle`, that know their own area and perimeter, an interface `Shape` that both satisfy without saying so, and a `Scale` method that changes a rect in place. Then the standard interfaces every Go program leans on: both shapes print themselves through `fmt.Stringer`, `FetchError` and `ErrNegativeSqrt` are errors of your own design, `Describe` sorts any value by its type, and `Rot13Reader` is an `io.Reader` that decodes a secret message as it is read. Finally `CountWords` reads any `io.Reader` and counts its words with P.3's `WordCount`.

This level follows the Tour of Go section "Methods and interfaces", including its Stringers, Errors, Readers and rot13Reader exercises. It is the biggest idea in the Primer: Go has no classes and no inheritance, and these two features do the work instead.

## What you're using from before

**Collections kit (P.3 · Collections).** `CountWords` reads all the bytes out of a reader, turns them into a string and hands them to your `collections.WordCount`, so `primer/shapes` imports `burrow/primer/collections`:

```go
counts := collections.WordCount(string(data))
```

You also use structs, pointers and `&` from P.3 throughout, plus `switch` from P.2 and `float64` and `fmt` from P.1.

## What's new here

**1. Methods.** A method is a function with a *receiver*: an extra parameter written in its own parentheses before the name. Inside the method the receiver is an ordinary variable. Unlike Ruby, where `def` sits inside a `class ... end`, a Go method is declared on its own at the top level of the file, next to the type, and any type you define in the package can have methods.

```go
type Temp struct {
	Celsius float64
}

func (t Temp) Fahrenheit() float64 {
	return t.Celsius*9/5 + 32
}

fmt.Println(Temp{Celsius: 100}.Fahrenheit()) // 212
```

**2. Methods are functions.** A method is just a function with the receiver moved to the front. The two forms below do the same thing; the method form reads better at the call site and, as you'll see, lets the type satisfy interfaces. You can even get the function back out of a method: `Temp.Fahrenheit` is a plain `func(Temp) float64`, called a *method expression*.

```go
func Fahrenheit(t Temp) float64 { return t.Celsius*9/5 + 32 }

t := Temp{Celsius: 20}
fmt.Println(Fahrenheit(t), t.Fahrenheit()) // 68 68
f := Temp.Fahrenheit                       // func(Temp) float64
fmt.Println(f(t))                          // 68
```

**3. Methods on any type you define.** The receiver doesn't have to be a struct. `type Miles float64` makes a new type whose values are float64s underneath, and it can have methods. The one rule: the type must be declared in the same package as the method, so you can't add methods to `int` or to another package's types. Ruby lets you reopen `Integer` and add methods anywhere; Go deliberately doesn't.

```go
type Miles float64

func (m Miles) Km() float64 { return float64(m) * 1.609344 }

fmt.Println(Miles(26.2).Km()) // 42.1648128
```

**4. Pointer receivers.** A receiver written `(t Temp)` is a *value receiver*: the method gets a copy, so changes it makes are lost when it returns. Written `(t *Temp)` it is a *pointer receiver*: the method gets the address of the caller's value (P.3's pointers) and can change it. Ruby methods always work on the object itself, so in Ruby every method is effectively a pointer receiver.

```go
func (t *Temp) Warm(by float64) { t.Celsius += by }
func (t Temp) WarmCopy(by float64) { t.Celsius += by }

t := Temp{Celsius: 20}
t.WarmCopy(5) // changes a copy
t.Warm(5)     // changes t
fmt.Println(t.Celsius) // 25
```

**5. Go takes the address for you.** `t.Warm(5)` works even though `t` is a `Temp`, not a `*Temp`: when a method has a pointer receiver and `t` is a variable, Go quietly calls `(&t).Warm(5)`. It works the other way too: calling a value method on a pointer, Go uses `*p`. A plain function taking `*Temp` gets no such help; there you must pass `&t` yourself.

```go
p := &Temp{Celsius: 0}
fmt.Println(p.Fahrenheit()) // 32: Go calls (*p).Fahrenheit()
```

**6. Choosing value or pointer.** Use a pointer receiver when the method must change the receiver, or when the struct is large and copying it on every call would be wasteful. The usual advice is to then use pointer receivers for every method of that type, so the type behaves one way. This level breaks that rule on purpose: `Rect` has value methods and one pointer method, so you can see the difference.

**7. Interfaces.** An *interface* is a type that lists method signatures and nothing else. A variable of an interface type can hold any value whose type has those methods, and calling a method on it runs the method of whatever value is inside.

```go
type Warmer interface {
	Fahrenheit() float64
}

var w Warmer = Temp{Celsius: 37}
fmt.Println(w.Fahrenheit()) // 98.6
```

**8. Interfaces are satisfied implicitly.** `Temp` never says `implements Warmer`; having the method is enough. This is Ruby's duck typing ("if it quacks..."), except the compiler checks it: assign a value without the right methods to an interface variable and the code won't compile. Because nobody has to declare anything, an interface can be written long after the types that satisfy it, even in a different package.

There is one twist, the *method set*. A `Temp` value has only the value-receiver methods; a `*Temp` has both kinds. So if `Warmer` listed `Warm`, a plain `Temp` would not satisfy it, but `&Temp{}` would. Whether a value can sit in an interface depends on the receivers you chose.

**9. Interface values.** Under the hood an interface value is a pair: a concrete value and its type. `fmt.Printf("%v %T", w, w)` prints both halves. The type half is what decides which method runs.

```go
var w Warmer = Temp{Celsius: 10}
fmt.Printf("%v %T\n", w, w) // {10} main.Temp
```

**10. nil inside, nil outside.** An interface holding a nil pointer is not itself nil: its type half is set. Methods still get called (with a nil receiver), which is fine if the method checks for nil. An interface with nothing at all in it *is* nil, and calling a method on that panics, which means the program stops with a runtime error. Ruby's `nil.foo` raising `NoMethodError` is the closest match.

```go
var p *Temp       // a nil pointer
var w Warmer = p  // the interface holds (nil, *Temp)
fmt.Println(w == nil) // false
var empty Warmer
fmt.Println(empty == nil) // true
```

**11. The empty interface.** `interface{}` lists no methods, so every value satisfies it. Go spells it `any`. Use it when a function must accept values of types it can't know in advance, as `fmt.Println` does; you then need the next two topics to find out what you got.

```go
var x any = 42
x = "now a string"
x = Temp{}
fmt.Println(x) // {0}
```

**12. Type assertions.** `x.(T)` gets the concrete value of type `T` out of an interface. If `x` holds something else the plain form panics, so normally you use the two-result "comma ok" form, which never panics and tells you whether it worked. `T` can also be an interface type, which asks "does the value inside have these methods?".

```go
var x any = "hello"
s, ok := x.(string)  // "hello", true
n, ok := x.(int)     // 0, false
w, ok := x.(Warmer)  // nil, false: a string has no Fahrenheit
```

**13. Type switches.** A *type switch* runs several assertions in a row. It looks like a `switch` with `x.(type)` as its value, and in each case the variable you named has that case's type. Cases are tried top to bottom and the first match wins, which matters when one value fits several cases (any value with a `String` method also fits a case for an interface that lists `String`). `case nil` matches an empty interface. This is Ruby's `case x when Integer ... when String ...`.

```go
func kind(x any) string {
	switch v := x.(type) {
	case bool:
		return fmt.Sprintf("bool %v", v)
	case Warmer:
		return fmt.Sprintf("%g F", v.Fahrenheit())
	default:
		return fmt.Sprintf("something else: %T", v)
	}
}
```

**14. Stringers.** The `fmt` package defines one of Go's most common interfaces, `fmt.Stringer`: anything with `String() string`. When you print a value with `Println`, `Sprint` or `%v`, fmt checks for that method and uses it, like Ruby's `to_s`. Give it a value receiver and it works for both values and pointers. The verb `%g` prints a float as briefly as possible (`3`, `0.5`), where `%f` would print `3.000000`.

```go
func (t Temp) String() string {
	return fmt.Sprintf("%g°C", t.Celsius)
}

fmt.Println(Temp{Celsius: 21.5}) // 21.5°C
```

**15. Errors.** Go has no exceptions for ordinary failures. A function that can fail returns an extra result of the built-in interface type `error`, which has one method, `Error() string`, and `nil` means "no error". Callers check it right away. Ruby would `raise` and `rescue`; Go makes the failure a value you can see in the signature.

```go
n, err := strconv.Atoi("12a")
if err != nil {
	fmt.Println("bad number:", err)
}
```

**16. Error types of your own.** Any type with an `Error() string` method is an error, so you can carry extra fields, such as which file or URL failed. Pointer receivers are the norm for struct errors. If yours wraps another error, add `Unwrap() error` returning it: then `errors.Is(err, target)` (is this error, or anything it wraps, `target`?) and `errors.As(err, &ptr)` (find an error of this type in the chain and put it in `ptr`) can see through it. `fmt.Errorf` with the verb `%w` wraps an error the same way.

```go
type QuotaError struct {
	User string
	Err  error
}

func (e *QuotaError) Error() string {
	return e.User + ": " + e.Err.Error()
}

func (e *QuotaError) Unwrap() error { return e.Err }

err := fmt.Errorf("upload: %w", &QuotaError{"gerard", io.ErrShortWrite})
var qe *QuotaError
fmt.Println(errors.As(err, &qe), qe.User) // true gerard
fmt.Println(errors.Is(err, io.ErrShortWrite)) // true
```

A trap from the Tour: inside an `Error` method, don't pass the receiver itself to `fmt.Sprint` or `%v`. fmt sees an error, calls `Error`, which calls fmt, and the program recurses until it crashes. Convert it to its underlying type first (for a `type E float64`, use `float64(e)`). `go vet` spots this mistake for you.

**17. Readers.** `io.Reader` is the interface for "a stream of bytes you can read from": files, network connections, HTTP bodies and strings (`strings.NewReader`) are all readers. Its one method is `Read(p []byte) (n int, err error)`. Each call fills some of `p` (anything from one byte to `len(p)`) and returns how many bytes it wrote, `n`, and at the end it returns the error `io.EOF`. Two rules catch people out: only `p[:n]` holds data, and a reader may return `n > 0` together with an error, so handle the bytes before looking at the error. `io.ReadAll(r)` reads until EOF for you. Ruby's `IO#read` and `StringIO` are the nearest relatives.

```go
r := strings.NewReader("Hello, Reader!")
buf := make([]byte, 8)
for {
	n, err := r.Read(buf)
	fmt.Printf("%q ", buf[:n])
	if err == io.EOF {
		break
	}
}
// "Hello, R" "eader!" ""
```

**18. Wrapping a reader.** Because `io.Reader` is so small, a struct holding one reader can be a reader itself, changing or watching the bytes as they pass through. Readers chain like Unix pipes, and nothing has to know what's at the far end. This one counts bytes on the way past:

```go
type countingReader struct {
	r     io.Reader
	total int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.total += n
	return n, err
}
```

**19. rot13.** rot13 is the oldest joke in cryptography: replace every letter with the one 13 places later, wrapping round from `z` to `a` (`a` becomes `n`, `n` becomes `a`). Because the alphabet has 26 letters, doing it twice gives back the original, so encoding and decoding are the same operation. A `byte` is a number and a character literal like `'a'` is too, so letters can be shifted with arithmetic: `'a' + 2` is `'c'`, and `b - 'a'` is a letter's position in the alphabet, 0 to 25. The remainder operator `%` is how you wrap round (`27 % 26` is 1). Leave everything that isn't `a`-`z` or `A`-`Z` alone.

```go
var b byte = 'h'
fmt.Println(b-'a', string(rune(b+1))) // 7 i
```

## What to do

1. Create `primer/shapes/shapes.go` with `package shapes`. You can split the work into several files (`shapes.go`, `errors.go`, `describe.go`, `rot13.go`); they're all one package.
2. Define `Shape` with `Area() float64` and `Perimeter() float64`.
3. Define `Rect` (fields `W` and `H`) and `Circle` (field `R`), all float64. Give both value-receiver `Area` and `Perimeter` methods. `math.Pi` is pi.
4. Give `Rect` a pointer-receiver `Scale(f float64)` that multiplies both sides by `f`.
5. Give both a value-receiver `String()`: `Rect(3x4)` and `Circle(r=1.5)`, numbers printed with `%g`.
6. Write `TotalArea(shapes ...Shape) float64`. The `...` makes it *variadic*: callers pass any number of shapes, and inside it `shapes` is a `[]Shape`. A caller with a slice passes it as `TotalArea(list...)`.
7. Write `FetchError` with fields `URL string` and `Err error`, and pointer-receiver methods `Error()`, returning `fetch <URL>: <Err>`, and `Unwrap()`, returning `Err`. P.6's crawler reports its failures with it.
8. Write `type ErrNegativeSqrt float64` with a value-receiver `Error()` giving `cannot Sqrt negative number: -2`, and `Sqrt(x float64) (float64, error)`: for a negative `x` return 0 and `ErrNegativeSqrt(x)`, otherwise `math.Sqrt(x)` and nil.
9. Write `Describe(v any) string` with a type switch, cases in this order:
   - `nil` gives `nil`; an `int` gives `int 42`; a `string` gives `string "hi"` (use `%q`, which adds the quotes)
   - an `error` gives `error: ` and its message
   - a `Shape` gives `shape with area 12` (`%g`)
   - a `fmt.Stringer` gives `stringer ` and its `String()`
   - anything else gives `other ` and its type, from `%T` (`other float64`)
10. Write `Rot13Reader`, a struct with one field `R io.Reader`, and a value-receiver `Read(p []byte) (int, error)` that reads from `R` into `p` and rot13s the bytes it got.
11. Write `CountWords(r io.Reader) (map[string]int, error)`: read everything with `io.ReadAll`, then return `collections.WordCount` of it. If reading fails, return nil and the error.
12. Keep `gofmt -l .` and `go vet ./primer/...` quiet.

## Example

```go
r := shapes.Rect{W: 3, H: 4}
r.Area(), r.Perimeter()        // 12, 14
fmt.Println(r)                 // Rect(3x4)
r.Scale(2)                     // r is now Rect(6x8)

shapes.TotalArea(shapes.Rect{W: 1, H: 2}, shapes.Circle{R: 1})
// 5.141592653589793

_, err := shapes.Sqrt(-2)
fmt.Println(err)               // cannot Sqrt negative number: -2

err = &shapes.FetchError{URL: "/a", Err: io.ErrUnexpectedEOF}
fmt.Println(err)               // fetch /a: unexpected EOF
errors.Is(err, io.ErrUnexpectedEOF) // true

shapes.Describe(42)            // "int 42"
shapes.Describe(r)             // "shape with area 48"
shapes.Describe(time.Second)   // "stringer 1s"
shapes.Describe(3.5)           // "other float64"

s := strings.NewReader("Lbh penpxrq gur pbqr!")
io.Copy(os.Stdout, shapes.Rot13Reader{R: s})
// You cracked the code!

shapes.CountWords(strings.NewReader("to be or not to be"))
// map[be:2 not:1 or:1 to:2], nil
```

## Done when

- [ ] `Rect` and `Circle` have the right areas and perimeters, and plain values of both satisfy `Shape`
- [ ] `Scale` changes the rect it's called on, and only `*Rect` has it
- [ ] Both shapes print as `Rect(3x4)` and `Circle(r=1.5)`, as values, pointers and inside slices
- [ ] `FetchError` and `ErrNegativeSqrt` are errors with the exact messages, and `errors.Is` and `errors.As` see through `FetchError`
- [ ] `Describe` puts every kind of value in the right case, in the right order
- [ ] `Rot13Reader` decodes correctly however the inner reader hands out its bytes
- [ ] `CountWords` works on any reader, passes read errors back, and imports `burrow/primer/collections`

## Reference

```go
// primer/shapes
package shapes

type Shape interface {
	Area() float64
	Perimeter() float64
}

type Rect struct{ W, H float64 }
type Circle struct{ R float64 }

// Value receivers: Area, Perimeter, String ("Rect(3x4)",
// "Circle(r=1.5)", numbers with %g).
func (r Rect) Area() float64
func (r *Rect) Scale(f float64) // multiplies W and H by f

func TotalArea(shapes ...Shape) float64 // 0 for no shapes

type FetchError struct {
	URL string
	Err error
}

func (e *FetchError) Error() string // "fetch " + URL + ": " + Err
func (e *FetchError) Unwrap() error // Err

type ErrNegativeSqrt float64

// "cannot Sqrt negative number: " + the number with %v
func (e ErrNegativeSqrt) Error() string

// (0, ErrNegativeSqrt(x)) for x < 0, else (math.Sqrt(x), nil)
func Sqrt(x float64) (float64, error)

// Cases in order: nil, int, string, error, Shape, fmt.Stringer,
// anything else ("other " + %T).
func Describe(v any) string

type Rot13Reader struct{ R io.Reader }

func (r Rot13Reader) Read(p []byte) (int, error)

// io.ReadAll(r), then collections.WordCount. A read error gives
// (nil, err).
func CountWords(r io.Reader) (map[string]int, error)
```

## The tests check

- Area and perimeter of three rects and three circles, to within 1e-9
- At run time, with a message instead of a compile error: `Rect{}` and `Circle{}` values satisfy `Shape`, `*Rect` does too, `*Rect` has `Scale` and a plain `Rect` does not
- `Scale(2)` then `Scale(0.5)` change the rect in place
- `TotalArea` with no shapes, one, and a spread slice mixing values and pointers
- Printing rects and circles as values, pointers, with `%v` and inside a `[]Shape`
- `FetchError`'s message, `errors.Unwrap`, `errors.Is` to its cause, and `errors.As` through a `%w` wrap
- `Sqrt` of six non-negative and three negative numbers: the value, a 0 with every error, the exact message, and `errors.As` into `ErrNegativeSqrt`
- `Describe` for 16 values: nil, ints, strings, three kinds of error, `Rect`, `*Rect`, `Circle`, a `time.Duration` (a Stringer), `float64`, `int64`, `bool` and a slice; a `Rect` described as a stringer gets a hint about case order
- rot13 of mixed text, both directions, non-letters, a non-ASCII letter and the empty string; rot13 twice is the original
- `Rot13Reader` passes `iotest.TestReader`, works one byte at a time and when data arrives together with `io.EOF`, touches only `p[:n]`, and returns the inner reader's error
- `CountWords` agrees with `collections.WordCount`, counts a `Rot13Reader`'s decoded words, and returns a failing reader's error
- `primer/shapes` imports `burrow/primer/collections`

## Try it

```bash
./burrow watch
go test -v -run TestLP_4_Describe ./primer/shapes  # a subtest each
go test -v -run 'TestLP_4_Rot13ReaderContract/one' ./primer/shapes
```

`go doc io.Reader` and `go doc fmt.Stringer` print the real definitions; both are a few lines long. `go doc testing/iotest` shows the misbehaving readers the tests use against yours.

## In the wild

`io.Reader` and `io.Writer` are the most used interfaces in Go: `gzip.NewReader`, `bufio.NewReader`, `cipher.StreamReader` and `http.MaxBytesReader` are all readers wrapping readers, exactly like `Rot13Reader`. `fs.PathError` and `url.Error` are struct errors with an `Unwrap` method, the same shape as `FetchError`.

## New in Go

methods and receivers · methods are functions · method expressions · methods on non-struct types · value vs pointer receivers · automatic & and * on method calls · method sets · interfaces · implicit satisfaction · interface values (value, type) · nil inside an interface vs a nil interface · any (the empty interface) · type assertions and comma ok · type switches · fmt.Stringer · %g, %q, %T · the error interface · custom error types · Unwrap, errors.Is, errors.As · the recursive Error trap · variadic functions · io.Reader · io.EOF · wrapping readers · io.ReadAll

Standard library: `fmt`, `math`, `io`, `errors`, `strings`
