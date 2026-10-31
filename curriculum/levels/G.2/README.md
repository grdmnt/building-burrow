# G.2 · Contract

> A protocol written down once, in a file both sides compile from, cannot drift.

## Your goal

Write Burrow's gRPC contract: `kv.proto`, a protobuf schema with the `Entry` and `Event` messages from G.1, the request and response messages, and a `KV` service with six methods. Then turn it into Go. `buf generate` runs two code generators, `protoc-gen-go` for the messages and `protoc-gen-go-grpc` for the service, and writes package `burrowpb`. All of it lives in a second Go module, `burrow/grpc`, in the `grpc/` folder, because gRPC is a library from outside the standard library and your root module stays standard library only.

The proof that you got the schema right is G.1: `proto.Marshal` of a generated `Entry` must produce exactly the bytes `pbwire.MarshalEntry` produces.

## What you're using from before

**Protobuf codec (G.1 · Wire Format).** You already know what the schema means on the wire. `string key = 1;` is "field 1, wire type LEN", `uint64 version = 3;` is "field 3, VARINT". The schema in this level has to give `Entry` and `Event` the same numbers, types and enum names that `pbwire` hard-codes:

```proto
message Entry {
  string key = 1;
  bytes value = 2;
  uint64 version = 3;
  int64 expires_at_unix_nano = 4; // 0 means no expiry
}
```

**Tool directive (M.1 · Oscilloscope).** The two code generators are Go programs. You add them to `grpc/go.mod` the same way you added staticcheck: `go get -tool`. Then `go tool protoc-gen-go` runs exactly the version `go.mod` pins, on every machine. That matters here more than for a linter: the generated code calls into the protobuf runtime library, and generator and runtime come from the same module, so pinning one pins both.

## What's new here

**A second module.** `grpc/go.mod` declares `module burrow/grpc`. It needs your code from the root module, which isn't published anywhere, so it requires `burrow v0.0.0` and a `replace` points that requirement at the folder above:

```text
module burrow/grpc

go 1.24

require burrow v0.0.0

replace burrow => ../
```

Now `import "burrow/store"` in `grpc/` compiles your root module's `store`. The root module never imports `grpc/`, and `go test ./...` at the root doesn't enter a folder with its own `go.mod`, so the root stays standard library only and `go list -deps ./...` from M.1 still shows nothing else. Because the import path `burrow/grpc/...` starts with `burrow/`, the `internal` rule lets `burrow/grpc` import `burrow/internal/testkit`, which the tests in this chapter use.

**Pinning gRPC to your Go version.** grpc-go raises its minimum Go version as Go releases come out. v1.80 is the last release that builds with Go 1.24; later ones need Go 1.25 or 1.26. If you run `go get google.golang.org/grpc@latest` on Go 1.24, the `go` command raises the `go` line in `grpc/go.mod` and downloads a newer toolchain to build with (that's `GOTOOLCHAIN=auto`, the default). Either is fine. Pin `@v1.80` to stay on your Go, or let it download. Only the `grpc/` module changes; the root module stays on 1.24.

**A `.proto` file.** proto3 syntax is short. A `message` lists fields with a type, a name and a number. The number is what goes on the wire, so it can never change once anything has been sent; the name is only for code. An `enum` lists named numbers, and proto3 wants the first to be 0. A `service` lists `rpc` methods, each taking one message and returning one, and `stream` in front of the return type makes the server send a sequence:

```proto
syntax = "proto3";

package burrow.v1;

option go_package = "burrow/grpc/burrowpb;burrowpb";

service KV {
  rpc Get(GetRequest) returns (GetResponse);
  rpc Watch(WatchRequest) returns (stream Event);
}
```

`package burrow.v1` is the protobuf name space: the service's full name is `burrow.v1.KV`, which is what goes into every request's path (`/burrow.v1.KV/Get`). The `v1` is there so a `v2` can live next to it one day. `go_package` is the Go import path of the generated code, then `;` and the package name.

Every method gets its own request and response message, even `DeleteResponse {}` with no fields. A method that returns a shared type can never grow its own fields; an empty message can.

**proto3 presence: `optional`.** proto3 writes nothing for a zero value, and a reader can't tell "0" from "not sent". Usually that's fine. For a conditional write it isn't: `if_version` unset means "write unconditionally", 0 means "only if the key doesn't exist", and 7 means "only if it is at version 7". That's exactly 7.2's `IfVersion *uint64`. Marking the field `optional` gives it explicit presence: the generator makes it a pointer, and a set 0 is written on the wire.

```proto
message PutRequest {
  string key = 1;
  bytes value = 2;
  optional uint64 if_version = 3;
  int64 ttl_ms = 4; // 0: no expiry
}
```

```go
req := &burrowpb.PutRequest{Key: "k", Value: v,
	IfVersion: proto.Uint64(0)} // create only
if req.IfVersion == nil { /* unconditional */ }
v := req.GetIfVersion() // 0 when unset: careful
```

This is how the schema folds `CompareAndSwap` into `Put` and `CompareAndDelete` into `Delete`, the same move 7.1 made with `If-Match`. `WatchRequest.after` is `optional` for the same reason: unset means "live events only", 0 means "everything you still have". `ttl_ms` doesn't need it, because 0 already means "no TTL".

Every generated message has `Get` methods (`GetKey()`, `GetEntry()`) that return the zero value on a nil message. Use them: `resp.GetEntry().GetVersion()` never panics.

**Enum value names.** Protobuf enum values share one name space per package, not per enum, so the convention (and the default `buf lint` rule) prefixes each value with the enum's name: `EVENT_TYPE_PUT`, not `PUT`. In Go that becomes `burrowpb.EventType_EVENT_TYPE_PUT`.

**buf and the two plugins.** `protoc` is the original protobuf compiler, and every language's generator is a plugin it runs. `buf` is a newer front end for the same plugins: it finds your `.proto` files, checks them (`buf lint`, and `buf breaking` against the last commit), and runs the plugins listed in `buf.gen.yaml`. Install it once, outside Go: `brew install bufbuild/buf/buf`, or `go install github.com/bufbuild/buf/cmd/buf@latest`. Then the plugins go into `grpc/go.mod` as tools:

```bash
cd grpc
go get -tool google.golang.org/protobuf/cmd/protoc-gen-go@latest
go get -tool google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
```

`buf.yaml` says where the `.proto` files live. Making `proto/` the module root is what makes the file's name `burrow/v1/kv.proto` (not `proto/burrow/v1/kv.proto`) and the Go variable `File_burrow_v1_kv_proto`:

```yaml
version: v2
modules:
  - path: proto
lint:
  use:
    - STANDARD
```

`buf.gen.yaml` runs each plugin as `go tool <name>`, so buf uses the versions `go.mod` pins and never a stray `protoc-gen-go` on your `PATH`. `module=burrow/grpc` strips the module path from `go_package`, so the output lands in `./burrowpb/`:

```yaml
version: v2
plugins:
  - local: ["go", "tool", "protoc-gen-go"]
    out: .
    opt: module=burrow/grpc
  - local: ["go", "tool", "protoc-gen-go-grpc"]
    out: .
    opt: module=burrow/grpc
```

**Generated code is committed.** `buf generate` writes `burrowpb/kv.pb.go` (the messages) and `burrowpb/kv_grpc.pb.go` (a `KVClient` interface with `NewKVClient`, a `KVServer` interface, `UnimplementedKVServer` and `RegisterKVServer`). Both start with `// Code generated ... DO NOT EDIT.`, which tools and reviewers recognise, and a header naming the generator versions. Commit them, so nobody needs buf just to build. Never edit them; change `kv.proto` and run `buf generate` again. A comment like `//go:generate sh -c "cd .. && buf generate"` in a small `burrowpb/generate.go` makes `go generate ./...` rebuild them.

## What to do

1. Create the module: `mkdir grpc && cd grpc && go mod init burrow/grpc`. Add `require burrow v0.0.0` and `replace burrow => ../` (with `go mod edit -require=burrow@v0.0.0 -replace=burrow=../`, or by hand).
2. `go get google.golang.org/grpc@v1.80` (or `@latest` and accept the toolchain download).
3. Add the two plugins with `go get -tool`, as above. Install buf.
4. Write `grpc/proto/burrow/v1/kv.proto` from the Reference: `Entry`, `EventType`, `Event`, the request and response messages, the `KV` service. Field numbers and enum names exactly as given; `Entry` and `Event` must match G.1.
5. Write `grpc/buf.yaml` and `grpc/buf.gen.yaml`. Run `buf lint`, then `buf generate`, both in `grpc/`.
6. `burrow start` installs the test, so run `go mod tidy` in `grpc/` to record what it imports, then `go test -race ./...` in `grpc/`. Commit `go.mod`, `go.sum`, the proto, both yaml files and `burrowpb/`.

## Example

```bash
$ cd grpc
$ buf lint
$ buf generate
$ ls burrowpb
kv.pb.go  kv_grpc.pb.go
$ head -5 burrowpb/kv.pb.go
// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.36.11
// 	protoc        (unknown)
// source: burrow/v1/kv.proto
```

Your versions will differ; they are whatever `grpc/go.mod` pins. The generated message and your hand-written codec agree:

```go
e := &burrowpb.Entry{Key: "user:1", Value: []byte("ann"),
	Version: 3}
b, _ := proto.Marshal(e)
fmt.Printf("% x\n", b)
// 0a 06 75 73 65 72 3a 31 12 03 61 6e 6e 18 03
fmt.Printf("% x\n", pbwire.MarshalEntry(store.Entry{
	Key: "user:1", Value: []byte("ann"), Version: 3}))
// 0a 06 75 73 65 72 3a 31 12 03 61 6e 6e 18 03
```

## Done when

- [ ] `grpc/go.mod` declares `module burrow/grpc`, requires `burrow v0.0.0` with `replace burrow => ../`, and has `tool` lines for `protoc-gen-go` and `protoc-gen-go-grpc`
- [ ] `kv.proto` is registered as `burrow/v1/kv.proto` in package `burrow.v1`, proto3, with `go_package` `burrow/grpc/burrowpb`
- [ ] Every message has the fields in the Reference, by number, name and type; `if_version` and `after` are `optional`, nothing else is
- [ ] `EventType` numbers `EVENT_TYPE_UNSPECIFIED` to `EVENT_TYPE_READY` as 0 to 4
- [ ] `KV` has the six methods with their request and response types; only `Watch` streams
- [ ] `proto.Marshal` of `Entry` and `Event` gives exactly `pbwire`'s bytes, both ways
- [ ] `buf.gen.yaml` runs both plugins through `go tool`, and the versions in the generated headers match `go.mod`

## Reference

```proto
// grpc/proto/burrow/v1/kv.proto
syntax = "proto3";
package burrow.v1;
option go_package = "burrow/grpc/burrowpb;burrowpb";

message Entry {
  string key = 1;
  bytes value = 2;
  uint64 version = 3;
  int64 expires_at_unix_nano = 4; // 0: no expiry
}

enum EventType {
  EVENT_TYPE_UNSPECIFIED = 0;
  EVENT_TYPE_PUT = 1;
  EVENT_TYPE_DELETE = 2;
  EVENT_TYPE_EXPIRE = 3;
  EVENT_TYPE_READY = 4;
}

message Event {
  uint64 id = 1;
  EventType type = 2;
  Entry entry = 3;
}

message GetRequest { string key = 1; }
message GetResponse { Entry entry = 1; }

message PutRequest {
  string key = 1;
  bytes value = 2;
  // unset: unconditional; 0: create only; n: only at version n
  optional uint64 if_version = 3;
  int64 ttl_ms = 4; // 0: no expiry
}
message PutResponse { Entry entry = 1; }

message DeleteRequest {
  string key = 1;
  optional uint64 if_version = 2; // unset: unconditional
}
message DeleteResponse {}

message TouchRequest {
  string key = 1;
  int64 ttl_ms = 2;
}
message TouchResponse { Entry entry = 1; }

message KeysRequest { string prefix = 1; }
message KeysResponse { repeated string keys = 1; }

message WatchRequest {
  string prefix = 1;
  optional uint64 after = 2; // unset: live only
}

service KV {
  rpc Get(GetRequest) returns (GetResponse);
  rpc Put(PutRequest) returns (PutResponse);
  rpc Delete(DeleteRequest) returns (DeleteResponse);
  rpc Touch(TouchRequest) returns (TouchResponse);
  rpc Keys(KeysRequest) returns (KeysResponse);
  rpc Watch(WatchRequest) returns (stream Event);
}
```

```text
grpc/
  go.mod            module burrow/grpc; require burrow v0.0.0;
                    replace burrow => ../; tool protoc-gen-go and
                    protoc-gen-go-grpc
  go.sum
  buf.yaml          modules: - path: proto
  buf.gen.yaml      both plugins as local: ["go", "tool", ...],
                    out: ., opt: module=burrow/grpc
  proto/burrow/v1/kv.proto
  burrowpb/kv.pb.go       generated, committed
  burrowpb/kv_grpc.pb.go  generated, committed
```

Decisions the tests rely on:

- The tests read the schema through the generated descriptors (`protoregistry`), not the Go structs, so a schema mistake shows up as a message rather than a build failure.
- The `.pb.go` header's `protoc-gen-go` version must equal the `google.golang.org/protobuf` version in `grpc/go.mod`, and the `protoc-gen-go-grpc` version must equal the tool's module version there. A plugin from your `PATH` fails this; `go tool` passes.
- The tests never pin a grpc or protobuf version: any release that works with your Go is fine.
- The Marshal comparison uses only valid UTF-8 keys (protobuf rejects invalid UTF-8 in `string` fields; `pbwire` doesn't check).

## The tests check

- The registered file is `burrow/v1/kv.proto`, package `burrow.v1`, proto3, with `go_package` `burrow/grpc/burrowpb`
- Every message's fields by number, name, kind, repetition, message or enum type, and presence (`optional` exactly on `PutRequest.if_version`, `DeleteRequest.if_version` and `WatchRequest.after`)
- `EventType` values 0 to 4 by name
- Service `KV`: six methods with their input and output types; `IsStreamingServer()` only for `Watch`, no client streaming
- `proto.Marshal` (deterministic) of five entries and four events equals `pbwire.MarshalEntry` and `MarshalEvent`; each side decodes the other's bytes back to the same value
- `go mod edit -json` on `grpc/go.mod`: module path, `burrow v0.0.0`, `replace burrow => ../`, both `tool` lines
- `buf.gen.yaml` runs both plugins through `go tool`
- Both generated files carry the `DO NOT EDIT` header, and their generator versions match `go.mod`

## Try it

```bash
cd grpc
buf lint
buf generate && git status burrowpb
go test -race ./burrowpb
buf breaking --against '.git#subdir=grpc'   # after a commit
```

## In the wild

Every gRPC API is a `.proto` file first: googleapis, etcd's `rpc.proto`, Kubernetes' CRI. buf is what most teams use to lint, check for breaking changes and generate, and `buf.build` hosts schemas the way the Go proxy hosts modules.

## New in Go

a second module with replace · pinning a dependency to your Go version · proto3 syntax · optional and explicit presence · go tool plugins in buf.gen.yaml · generated code with DO NOT EDIT · protoreflect descriptors

Standard library: `none new; google.golang.org/protobuf and google.golang.org/grpc`
