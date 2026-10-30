# M.1 · Oscilloscope

A bench instrument that inspects the circuit without becoming part of it. Add `staticcheck` to the module as a Go 1.24 `tool` dependency, learn what `go.sum` and `go mod tidy` are actually for, and prove that a tool dependency never reaches the binary: the `burrow` module stays standard-library only. Everything else in this level is reading, and all of it is the module-management half of a real Go job.

## Build

```go
// go.mod, after `go get -tool honnef.co/go/tools/cmd/staticcheck@latest`:
//
//   module burrow
//
//   go 1.24
//
//   tool honnef.co/go/tools/cmd/staticcheck
//
//   require (
//       honnef.co/go/tools v0.6.x  // staticcheck releases are named by year (2025.1); the module version is semver
//       ... // indirect lines the tool needs, written by go get, tidied by go mod tidy
//   )
//
// go.sum appears next to it: one line per module version with the hash of its contents and
// one with the hash of its go.mod, so a later download that differs is refused.
//
// `go tool staticcheck ./...` builds the tool from the pinned version (cached after the first
// run) and runs it. No GOPATH/bin, no version drift between machines, no `go install` step in
// the README. `go list -deps ./...` from the root still shows nothing outside the standard
// library: tool requirements sit in go.mod but are not in any package's import graph.
```

- `go get -tool honnef.co/go/tools/cmd/staticcheck@latest` in the root module: a `tool` directive, `require` lines and a `go.sum`
- `go mod tidy -diff` clean, `go tool -n staticcheck` resolving, `go tool staticcheck ./...` running
- Fix what staticcheck reports (ungraded, but do it: every finding on this codebase so far is real)
- Read the rest of this page; none of it is graded, all of it will come up at work

Decisions the tests rely on:

- The `tool` directive is in the root `go.mod` (the `burrow` module), added with `go get -tool`, not typed by hand. Hand-typed directives leave `go.sum` incomplete; `go get` and `go mod tidy` write both files.
- `go.sum` is committed. It is not a lock file in the npm sense (`go.mod` already pins exact versions, and Go builds always use exactly the versions in `go.mod`, with no resolution step), it is the list of hashes `go` checks every download against. Deleting it and re-running `go mod tidy` regenerates it from the checksum database, which is why it must agree with `go.mod`: `go mod tidy -diff` printing anything, or exiting non-zero, fails the level.
- Every package in the `burrow` module keeps depending only on the standard library. `go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./...` prints nothing. The tool's modules appear in `go.mod` and `go.sum` and nowhere else.

What `go.sum` is backed by: when `go` downloads a module version for the first time it asks the checksum database at `sum.golang.org` for the hash, verifies the download against it, and then records it in `go.sum`. Every later machine and CI run checks the same hash, so a module author (or a compromised proxy) cannot change the contents of a published version without every builder noticing. `GOSUMDB=off` turns the check off; `GONOSUMDB` and `GOPRIVATE` exempt private module paths, which also keeps their names from being sent to the public proxy. `GOPROXY` is `https://proxy.golang.org,direct` by default; a corporate proxy such as Athens or Artifactory goes first in that list. `GOFLAGS=-mod=mod` lets a build update `go.mod` on the fly, which you want never; `-mod=readonly` is the default and is what CI should run with.

What `go mod tidy` does: it walks every package in the module, including tests and tool directives, adds every module those imports need and removes every requirement nothing uses, then rewrites `go.sum` to match. Run it after any import change, and read its diff. `go mod tidy -diff` (Go 1.23) shows what it would do without doing it, which is the CI check. `go mod why -m <module>` explains why a module is required; `go mod graph` prints the whole requirement graph; `go list -m -u all` shows available upgrades, and `go get <module>@<version>` (or `@latest`, or `@v1`, or a commit hash) moves one.

Related and worth a look, each the same mechanism as the tool directive:

- `govulncheck`: `go get -tool golang.org/x/vuln/cmd/govulncheck@latest` then `go tool govulncheck ./...` reports known vulnerabilities in the functions your binary actually calls, from the Go vulnerability database, not just modules you require. Pure standard-library code gets findings too: it checks the Go version.
- `go generate`: a `//go:generate go tool stringer -type=Op` comment in `wire/` (after `go get -tool golang.org/x/tools/cmd/stringer`) writes `op_string.go` so every `Op` prints as `OpGet` instead of `1`. `go generate ./...` runs every such comment; nothing runs it for you, so commit the output.
- `go run pkg@version`: a one-off run of a tool with no change to `go.mod`, when you don't want to keep it.
- `go work`: a `go.work` file that makes several modules in one checkout build against each other without `replace` lines. Q.4 uses it for the Service Bus module. `go.work` is for your machine; add it to `.gitignore`.

## The tests check

- The root go.mod has a `tool honnef.co/go/tools/cmd/staticcheck` directive and a `go 1.24` (or newer) line
- go.sum exists and has entries for `honnef.co/go/tools` (both the module hash and the `/go.mod` hash)
- `go mod tidy -diff` exits 0 with no output
- `go tool -n staticcheck` exits 0 and prints the path of a built binary
- `go list -deps` over every package in the module reports only standard-library packages: a tool dependency never reaches the binary
- Running `go tool staticcheck ./...` and fixing its findings is encouraged and ungraded

## Try it

```bash
go get -tool honnef.co/go/tools/cmd/staticcheck@latest
go tool staticcheck ./...
go mod tidy -diff; echo "exit: $?"
go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./...   # prints nothing
go mod why -m honnef.co/go/tools
```

## Uses

Nothing from earlier levels. This is a root part.

## In the wild

Every Go repo at work has this go.mod shape; before Go 1.24 the same thing was done with a `tools.go` file of blank imports behind a build tag, which you will still see; golangci-lint bundles staticcheck with a hundred other checkers

## New in Go

go get -tool and the tool directive · go.sum and the checksum database · go mod tidy and -diff · go mod why · GOPROXY, GOSUMDB and GOPRIVATE · go tool, go run pkg@version · govulncheck · go generate · go work

Standard library: `none new; the Go command itself`
