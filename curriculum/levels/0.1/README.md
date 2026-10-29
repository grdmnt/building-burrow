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

## The tests check

- Exact stdout including the trailing newline for version and its flag spellings
- Errors go to stderr only, normal output to stdout only
- Exit 2 for no command, unknown command, unknown flag, extra argument; 0 for version and help
- The unknown-command and unknown-flag messages name the offending token
- `go build` the real binary and check `main` passes run()'s result to os.Exit (exit status 2 for `bogus`)
- Every .go file outside curriculum/ is gofmt-clean

## Try it

```bash
go run ./cmd/burrowd version
go run ./cmd/burrowd bogus; echo "exit: $?"
```

## Uses

Nothing from earlier levels. This is a root part.

## In the wild

spf13/cobra and urfave/cli generate this shape; the run(args, stdout, stderr) pattern is what makes any CLI testable

## New in Go

modules · package main · os.Args · io.Writer · exit codes · gofmt · go run vs go build

Standard library: `fmt`, `os`, `io`
