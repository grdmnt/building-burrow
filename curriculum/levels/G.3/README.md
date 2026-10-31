# G.3 · Front Panel

> The same five buttons as the HTTP API, wired to a different panel: typed messages in, status codes out.

## Your goal

Implement the `KV` service from G.2 over any `store.Store`: `Get`, `Put`, `Delete`, `Touch` and `Keys`. Most of the work is the error model. gRPC has no 404 or 412; it has sixteen status codes, and every store error has to become the right one, with the reason attached so a client can tell two errors with the same code apart. On the way in, the client's deadline arrives as the handler's context, and passing it to the store is what makes a slow store give up. Then wrap it in a real server with a request size limit, the standard health service and server reflection, and a binary, `burrow-grpc`, that serves it on a port.

## What you're using from before

**Service contract (G.2 · Contract).** `burrowpb.KVServer` is the interface you implement, and `burrowpb.RegisterKVServer` connects it to a `*grpc.Server`. Requests and responses are the generated structs: read with the `Get` methods, build with literals.

**Memory store (1.1 · Register File), CAS (1.3 · Versions), Expiry (1.4 · Decay).** The server is a thin translation layer over `store.Store`; it never stores anything itself. Each request picks one store call:

```text
Put  if_version unset, ttl_ms 0   Set
     if_version unset, ttl_ms n   SetWithTTL(n ms)
     if_version set,   ttl_ms 0   CompareAndSwap
     if_version set,   ttl_ms n   CompareAndSwapWithTTL
Delete  if_version unset          Delete
        if_version n > 0          CompareAndDelete(n)
Touch   ttl_ms                    Touch
```

The store already validates keys and TTLs and returns its sentinels (`ErrInvalidKey`, `ErrInvalidTTL`, `ErrNotFound`, `ErrVersionMismatch`). Don't validate again in the server; map what comes back. `ExpiresAt` goes out as `UnixNano()`, and a zero time as 0.

**Deadline guard (7.4 · Watchdog).** In 7.4 a middleware put the deadline on the request's context. In gRPC the client does that: a call made with a 100 ms context sends a `grpc-timeout: 100m` header, and the server's handler gets a context that ends then, or sooner if the client cancels or hangs up. The rule from 7.4 is unchanged: pass `ctx` to every store call, and a store that blocks gives up with `ctx.Err()`.

## What's new here

**Implementing a generated interface.** `protoc-gen-go-grpc` generates `KVServer` with all six methods plus an unexported one, and an `UnimplementedKVServer` struct whose methods all return `codes.Unimplemented`. Embed it by value. Your struct then satisfies the interface, methods you haven't written yet (`Watch`, until G.5) answer `Unimplemented`, and a method added to the `.proto` later doesn't break your build:

```go
type Server struct {
	burrowpb.UnimplementedKVServer
	Store store.Store
}

func (s *Server) Get(ctx context.Context,
	req *burrowpb.GetRequest) (*burrowpb.GetResponse, error) {
	e, err := s.Store.Get(ctx, req.GetKey())
	if err != nil {
		return nil, toStatus(err, false)
	}
	return &burrowpb.GetResponse{Entry: toProto(e)}, nil
}
```

**A gRPC server.** `grpc.NewServer(opts...)` makes the server, `Register...` adds services to it, and `Serve(lis)` accepts connections on any `net.Listener` until `Stop` or `GracefulStop`. One server can host many services; each request names its service and method in its path, `/burrow.v1.KV/Get`. Under the hood every call is an HTTP/2 stream, and each message on it is G.1's 5-byte frame.

```go
gs := grpc.NewServer(grpc.MaxRecvMsgSize(kvserver.MaxRecvMsgSize))
burrowpb.RegisterKVServer(gs, srv)
lis, _ := net.Listen("tcp", "127.0.0.1:50051")
go gs.Serve(lis)
// ...
gs.GracefulStop() // stop accepting, wait for running calls
```

**Status codes.** A handler returns an `error`. If it was made by `status.Error(code, msg)`, the client receives that code and message; any other error arrives as `codes.Unknown`. `status.Code(err)` reads the code back on either side. Burrow's mapping:

```text
store.ErrInvalidKey, ErrInvalidTTL     InvalidArgument
Delete with if_version 0               InvalidArgument
store.ErrNotFound, unconditional       NotFound
store.ErrNotFound, if_version set      Aborted
store.ErrVersionMismatch               Aborted
context.DeadlineExceeded / Canceled    DeadlineExceeded / Canceled
store.ErrClosed                        Unavailable
anything else                          Internal
request over MaxRecvMsgSize            ResourceExhausted (grpc does it)
```

Why `Aborted` and not `FailedPrecondition`? gRPC's own guide to the codes answers it: use `Aborted` when "a client-specified test-and-set fails, indicating the client should restart a read-modify-write sequence", and `FailedPrecondition` when the client "should not retry until the system state has been explicitly fixed" (deleting a directory that isn't empty). A failed CAS is the textbook test-and-set: read again, try again. And as in 7.1, a conditional write on a missing key is a failed precondition like any other, so it's `Aborted` too; `NotFound` is only for unconditional operations.

Use `errors.Is`, not `==`: the store wraps its sentinels (`invalid key: empty`), and an engine might wrap them again. For the two context errors, `status.FromContextError(err)` builds the right status.

**Error details.** A code is coarse. `Aborted` covers both "wrong version" and "the key you made a condition on is gone", and a client that wants to hand back `store.ErrNotFound` or `store.ErrVersionMismatch` (G.6 does) can't tell them apart from the code alone. A status can carry typed detail messages, and `google.rpc.ErrorInfo` is the standard one: a machine-readable `Reason` in UPPER_SNAKE_CASE and a `Domain` saying whose reason it is. Every error mapped from a store sentinel carries one:

```go
import "google.golang.org/genproto/googleapis/rpc/errdetails"

st := status.New(codes.Aborted, err.Error())
st, _ = st.WithDetails(&errdetails.ErrorInfo{
	Reason: "VERSION_MISMATCH", Domain: kvserver.Domain})
return st.Err()
```

The reasons are `INVALID_KEY`, `INVALID_TTL`, `INVALID_VERSION` (delete with `if_version` 0, which can never match), `NOT_FOUND`, `VERSION_MISMATCH`, and `CANNOT_RESUME` (G.5). Domain is `"burrow.v1"`. `WithDetails` can only fail if the detail can't be marshalled; fall back to the plain status if it does.

**The size limit.** gRPC refuses to read a message over `MaxRecvMsgSize` (4 MiB by default) and answers `ResourceExhausted` without calling your handler. Burrow's values are at most 1 MiB, as in 7.1, so the limit is 1 MiB plus room for the key and the other fields: `1<<20 + 64<<10`.

**Health and reflection.** Two standard services every gRPC server should have. `grpc.health.v1.Health` answers "are you serving?" for load balancers and Kubernetes probes; `health.NewServer()` reports `SERVING` for `""` (the whole server) and you set it for `burrow.v1.KV`. Server reflection lets a tool ask the server for its schema, so `grpcurl` can call it with no `.proto` file:

```go
h := health.NewServer()
h.SetServingStatus("burrow.v1.KV",
	healthpb.HealthCheckResponse_SERVING)
healthpb.RegisterHealthServer(gs, h)
reflection.Register(gs)
```

```bash
$ grpcurl -plaintext localhost:50051 list
burrow.v1.KV
grpc.health.v1.Health
grpc.reflection.v1.ServerReflection
grpc.reflection.v1alpha.ServerReflection
```

**Testing without a port: bufconn.** `google.golang.org/grpc/test/bufconn` is a `net.Listener` backed by memory. The suite serves on one and dials it with a custom dialer, so tests run in parallel without ports or timeouts. You can do the same in your own tests:

```go
lis := bufconn.Listen(1 << 20)
go gs.Serve(lis)
conn, _ := grpc.NewClient("passthrough:///bufnet",
	grpc.WithContextDialer(func(ctx context.Context,
		_ string) (net.Conn, error) {
		return lis.DialContext(ctx)
	}),
	grpc.WithTransportCredentials(insecure.NewCredentials()))
kv := burrowpb.NewKVClient(conn)
```

`grpc.NewClient` doesn't connect until the first call. `passthrough:///` hands the name to the dialer unchanged instead of resolving it with DNS.

**A gateway binary.** `burrowd` stays standard library only, so it can't host gRPC. `burrow-grpc` lives in the `grpc` module and serves the KV service in front of any `store.Store`: by default the frame client from 3.3 connected to a running `burrowd`, or with `-mem` an in-process memory store. It is the same "a client is itself a Store" idea from 3.3, now used to put a new protocol in front of an old one.

## What to do

1. Create `grpc/kvserver/server.go` with `Server`, `MaxRecvMsgSize`, `Domain`, and the five unary methods. Write `toProto(store.Entry)` and `toStatus(err, conditional)` once and use them everywhere.
2. In `Put`, choose the store call from `req.IfVersion == nil` and `ttl_ms`, as in the table. In `Delete`, `if_version` 0 is `InvalidArgument` before the store is called.
3. Write `(s *Server) GRPCServer(opts ...grpc.ServerOption) *grpc.Server`: `grpc.NewServer` with `MaxRecvMsgSize` and then `opts`, register KV, health (`SERVING` for `burrow.v1.KV`) and reflection.
4. Create `grpc/cmd/burrow-grpc/main.go`: flags `-listen` (default `127.0.0.1:50051`), `-addr` (default `127.0.0.1:7070`) and `-mem`; any other argument is exit 2. Listen, print `grpc on <addr>` to stdout, serve. On SIGINT or SIGTERM, `GracefulStop`, falling back to `Stop` after 10 s, and exit 0.
5. Run `go mod tidy` and `go test -race ./...` in `grpc/`. Then try it with `grpcurl`.

## Example

```bash
$ go run ./cmd/burrow-grpc -mem
grpc on 127.0.0.1:50051
```

In another terminal (bytes fields are base64 in grpcurl's JSON):

```bash
$ grpcurl -plaintext -d '{"key":"greeting","value":"aGVsbG8="}' \
    localhost:50051 burrow.v1.KV/Put
{
  "entry": {
    "key": "greeting",
    "value": "aGVsbG8=",
    "version": "1"
  }
}
$ grpcurl -plaintext -d '{"key":"greeting","value":"eA==",
    "if_version":"5"}' localhost:50051 burrow.v1.KV/Put
ERROR:
  Code: Aborted
  Message: store: version mismatch
  Details:
  1)	{
      "@type": "type.googleapis.com/google.rpc.ErrorInfo",
      "domain": "burrow.v1",
      "reason": "VERSION_MISMATCH"
    }
$ grpcurl -plaintext -d '{"service":"burrow.v1.KV"}' \
    localhost:50051 grpc.health.v1.Health/Check
{
  "status": "SERVING"
}
```

## Done when

- [ ] `Put`, `Get`, `Delete`, `Touch` and `Keys` work over a connection, with versions, binary values and `expires_at_unix_nano` from the store's clock
- [ ] `if_version` unset, 0 and n behave as `Set`, create-only and CAS; with `ttl_ms` they expire
- [ ] Every store error has the code in the table and an `ErrorInfo` with domain `burrow.v1` and the right reason; failed writes change nothing
- [ ] A store that blocks makes a call with a 100 ms deadline fail with `DeadlineExceeded`, and the store sees its context end; a client cancel gives `Canceled`
- [ ] A 1 MiB value fits; a 2 MiB request is `ResourceExhausted`, and the server keeps serving
- [ ] Health reports `SERVING` for `""` and `burrow.v1.KV`; reflection lists the services and serves `burrow/v1/kv.proto`
- [ ] `burrow-grpc -mem -listen 127.0.0.1:0` prints `grpc on <addr>`, serves over TCP, and exits 0 on SIGINT

## Reference

```go
// grpc/kvserver
package kvserver

// MaxRecvMsgSize: a 1 MiB value plus room for the key and framing.
const MaxRecvMsgSize = 1<<20 + 64<<10

// Domain is the ErrorInfo domain on every store error.
const Domain = "burrow.v1"

type Server struct {
	burrowpb.UnimplementedKVServer
	Store store.Store
}

// GRPCServer returns a server with MaxRecvMsgSize, then opts, and KV,
// grpc.health.v1 (SERVING for "" and burrow.v1.KV) and reflection
// registered. The caller calls Serve.
func (s *Server) GRPCServer(opts ...grpc.ServerOption) *grpc.Server

// Errors: status code + errdetails.ErrorInfo{Reason, Domain}
//   ErrInvalidKey                  InvalidArgument   INVALID_KEY
//   ErrInvalidTTL                  InvalidArgument   INVALID_TTL
//   Delete with if_version 0       InvalidArgument   INVALID_VERSION
//   ErrNotFound (unconditional)    NotFound          NOT_FOUND
//   ErrNotFound (if_version set)   Aborted           NOT_FOUND
//   ErrVersionMismatch             Aborted           VERSION_MISMATCH
//   context errors                 DeadlineExceeded / Canceled
//   ErrClosed                      Unavailable
//   anything else                  Internal
// Matched with errors.Is. Put ttl_ms < 0 reaches the store, which
// says ErrInvalidTTL; Touch ttl_ms <= 0 likewise.

// grpc/cmd/burrow-grpc
//   burrow-grpc [-listen 127.0.0.1:50051] [-addr 127.0.0.1:7070]
//               [-mem]
//   first stdout line: grpc on <addr>
//   SIGINT/SIGTERM: GracefulStop (Stop after 10 s), exit 0
//   unexpected argument: exit 2
```

Decisions the tests rely on:

- The handler passes its own `ctx` to the store. The suite's slow store blocks until `ctx.Done()` and reports what it saw.
- A store error wrapping a sentinel (`fmt.Errorf("engine: %w", store.ErrNotFound)`) maps like the sentinel.
- `GRPCServer` must not call `Serve`; the tests serve it on an in-memory listener, and the binary on TCP.
- `Keys` with an empty prefix lists everything; a nil `Keys` result is fine.

## The tests check

- Put and Get round trip with versions and a binary value; Keys by prefix, sorted; Delete then NotFound
- `ttl_ms` gives `expires_at_unix_nano` = store clock + ttl on a `FakeClock`; Touch moves it and keeps the version; a Put with `ttl_ms` 0 clears it
- `if_version` 0 creates, 1 swaps, with `ttl_ms` it is a CAS with expiry; conditional Delete; unset `if_version` overwrites
- A table of fourteen requests: status code and ErrorInfo reason and domain for each, and nothing changed afterwards
- A fake store returning wrapped NotFound, ErrClosed, the two context errors and an unknown error: the five codes
- A store that blocks: DeadlineExceeded within the budget, Canceled on a client cancel, and the store observes both
- `MaxRecvMsgSize` is `1<<20 + 64<<10`; 1 MiB fits; 2 MiB is ResourceExhausted and the next call works
- 16 goroutines × 25 Puts and Gets over one connection, under `-race`
- Health `SERVING` for `""` and `burrow.v1.KV`; reflection's ListServices and FileContainingSymbol
- No goroutine leaks after `GracefulStop` and closing the connection (counted after a warm-up)
- The binary, on a real 127.0.0.1:0 port: the `grpc on` line, Put/Get, health, reflection, exit 0 on SIGINT; an extra argument is exit 2

## Try it

```bash
cd grpc
go test -race ./kvserver ./cmd/burrow-grpc
go run ./cmd/burrow-grpc -mem &
grpcurl -plaintext localhost:50051 describe burrow.v1.KV
grpcurl -plaintext -d '{"key":"a b"}' localhost:50051 burrow.v1.KV/Get
```

## In the wild

etcd's v3 API is exactly this shape (a KV service over its store, `Aborted`-style errors for failed transactions); Google's APIs attach `ErrorInfo` to every error (AIP-193); grpc-gateway and Envoy's gRPC-JSON transcoder put HTTP in front of the same services.

## New in Go

implementing a generated interface · embedding UnimplementedServer for forward compatibility · grpc.NewServer, Register, Serve · status and codes · error details with ErrorInfo · deadlines arriving as the handler's ctx · status.FromContextError · health and reflection · bufconn

Standard library: `context`, `errors`, `net`, `os/signal`; plus `google.golang.org/grpc`, `.../status`, `.../codes`, `.../health`, `.../reflection`, `google.golang.org/genproto/googleapis/rpc/errdetails`
