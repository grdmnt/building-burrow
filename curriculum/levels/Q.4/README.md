# Q.4 · The Real Thing

> Every part on the board so far is one you made. This level buys one from the shop and checks it fits the socket.

## Your goal

Run the same tests against your broker and against the real thing. Azure Service Bus is Microsoft's hosted message broker; the Service Bus *emulator* is a Docker container that speaks the same protocol, and `azservicebus` is the Go SDK (a client library) that talks to both. You will add that SDK as the curriculum's one third-party dependency, keep it in a module of its own so the `burrow` module stays standard-library only, and write a scenario suite that runs against both backends. Where they differ, you write it down in a table.

Two things get learned. How a message broker you built compares to a product people pay for, name by name. And how Go dependencies really work in a multi-module repository, which is the part of this level you will use at work every week.

## What you're using from before

**Competing consumers (Q.3 · Competing Consumers).** The finished broker and its client: `Send`, batch peek-lock `ReceiveBatch`, `Complete`, `Abandon`, `DeadLetter`, `RenewLock`, and a `Receiver` with prefetch. It is one of the two backends under test, and its client is what the Burrow side of the scenario interface calls:

```go
type Backend interface {
    Send(ctx context.Context, queue string, body []byte) error
    Receive(ctx context.Context, queue string, max int) ([]Msg, error)
    Complete(ctx context.Context, m Msg) error
    Abandon(ctx context.Context, m Msg) error
    DeadLetter(ctx context.Context, m Msg, reason string) error
    RenewLock(ctx context.Context, m Msg) error
}
```

**Tool directive (M.1 · Oscilloscope).** `go get` writing `go.mod` and `go.sum`, `go mod tidy -diff` as the check, and the rule that nothing outside the standard library reaches a `burrow` package (`go list -deps` proves it). Here the same commands add a library instead of a tool, in a second module, and M.1's check keeps passing on the first:

```bash
go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./...
# from the repo root: still prints nothing
```

## What's new here

**`require` versus `tool`.** M.1's `tool honnef.co/go/tools/cmd/staticcheck` line pulled a module into `go.mod` without any package importing it. A `require github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus v1.x` line is the ordinary kind: it is there because a `.go` file imports a package from it, and that package is compiled into your binary. `go get <module>@latest` adds the line; `go mod tidy` adds the indirect lines it needs and writes `go.sum`. Both kinds pin an exact version; the difference is whether the code links in.

**A second module in one repository.** A module is a directory with a `go.mod`, and the packages under it. A repository can hold several. Put the comparison in `azcompare/` with its own `go.mod`, so the SDK is required there and nowhere else. The `burrow` module's `go.mod` never changes, and `go list -deps` from the root stays standard-library only.

The catch is that `azcompare` needs to import `burrow/queue`. Two ways to point one local module at another:

```text
# azcompare/go.mod: a replace line sends the import to a directory
require burrow v0.0.0
replace burrow => ../

# or, at the repo root, a workspace file (Go 1.18+)
go work init . ./azcompare
```

A `replace` is committed and travels with the module. A `go.work` is for your machine: it tells every `go` command in the tree to build the listed modules against each other, and it goes in `.gitignore` (CI and other developers run `go work init` themselves, or use the `replace`). Pick one; `go work` is cleaner for a repo you control, `replace` for a module others consume.

**Using a third-party SDK.** Read its documentation on pkg.go.dev, not its source, first. The names you need map one to one onto your broker:

| Burrow | azservicebus |
|---|---|
| `Send` | `Sender.SendMessage` |
| `ReceiveBatch(max)` | `Receiver.ReceiveMessages(ctx, max, nil)` |
| `Complete(token)` | `Receiver.CompleteMessage(ctx, msg, nil)` |
| `Abandon(token)` | `Receiver.AbandonMessage` |
| `DeadLetter(token, reason, desc)` | `Receiver.DeadLetterMessage` with `DeadLetterOptions` |
| `RenewLock(token)` | `Receiver.RenewMessageLock` |
| peek-lock vs receive-and-delete | `ReceiveModePeekLock` vs `ReceiveModeReceiveAndDelete` |

The SDK's `ReceivedMessage` has the fields your `Message` copied: `MessageID`, `SequenceNumber`, `EnqueuedTime`, `DeliveryCount`, `LockToken`, `ApplicationProperties`. Most are pointers, because the wire format can omit them; `*string` nil means "not set". Expect to write `if m.ContentType != nil { ... }` more than you would like.

```go
client, err := azservicebus.NewClientFromConnectionString(connStr, nil)
recv, err := client.NewReceiver("orders", &azservicebus.ReceiverOptions{
    ReceiveMode: azservicebus.ReceiveModePeekLock,
})
msgs, err := recv.ReceiveMessages(ctx, 10, nil)
err = recv.CompleteMessage(ctx, msgs[0], nil)
```

**Build tags for integration tests.** A test that needs Docker should not run under a plain `go test ./...`. A build constraint is a comment before the `package` line that includes the file only when a tag is given:

```go
//go:build integration

package azcompare
```

`go test -tags integration ./azcompare` compiles it in; without the tag the file does not exist as far as the compiler is concerned. Inside, skip cleanly when the emulator isn't reachable, so the suite is green on a machine without Docker rather than red:

```go
c, err := net.DialTimeout("tcp", "localhost:5672", time.Second)
if err != nil {
    t.Skip("service bus emulator not running on :5672")
}
c.Close()
```

**The emulator.** Two containers: the emulator itself and a SQL Edge database it stores state in, both from Microsoft's registry, started with `docker compose`. Queues are declared ahead of time in a `Config.json` mounted into the container, with the same knobs you built: `LockDuration` (`"PT1M"`) and `MaxDeliveryCount` (`10`). The SDK connects with `Endpoint=sb://localhost;SharedAccessKeyName=RootManageSharedAccessKey;SharedAccessKey=SAS_KEY_VALUE;UseDevelopmentEmulator=true;` on port 5672. The emulator keeps nothing across restarts, so the `kill -9` scenario only makes sense against Burrow; say so in the table rather than skipping it quietly.

## What to do

1. `mkdir azcompare && cd azcompare && go mod init burrow/azcompare`, then `go get github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus@latest`. Commit `go.mod` and `go.sum`.
2. Point it at the root module: `replace burrow => ../` in `azcompare/go.mod`, or `go work init . ./azcompare` at the root with `go.work` in `.gitignore`.
3. Optionally `go get -tool honnef.co/go/tools/cmd/staticcheck@latest` inside `azcompare` too, so `go tool staticcheck ./...` works there.
4. Write `azcompare/docker-compose.yml` and `Config.json` with a namespace and the queues your scenarios use (`orders` at least), `LockDuration` `PT5S` or similar so lock-expiry scenarios finish quickly, `MaxDeliveryCount` 10.
5. Define the `Backend` interface and two implementations: one over your `queue.Client`, one over `azservicebus`. Map the names as in the table.
6. Port the scenarios from Q.1 to Q.3 to run through `Backend`: send and receive in order, peek-lock expiry and redelivery with `DeliveryCount`, `ErrLockLost` on a late complete, dead-letter after `MaxDeliveryCount`, three competing consumers. Use real time; there is no fake clock inside a container.
7. Put the emulator tests behind `//go:build integration` and skip when port 5672 is closed. Run `go test -tags integration ./...` from `azcompare` with the emulator up.
8. Write `azcompare/DIFFERENCES.md`: one row per scenario, Burrow result, emulator result, and why they differ when they do (persistence across restart, lock token format, what `Abandon` does to ordering, sequence numbers after dead-lettering).
9. From the root: `go mod tidy -diff` in both modules prints nothing; `go list -deps` from the root is still standard-library only.

## Example

```bash
$ cd azcompare && docker compose up -d
$ go test -tags integration -run Scenario -v ./...
=== RUN   TestScenario/burrow/order
=== RUN   TestScenario/burrow/lock_expiry
=== RUN   TestScenario/emulator/order
=== RUN   TestScenario/emulator/lock_expiry
--- PASS: TestScenario (14.21s)
$ docker compose down
$ go test ./...              # no tag: the emulator tests don't exist
ok      burrow/azcompare    0.31s
$ cd ..
$ go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./...
$
```

A row of the table:

| Scenario | Burrow | Emulator | Why |
|---|---|---|---|
| kill -9, restart, receive | all 1,000 | n/a | emulator state is not persisted |
| abandon then receive | same message first | same message first | both requeue at the front |

## Done when

- [ ] `azcompare/go.mod` requires `azservicebus`; the root `go.mod` does not
- [ ] `go list -deps` from the root still shows only standard-library packages
- [ ] `go mod tidy -diff` prints nothing in either module
- [ ] The scenario suite passes against Burrow, and against the emulator when it is running
- [ ] Without the `integration` tag or without Docker, `go test ./...` in `azcompare` is green, not red
- [ ] `DIFFERENCES.md` explains every scenario that behaves differently
- [ ] `gofmt -l .` and `go vet ./...` print nothing

## Reference

```go
// azcompare/go.mod is its own module:
//   cd azcompare && go mod init burrow/azcompare
//   go get \
//   github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus@latest
// The root go.mod does not require the SDK, and `go list -deps ./...`
// from the root stays std-only (M.1's test keeps checking that).
// To use burrow's packages from azcompare, add a
// `replace burrow => ../` line, or make the repo a workspace:
// `go work init . ./azcompare` (go.work is local, don't commit it).
// A `tool` directive in azcompare/go.mod gives it `go tool staticcheck`
// too.
```

- Emulator in Docker with config.json
- A separate module with its own `go.mod` and `go.sum`; `go mod tidy -diff` clean; a `replace` or `go.work` to reach burrow's packages
- Port scenarios
- Difference table

## The tests check

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/Q.4/files/`, matching the earlier chapters.

The suite will cover:

- Scenario suite against both backends (emulator tests skip without Docker)

## Try it

```bash
./burrow test
cd azcompare && docker compose up -d
go test -tags integration -v ./...
go mod tidy -diff; echo "exit: $?"
cd ..
go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./...
```

## In the wild

github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus; this is also the module-management lesson for every work project

## New in Go

require vs tool directives · a second module in one repo · replace and go.work · third-party SDKs and pointer fields · build tags for integration tests · t.Skip

Standard library: `nothing new`. Third party: `azservicebus` (the Azure SDK; the one dependency in the curriculum)
