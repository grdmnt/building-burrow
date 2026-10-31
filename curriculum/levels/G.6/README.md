# G.6 · Client Library

> A client that is itself a Store: the same promise as 3.3 and 7.5, kept over a third protocol.

## Your goal

Write `kvclient`, a Go client for the `KV` service that satisfies `store.Store`, so the chapter 1 conformance suites pass against a server on the other end of a gRPC connection. That means undoing G.3's error mapping: a status with a code and an `ErrorInfo` has to come back as `store.ErrNotFound` or `store.ErrVersionMismatch`, so `errors.Is` works as it does on every other `Store`, while `status.Code` still finds the code. Then `Watch`, as an iterator like 8.5's, that resumes by itself when the stream breaks. And let gRPC do the rest: a service config that retries reads on `UNAVAILABLE` and makes calls wait for the connection instead of failing fast.

## What you're using from before

**gRPC API (G.3 · Front Panel) and gRPC Watch (G.5 · Event Stream).** Every method maps to one call on `burrowpb.KVClient`, the same table as G.3 read the other way: `Set` is `Put` with no `if_version`, `CompareAndSwap(k, v, ...)` is `Put` with `IfVersion: proto.Uint64(v)`, `CompareAndDelete` is `Delete` with `if_version`. The `ErrorInfo` reasons G.3 attaches are what the client maps back.

**Interceptors (G.4 · Signal Chain).** The request ID comes back in the trailers. That's what keeps error responses trailers-only, and only a trailers-only response can be retried. The retry policy below works because of that choice.

**Watch client (8.5 · Watch Client).** The shape and the rules are the same: `Watch` returns an `iter.Seq2[watch.Event, error]` that owns its stream; `READY` is never yielded but its ID counts as seen; on a broken stream it reconnects with backoff and resumes after the last ID seen; it yields an error once and stops for anything it can't handle.

## What's new here

**Dialing.** `grpc.NewClient(target, opts...)` returns a `*grpc.ClientConn` without connecting. The first call connects, and a dropped connection reconnects in the background with its own backoff. One `ClientConn` is safe for any number of goroutines and multiplexes their calls over one HTTP/2 connection; make one per server and share it. `Dial` takes the target and the caller's dial options (credentials, a test's dialer) and adds Burrow's defaults in front:

```go
func Dial(target string, opts Options,
	dialOpts ...grpc.DialOption) (*Client, error) {
	all := append([]grpc.DialOption{
		grpc.WithDefaultServiceConfig(ServiceConfig)}, dialOpts...)
	cc, err := grpc.NewClient(target, all...)
	// ...
}
```

**Errors that answer both questions.** A caller of a `Store` asks `errors.Is(err, store.ErrNotFound)`. A caller that knows it's talking gRPC asks `status.Code(err)`. Wrap with two `%w` verbs (Go 1.20+) and both work: `errors.Is` follows both branches, and `status.FromError` uses `errors.As` to find the status inside.

```go
func mapErr(err error) error {
	st, ok := status.FromError(err)
	if !ok || err == nil {
		return err
	}
	sentinel := /* from ErrorInfo reason, else from the code */
	if sentinel == nil {
		return err
	}
	return fmt.Errorf("%w: %w", sentinel, err)
}
```

Map by `ErrorInfo` reason first (`NOT_FOUND`, `VERSION_MISMATCH`, `INVALID_KEY`, `INVALID_TTL`, `CANNOT_RESUME`, domain `burrow.v1`), found with `st.Details()` and a type assertion to `*errdetails.ErrorInfo`. The reason is what makes `CompareAndSwap` on a missing key `ErrNotFound`, as `storetest.RunCAS` requires, even though its code is `Aborted`. Fall back to the code for errors that don't come from the server: `Canceled` and `DeadlineExceeded` are produced by the client itself and become `context.Canceled` and `context.DeadlineExceeded`, which `storetest`'s cancelled-context check needs.

**TTLs on the wire.** `ttl_ms` 0 means "no TTL", so a `ttl <= 0` can't be sent as it is: `SetWithTTL`, `CompareAndSwapWithTTL` and `Touch` return `store.ErrInvalidTTL` without a call. A positive TTL rounds up to whole milliseconds, so 500 µs is 1 ms rather than 0. `ExpiresAt` is `time.Unix(0, n)` for a non-zero `expires_at_unix_nano`, and zero otherwise.

**The service config.** A JSON document, per service or per method, that tells the client library how to call. `grpc.WithDefaultServiceConfig` sets it when the name resolver doesn't supply one (a DNS TXT record or xDS can). Two settings matter here:

```json
{
  "methodConfig": [
    {
      "name": [
        {"service": "burrow.v1.KV", "method": "Get"},
        {"service": "burrow.v1.KV", "method": "Keys"},
        {"service": "burrow.v1.KV", "method": "Touch"}
      ],
      "waitForReady": true,
      "retryPolicy": {
        "maxAttempts": 4,
        "initialBackoff": "0.05s",
        "maxBackoff": "1s",
        "backoffMultiplier": 2,
        "retryableStatusCodes": ["UNAVAILABLE"]
      }
    },
    {"name": [{"service": "burrow.v1.KV"}], "waitForReady": true}
  ]
}
```

`retryPolicy` retries a call transparently, inside the library, when it fails with a listed code. Only safe calls go in the list: `Get`, `Keys` and `Touch` (setting an expiry to now+ttl twice is the same as once). A `Put` that failed with `UNAVAILABLE` may still have been applied, and a retried `Set` would bump the version twice, so writes surface the error to the caller, who knows whether the write was conditional. `UNAVAILABLE` is the only code worth retrying here: it's what a restarting server or a dropped connection gives. The most specific `name` wins, so the first entry needs `waitForReady` too.

`waitForReady` changes what a call does when the connection is down. By default a call fails at once with `UNAVAILABLE` ("fail fast"). With `waitForReady` it waits for the connection to come back, bounded by its context's deadline. A storage client almost always wants that: a server restart becomes a short pause instead of a burst of errors. Pass it per call with the `grpc.WaitForReady(true)` call option, or for every method here.

**Watch with resume.** The iterator loops over streams. Each stream is a `Watch` call and a `Recv` loop; each received event updates `last` and `seen`; `READY` is skipped after that update. When `Recv` fails:

```text
ctx done                  yield ctx's error (mapped), stop
UNAVAILABLE               wait Backoff(attempt), call Watch again
                          with After: proto.Uint64(last) if seen
anything else             yield the mapped error, stop
                          (OutOfRange -> watch.ErrCannotResume)
```

`seen` is the subtle part. A first call with `afterID` 0 sends `after` unset: live events only. Once any message, `READY` included, has arrived, every reconnect sends `after` set, even when `last` is 0, because "everything after 0" is exactly what the client is missing. A client that reconnected with `after` unset would open a fresh stream and silently skip whatever was written while it was away. The stream runs under a context the iterator cancels on return, so `break` in the caller's loop ends the stream on the server.

## What to do

1. Create `grpc/kvclient/client.go` with `ServiceConfig`, `Options`, `Client`, `Dial` and `Close`.
2. Write `mapErr` and the nine `store.Store` methods. Every error from a call goes through `mapErr`; `ttl <= 0` is `store.ErrInvalidTTL` before any call.
3. Write `Watch` as above. A default `Backoff` when `Options.Backoff` is nil: exponential from 50 ms with jitter, capped at 1 s.
4. `go mod tidy`, then `go test -race ./...` in `grpc/`.

## Example

```go
c, err := kvclient.Dial("localhost:50051", kvclient.Options{},
	grpc.WithTransportCredentials(insecure.NewCredentials()))
if err != nil {
	log.Fatal(err)
}
defer c.Close()

var st store.Store = c
st.Set(ctx, "greeting", []byte("hello"))
_, err = st.CompareAndSwap(ctx, "greeting", 7, []byte("x"))
fmt.Println(errors.Is(err, store.ErrVersionMismatch),
	status.Code(err))
// true Aborted

for ev, err := range c.Watch(ctx, "user:", 0) {
	if err != nil {
		log.Print(err) // ctx done, or a resume that can't work
		break
	}
	fmt.Println(ev.ID, ev.Type, ev.Entry.Key)
}
```

Stop `burrow-grpc` while that loop runs and start it again: the loop pauses, then carries on with whatever was written in between (with `-mem` the broker is gone after a restart, so the resume gets `ErrCannotResume`; against one long-lived server, a dropped connection resumes cleanly).

## Done when

- [ ] `storetest.Run`, `RunConcurrent`, `RunCAS` and `RunTTL` pass through `kvclient` over gRPC
- [ ] Every store sentinel survives the trip: `errors.Is` finds it and `status.Code` finds the code; a deadline gives `context.DeadlineExceeded`
- [ ] A `Get` against a server that answers `UNAVAILABLE` twice succeeds on the third attempt; a `Set` is tried exactly once
- [ ] A `Get` issued while the server is down waits for it to come up
- [ ] `Watch` yields events without `READY`, resumes from an ID, and `break` closes the stream on the server
- [ ] `Watch` from a lost ID yields one `watch.ErrCannotResume` error and ends; a cancelled context yields one `context.Canceled` error and ends
- [ ] When the server stops and starts again, the events written while it was down arrive, with no gaps and no duplicates, resumed after the last ID seen, `READY`'s included
- [ ] No goroutines leak after `Close`

## Reference

```go
// grpc/kvclient
package kvclient

// ServiceConfig: waitForReady for every KV method; retryPolicy
// (maxAttempts 4, UNAVAILABLE) for Get, Keys and Touch only.
const ServiceConfig = `{"methodConfig": [...]}`

type Options struct {
	// Watch reconnect wait for attempt n; nil: exponential from 50ms
	// with jitter, capped at 1s.
	Backoff func(attempt int) time.Duration
}

type Client struct { /* yours */ }

var _ store.Store = (*Client)(nil)

// Dial: grpc.NewClient(target, WithDefaultServiceConfig(ServiceConfig),
// dialOpts...). dialOpts come last so they can override.
func Dial(target string, opts Options,
	dialOpts ...grpc.DialOption) (*Client, error)
func (c *Client) Close() error

// The nine store.Store methods. Errors: fmt.Errorf("%w: %w",
// sentinel, statusErr), sentinel from the ErrorInfo reason
//   INVALID_KEY ErrInvalidKey, INVALID_TTL ErrInvalidTTL,
//   NOT_FOUND ErrNotFound, VERSION_MISMATCH ErrVersionMismatch,
//   CANNOT_RESUME watch.ErrCannotResume
// else from the code: Canceled context.Canceled, DeadlineExceeded
// context.DeadlineExceeded (NotFound, Aborted, OutOfRange as above).
// ttl <= 0: store.ErrInvalidTTL, no call. ttl rounds up to ms.

// Watch: afterID 0 is a live stream (after unset); otherwise resume
// after afterID. READY is not yielded; its ID counts as seen. On
// UNAVAILABLE, wait Backoff(attempt) and resume after the last ID
// seen. Yields one error and stops for ctx errors and anything else.
func (c *Client) Watch(ctx context.Context, prefix string,
	afterID uint64) iter.Seq2[watch.Event, error]
```

Decisions the tests rely on:

- The suite dials with its own `grpc.WithContextDialer`, insecure credentials and short connect backoff, passed as `dialOpts`; it relies on `Dial` putting the service config first.
- Its servers answer `UNAVAILABLE` through G.3's mapping (`store.ErrClosed`), and restart on a new in-memory listener with the same store and broker. A Watch resume sees the same broker, so no events are lost.
- It counts the server's subscriptions through G.5's `Broker` field, and records the `after` of every resume.
- `Close` closes the `ClientConn`; iterators still running end with an error.

## The tests check

- `storetest.Run`, `RunConcurrent`, `RunCAS` and `RunTTL`, each subtest against a fresh server and client
- Eight error cases: `errors.Is` with the sentinel, and `status.Code` with NotFound, Aborted (stale CAS, CAS on a missing key, both CompareAndDelete cases) or InvalidArgument; a 50 ms deadline on a store that blocks: `context.DeadlineExceeded` and code DeadlineExceeded
- `ServiceConfig` is valid JSON; a store failing twice with ErrClosed: Get returns the value and the store saw three calls; Set returns Unavailable and the store saw one
- A Get issued while the server is down returns ErrNotFound once it starts 1 s later
- Live Watch: put and delete events with entries, READY not yielded; resume after 2 under prefix `k`; `break` after two events and a cancelled context both bring the open count back down
- History 8, twenty events, Watch after 3: exactly one error, `watch.ErrCannotResume` and OutOfRange; cancel: one `context.Canceled` error and the iterator ends
- Server stopped after events 4 and 5, events 6 to 10 written while it's down, started again: 6 to 12 arrive in order, and the resume asked for after 5
- READY 3 and nothing else, server stopped, 4 and 5 written, started: 4 and 5 arrive
- No goroutine leaks (counted after a warm-up)

## Try it

```bash
cd grpc
go test -race ./...
go run ./cmd/burrow-grpc -mem &
# a scratch main: kvclient.Dial, then range over Watch while another
# terminal writes with grpcurl
```

## In the wild

etcd's `clientv3` is a gRPC client that hides the protocol behind a Go API, maps statuses back to its own errors and resumes watches by revision; Google's Cloud client libraries ship a service config with retry policies exactly like this one; Spanner and Bigtable clients wait for ready by default.

## New in Go

grpc.NewClient and lazy connections · one ClientConn shared by all goroutines · wrapping with two %w verbs · status.FromError through errors.As · reading error details · service config JSON: retryPolicy and waitForReady · committed calls and transparent retries · a resuming stream as iter.Seq2

Standard library: `iter`, `errors`, `fmt`, `math/rand/v2`; plus `google.golang.org/grpc`, `.../status`, `google.golang.org/genproto/googleapis/rpc/errdetails`
