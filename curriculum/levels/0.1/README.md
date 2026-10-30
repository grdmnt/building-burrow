# 0.1 · Power On

Create the `burrow` module and a `burrowd` binary with a proper command-line shape. Every later level plugs a command into this entry point, so the testable `run` function is worth getting right now.

## Build

```go
// cmd/burrowd/main.go
package main

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// run is the whole program. It never calls os.Exit, which makes it testable.
func run(args []string, stdout, stderr io.Writer) int

// Commands and exit codes:
//   burrowd version | -version | --version   stdout: `burrowd 0.1.0\n`   exit 0
//   burrowd help | -h | --help               stdout: usage               exit 0
//   burrowd                                  stderr: usage               exit 2
//   burrowd bogus                            stderr: unknown command "bogus" + usage   exit 2
//   burrowd version now                      stderr: error naming `now` exit 2
//   burrowd --frobnicate                     stderr: error naming the flag exit 2
// Usage starts with exactly `usage: burrowd <command>` and lists `version`.
```

- `go mod init burrow` with `go 1.24` in go.mod (done for you)
- `cmd/burrowd/main.go` with `run(args, stdout, stderr) int`
- `version`, `help` and usage errors with the right exit codes and streams
- A README line stating the rules: standard library only, every test runs with -race, database/sql is out of scope because the standard library ships no driver
- `gofmt` and `go vet ./...` clean, from this level on

Worth knowing now:

- `burrow start` put a helper package at `internal/testkit`. A package under a directory named `internal` can only be imported from inside the tree rooted at that directory's parent: `burrow/internal/testkit` is importable by every package in the `burrow` module and by nobody outside it. That is the whole mechanism; there is no keyword. Use it for your own helpers too.
- `database/sql` is the one everyday standard-library package this curriculum never touches, because the standard library ships no driver and the rule is standard library only. Learn it on a work project; the shape (`sql.DB` is a pool, `QueryContext`, `Scan`, always close `Rows`) is a day's reading once you know Go.
- `go vet` is the compiler's suspicious cousin: it catches `Printf` verbs that don't match their arguments, copied locks, unreachable code and misuse of `context`. It runs as part of `go test` for a small subset of checks; the full set is `go vet ./...`, which 0.1's tests run over the whole module.

## The tests check

- Exact stdout including the trailing newline for version and its flag spellings
- Errors go to stderr only, normal output to stdout only
- Exit 2 for no command, unknown command, unknown flag, extra argument; 0 for version and help
- The unknown-command and unknown-flag messages name the offending token
- `go build` the real binary and check `main` passes run()'s result to os.Exit (exit status 2 for `bogus`)
- Every .go file outside curriculum/ is gofmt-clean
- `go vet ./...` over the whole module reports nothing (a sibling of the gofmt test; both keep running against everything you write later)

## Try it

```bash
go run ./cmd/burrowd version
go run ./cmd/burrowd bogus; echo "exit: $?"
gofmt -l . && go vet ./...
```

## Uses

Nothing from earlier levels. This is a root part.

## In the wild

spf13/cobra and urfave/cli generate this shape; the run(args, stdout, stderr) pattern is what makes any CLI testable

## New in Go

modules · package main · os.Args · io.Writer · exit codes · gofmt · go vet · go run vs go build · internal/ packages · database/sql (the one to learn elsewhere)

Standard library: `fmt`, `os`, `io`
