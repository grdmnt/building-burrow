# 0.1 · Power On

Create the `burrow` module and a `burrowd` binary with a proper command-line shape. Every later level plugs into this entry point, so it's worth getting right now.

## Build

```go
// cmd/burrowd/main.go
package main

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the whole program. It never calls os.Exit itself, which makes it testable.
func run(args []string, stdout, stderr io.Writer) int
```

| Command | stdout | stderr | exit |
|---|---|---|---|
| `burrowd version` (or `-version`, `--version`) | `burrowd 0.1.0` | nothing | 0 |
| `burrowd help` (or `-h`, `--help`) | usage | nothing | 0 |
| `burrowd` (no command) | nothing | usage | 2 |
| `burrowd bogus` | nothing | `unknown command "bogus"` + usage | 2 |
| `burrowd version now` | nothing | an error naming `now` | 2 |

Usage text starts with exactly `usage: burrowd <command>` and lists the `version` command.

## The tests check

- Exact output, including the trailing newline.
- Errors go to stderr, normal output to stdout, never both.
- Exit code 2 for usage mistakes, 0 for success. (Unix convention: 1 is for runtime failures.)
- Unknown flags such as `--frobnicate` are usage errors too, and the message names the flag.
- The real binary: the test runs `go build` and checks `main` passes `run`'s result to `os.Exit`.
- Every `.go` file in the repo is `gofmt`-clean.

## Try it

```bash
go run ./cmd/burrowd version
go run ./cmd/burrowd bogus; echo "exit: $?"
```

## New in Go

modules · `package main` · `os.Args` · `io.Writer` · exit codes · `gofmt`
