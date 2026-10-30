# M.1 · Oscilloscope

> A bench instrument inspects the circuit without becoming part of it.

## Your goal

Add `staticcheck`, the most widely used Go linter, to this project the modern way: as a *tool dependency* recorded in `go.mod`, so every machine runs the same version with `go tool staticcheck` and nobody has to install anything. Along the way learn what `go.sum` and `go mod tidy` are for, because every Go repo at work has them and most people only half understand them. The one thing to prove is that a tool dependency never reaches the binary: `burrowd` stays standard-library only. The rest of this level is reading, ungraded, and all of it comes up at work.

## What you're using from before

Nothing from earlier levels. This is a root part. It touches only `go.mod` and `go.sum` at the module root.

## What's new here

**Modules, `go.mod` and `require`.** A module is a tree of packages with one `go.mod` at its root, and the module path (`burrow`) is the prefix of every import path inside it. `require` lines name other modules and their exact versions. There is no resolution step at build time: Go builds with exactly the versions `go.mod` lists, which is why there is no separate lock file.

**`go get -tool` and the `tool` directive.** Before Go 1.24, keeping a linter at a pinned version meant a `tools.go` file of blank imports behind a build tag, which you will still see in older repos. Now one command writes a `tool` line plus the `require` lines the tool needs:

```bash
go get -tool honnef.co/go/tools/cmd/staticcheck@latest
```

and `go tool staticcheck ./...` builds that exact version (cached after the first time) and runs it. `go tool -n staticcheck` prints the path of the built binary without running it. A tool is a build-time dependency: it sits in `go.mod` but no package imports it, so it is never linked into anything you ship.

**`go.sum` and the checksum database.** The first time `go` downloads a module version it asks the checksum database at `sum.golang.org` for that version's hash, verifies the download against it, and records two lines in `go.sum`: the hash of the module's contents and the hash of its `go.mod`. Every later build on every machine checks downloads against those lines, so a module author or a compromised proxy cannot change a published version without every builder noticing. `go.sum` is committed and never edited by hand; `go get` and `go mod tidy` write it. Deleting it and running `go mod tidy` regenerates it from the database, which is why it must always agree with `go.mod`.

**`go mod tidy`.** It walks every package in the module, tests and tool directives included, adds every module those imports need, removes every requirement nothing uses, and rewrites `go.sum` to match. Run it after any import change and read its diff. `go mod tidy -diff` prints what tidy *would* change and exits 1 if there is anything, which is the CI check:

```bash
$ go mod tidy -diff; echo "exit: $?"
exit: 0
```

**Seeing why a module is there.** `go mod why -m <module>` prints the import chain that pulls a module in. `go mod graph` prints the whole requirement graph. `go list -m -u all` shows available upgrades, and `go get <module>@<version>` (or `@latest`, `@v1`, or a commit hash) moves one. The template below is the "is anything outside the standard library linked in?" check. It prints every package that is not in the standard library, and on this module that should be only `burrow/...` packages:

```bash
go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./...
```

**The environment knobs.** `GOPROXY` is `https://proxy.golang.org,direct` by default; a corporate proxy such as Athens or Artifactory goes first in that list. `GOSUMDB=off` turns the checksum check off, which you want never. `GONOSUMDB` and `GOPRIVATE` exempt private module paths from the public database and the public proxy, which also keeps their names from being sent there. `-mod=readonly` is the default and means a build fails rather than editing `go.mod`; `GOFLAGS=-mod=mod` lets builds update `go.mod` on the fly, and CI should never run with it.

**Related, same mechanism.** Each of these is the tool directive again:

- `govulncheck`: `go get -tool golang.org/x/vuln/cmd/govulncheck@latest`, then `go tool govulncheck ./...` reports known vulnerabilities in functions your binary actually calls, from the Go vulnerability database. Pure standard-library code gets findings too, because it checks your Go version.
- `go generate`: a `//go:generate go tool stringer -type=Op` comment (after `go get -tool golang.org/x/tools/cmd/stringer`) writes `op_string.go` so every `Op` prints as `OpGet` instead of `1`. `go generate ./...` runs every such comment; nothing runs it for you, so commit the output.
- `go run pkg@version`: a one-off run of a tool with no change to `go.mod`, for when you don't want to keep it.
- `go work`: a `go.work` file that makes several modules in one checkout build against each other without `replace` lines. It is for your machine; add it to `.gitignore`.

## What to do

1. In the module root, run `go get -tool honnef.co/go/tools/cmd/staticcheck@latest`. Look at what changed: `git diff go.mod` and the new `go.sum`.
2. Run `go mod tidy`, then `go mod tidy -diff` until it prints nothing and exits 0.
3. Run `go tool -n staticcheck` to see where the binary was built, then `go tool staticcheck ./...`.
4. Fix what it reports. This is ungraded, but every finding on this codebase so far has been real.
5. Run the `go list -deps` check above and confirm it prints only `burrow/...` packages.
6. Read the rest of this page if you skipped it. None of it is graded.

## Example

```bash
$ go get -tool honnef.co/go/tools/cmd/staticcheck@latest
go: added honnef.co/go/tools v0.6.1
go: added golang.org/x/exp/typeparams v0.0.0-20231108232855-2478ac86f678
go: added golang.org/x/tools v0.30.0
...
$ git diff go.mod
+tool honnef.co/go/tools/cmd/staticcheck
+
+require (
+	honnef.co/go/tools v0.6.1
+	golang.org/x/tools v0.30.0 // indirect
+	...
+)
$ go tool staticcheck ./...
store/memory.go:118:2: this value of err is never used (SA4006)
wire/frame.go:41:6: func readN is unused (U1000)
$ go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./...
burrow/store
burrow/wire
...
```

The versions and findings will differ. The last command lists only `burrow/...` packages: the tool's modules are in `go.mod` but nowhere in the import graph.

## Done when

- [ ] `go.mod` has a `tool honnef.co/go/tools/cmd/staticcheck` line, written by `go get -tool`
- [ ] `go.sum` exists next to it and has lines for `honnef.co/go/tools`
- [ ] `go mod tidy -diff` prints nothing and exits 0
- [ ] `go tool -n staticcheck` prints a path and exits 0
- [ ] `go list -deps` over the module shows only `burrow/...` and standard-library packages

## Reference

The exact contract the tests hold you to.

```go
// go.mod, after
//   go get -tool honnef.co/go/tools/cmd/staticcheck@latest
//
//   module burrow
//
//   go 1.24
//
//   tool honnef.co/go/tools/cmd/staticcheck
//
//   require (
//       honnef.co/go/tools v0.6.x  // staticcheck releases are named by
//                                  // year (2025.1); the module version
//                                  // is semver
//       ... // indirect lines the tool needs, written by go get, tidied
//           // by go mod tidy
//   )
//
// go.sum appears next to it: one line per module version with the hash
// of its contents and one with the hash of its go.mod, so a later
// download that differs is refused.
//
// `go tool staticcheck ./...` builds the tool from the pinned version
// (cached after the first run) and runs it. No GOPATH/bin, no version
// drift between machines, no `go install` step in the README.
// `go list -deps ./...` from the root still shows nothing but the
// standard library and burrow/...: tool requirements sit in go.mod but
// are not in any package's import graph.
```

Decisions the tests rely on:

- The `tool` directive is in the root `go.mod` (the `burrow` module), added with `go get -tool`, not typed by hand. Hand-typed directives leave `go.sum` incomplete; `go get` and `go mod tidy` write both files.
- `go.sum` is committed. It is not a lock file in the npm sense (`go.mod` already pins exact versions, and Go builds always use exactly the versions in `go.mod`, with no resolution step); it is the list of hashes `go` checks every download against. `go mod tidy -diff` printing anything, or exiting non-zero, fails the level.
- Every package in the `burrow` module keeps depending only on the standard library. `go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./...` prints nothing but `burrow/...` packages. The tool's modules appear in `go.mod` and `go.sum` and nowhere else.
- The tests run `go` with `GOFLAGS` cleared and `GOWORK=off`, so a `go.work` file or a `-mod` flag in your environment doesn't change the result.

## The tests check

- The root go.mod has a `tool honnef.co/go/tools/cmd/staticcheck` directive (read with `go mod edit -json`)
- go.sum exists and has entries for `honnef.co/go/tools`
- `go mod tidy -diff` exits 0 with no output
- `go tool -n staticcheck` exits 0 and prints the path of a built binary
- `go list -deps` over every package in the module reports only `burrow/...` and standard-library packages: a tool dependency never reaches the binary
- Running `go tool staticcheck ./...` and fixing its findings is encouraged and ungraded

## Try it

```bash
./burrow test
go get -tool honnef.co/go/tools/cmd/staticcheck@latest
go tool staticcheck ./...
go mod tidy -diff; echo "exit: $?"
go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./...
go mod why -m honnef.co/go/tools
```

## In the wild

Every Go repo at work has this go.mod shape; before Go 1.24 the same thing was done with a `tools.go` file of blank imports behind a build tag, which you will still see; golangci-lint bundles staticcheck with a hundred other checkers

## New in Go

go get -tool and the tool directive · go.sum and the checksum database · go mod tidy and -diff · go mod why · GOPROXY, GOSUMDB and GOPRIVATE · go tool, go run pkg@version · govulncheck · go generate · go work

Standard library: `none new; the Go command itself`
