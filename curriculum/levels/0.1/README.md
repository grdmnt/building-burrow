# 0.1 · Power On

> Every machine starts with a power switch. Yours is a program that starts, says what it is, and exits cleanly.

## Your goal

Write `burrowd`, a tiny command-line program that only knows two commands, `version` and `help`. It has to print to the right place (normal output or errors) and exit with the right number, because shells and scripts rely on both.

**Why the name?** Burrow is the key-value store you're building. The `d` stands for *daemon*, the Unix name for a long-running server program (`sshd`, `httpd`). For now `burrowd` only prints its version; it grows into the server one level at a time.

## What you're using from before

Nothing yet. This is the first part.

## What's new here

**A testable `main`.** Go starts every program at `func main()` in `package main`, and a program reports success or failure with its exit code: 0 means it worked, anything else means it didn't (2 is the convention for "you used it wrong"). The catch is that `os.Exit` ends the process immediately, so a test can't call code that calls it.

The fix is to keep `main` to one line and put everything else in a function that *returns* the exit code and *writes* to whatever it's given:

```go
func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
```

`io.Writer` is the interface for "something you can write bytes to". The real program passes `os.Stdout` and `os.Stderr`; a test passes two `bytes.Buffer`s and then reads back what was printed:

```go
var out, errOut bytes.Buffer
code := run([]string{"version"}, &out, &errOut)
// code == 0, out.String() == "burrowd 0.1.0\n", errOut.String() == ""
```

**Two output streams.** stdout is for the program's normal output, stderr is for errors and complaints. Keeping them apart is what lets someone pipe your output into another program without error messages mixed in.

## What to do

1. Create `cmd/burrowd/main.go` with `package main`.
2. Write the one-line `main` shown above.
3. Write `run(args []string, stdout, stderr io.Writer) int`. `args` is what came after the program name: for `burrowd version` it's `[]string{"version"}`.
4. Handle these cases:
   - `version`, `-version` or `--version`: print `burrowd 0.1.0` to stdout, return 0.
   - `help`, `-h` or `--help`: print the usage text to stdout, return 0.
   - No arguments: print the usage text to stderr, return 2.
   - An unknown command like `bogus`: print `unknown command "bogus"` and the usage text to stderr, return 2.
   - An unknown flag like `--frobnicate`, or an extra word like `version now`: print an error that names it to stderr, return 2.
5. Make the usage text start with exactly `usage: burrowd <command>` and mention `version`. The rest of it is up to you.
6. Run `gofmt -w .` (formats your code) and `go vet ./...` (flags code that compiles but is almost certainly wrong) until both print nothing.

## Example

```bash
$ go run ./cmd/burrowd version
burrowd 0.1.0
$ go run ./cmd/burrowd bogus
unknown command "bogus"
usage: burrowd <command>

commands:
  version   print the version
  help      show this help
$ echo $?
2
$ go run ./cmd/burrowd version now
burrowd: version takes no arguments, got "now"
```

To see the two streams separately, hide one: `go run ./cmd/burrowd version > /dev/null` shows nothing (stdout hidden), and so does `go run ./cmd/burrowd bogus 2> /dev/null` (stderr hidden).

## Done when

- [ ] `burrowd version` prints exactly `burrowd 0.1.0` and a newline, and nothing else
- [ ] Help goes to stdout with exit 0; every mistake goes to stderr with exit 2
- [ ] Error messages name the word that was wrong (`bogus`, `--frobnicate`, `now`)
- [ ] The built binary really exits with 2 for `bogus` (so `main` passes `run`'s result to `os.Exit`)
- [ ] `gofmt -l .` and `go vet ./...` print nothing

## Reference

The exact contract the tests hold you to. `go.mod` (module `burrow`, `go 1.24`) is already there.

```go
// cmd/burrowd/main.go
package main

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// run is the whole program. It never calls os.Exit.
func run(args []string, stdout, stderr io.Writer) int
```

| You run | Prints | To | Exit |
|---|---|---|---|
| `burrowd version`, `-version`, `--version` | `burrowd 0.1.0\n` | stdout | 0 |
| `burrowd help`, `-h`, `--help` | usage | stdout | 0 |
| `burrowd` | usage | stderr | 2 |
| `burrowd bogus` | `unknown command "bogus"` + usage | stderr | 2 |
| `burrowd version now` | an error naming `now` | stderr | 2 |
| `burrowd --frobnicate` | an error naming the flag | stderr | 2 |

From this level on, everything you write must stay `gofmt`-clean and pass `go vet ./...`.

## The tests check

- Exact stdout including the trailing newline for version and its flag spellings
- Errors go to stderr only, normal output to stdout only
- Exit 2 for no command, unknown command, unknown flag, extra argument; 0 for version and help
- The unknown-command and unknown-flag messages name the offending token
- `go build` the real binary and check `main` passes run()'s result to os.Exit (exit status 2 for `bogus`)
- Every .go file outside curriculum/ is gofmt-clean
- `go vet ./...` over the whole module reports nothing (both checks keep running against everything you write later)

## Try it

```bash
./burrow watch             # re-runs the tests every time you save
go run ./cmd/burrowd version
go run ./cmd/burrowd bogus; echo "exit: $?"
```

## In the wild

spf13/cobra and urfave/cli generate this shape; the `run(args, stdout, stderr)` pattern is what makes any CLI testable.

## New in Go

modules · package main · os.Args · io.Writer · exit codes · stdout vs stderr · gofmt · go vet · go run vs go build

Standard library: `fmt`, `os`, `io`
