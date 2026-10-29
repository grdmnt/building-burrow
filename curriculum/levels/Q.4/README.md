# Q.4 · The Real Thing

Add a dependency on purpose, learn modules with `go.sum`, and point the same scenarios at the real Azure Service Bus emulator.

## Build

```go
// go mod init burrow/azcompare; go get github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus
```

- Emulator in Docker with config.json
- Separate module, go.sum, go mod tidy, go vet
- Port scenarios
- Difference table

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/Q.4/files/`, matching the earlier chapters.

## Pass when

The same scenario suite passes against Burrow and the emulator, or fails in ways your table explains.

The suite will cover:

- Scenario suite against both backends (emulator tests skip without Docker)

## Uses

- Competing consumers (built in Q.3 · Competing Consumers)

## In the wild

github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus; this is also the module-management lesson for every work project

## New in Go

modules with dependencies · go.sum and go mod tidy · third-party SDKs · build tags for integration tests

Standard library: `azservicebus (Azure SDK; the one dependency in the curriculum)`
