# Q.4 · The Real Thing

Add a dependency on purpose and point the same scenarios at the real Azure Service Bus emulator. M.1 already taught `go get`, `go.sum` and `go mod tidy` for a tool; this is the same machinery for a library the binary links, kept in its own module so the `burrow` module stays standard-library only.

## Build

```go
// azcompare/go.mod is its own module: `cd azcompare && go mod init burrow/azcompare`, then
// `go get github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus@latest`.
// The root go.mod does not require the SDK and `go list -deps ./...` from the root stays std-only
// (M.1's test keeps checking that). To use burrow's packages from azcompare, add a `replace burrow => ../`
// line, or make the repo a workspace: `go work init . ./azcompare` (go.work is local, don't commit it).
// A `tool` directive in azcompare/go.mod gives it `go tool staticcheck` too.
```

- Emulator in Docker with config.json
- A separate module with its own `go.mod` and `go.sum`; `go mod tidy -diff` clean; a `replace` or `go.work` to reach burrow's packages
- Port scenarios
- Difference table

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/Q.4/files/`, matching the earlier chapters.

## Pass when

The same scenario suite passes against Burrow and the emulator, or fails in ways your table explains.

The suite will cover:

- Scenario suite against both backends (emulator tests skip without Docker)

## Uses

- Competing consumers (built in Q.3 · Competing Consumers)
- Tool directive (built in M.1 · Oscilloscope)

## In the wild

github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus; this is also the module-management lesson for every work project

## New in Go

require vs tool directives · replace and go.work for a multi-module repo · third-party SDKs · build tags for integration tests

Standard library: `azservicebus (Azure SDK; the one dependency in the curriculum)`
