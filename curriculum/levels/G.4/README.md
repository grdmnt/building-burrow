# G.4 · Signal Chain

> The same three layers as 7.3, on a bus where nothing is a handler and nothing has a status line.

## Your goal

Give the gRPC server what 7.3 gave the HTTP API: a request ID on every call, one JSON access log line per call, and panic recovery that turns a crash into `Internal` and keeps the server alive. In gRPC these are *interceptors*, functions that wrap every call the server dispatches. There are two kinds, one for unary calls and one for streams, so you write each layer twice. The stream version needs one new trick: a stream's context lives inside the stream, so to put a value in it you wrap the `grpc.ServerStream` by embedding, exactly like 7.3 wrapped the `ResponseWriter`.

## What you're using from before

**gRPC API (G.3 · Front Panel).** `Server.GRPCServer()` grows two options, the interceptor chains, and `Server` grows a `Logger`:

```go
type Server struct {
	burrowpb.UnimplementedKVServer
	Store  store.Store
	Logger *slog.Logger // nil: no access log
}
```

**Middleware chain (7.3 · Signal Chain).** The ideas carry over one for one. `RequestID` honours or creates an ID, stores it in the context under an unexported key, and sends it back to the client. `AccessLog` times the call and logs it. `Recover` defers a `recover()`. The order is the same too, outermost first, `Recover`, `RequestID`, `AccessLog`, and for the same reason: `Recover` outermost catches a panic in the other two. What changes is the shape of the function and where the ID travels.

## What's new here

**Unary interceptors.** A `grpc.UnaryServerInterceptor` gets the context, the decoded request, `info.FullMethod` (`/burrow.v1.KV/Get`) and `handler`, the next layer in. It calls `handler` (or doesn't) and returns what it returns. It's 7.3's middleware with the request and response as values instead of an `http.ResponseWriter`:

```go
func UnaryAccessLog(l *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any,
		info *grpc.UnaryServerInfo,
		h grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := h(ctx, req)
		// log info.FullMethod, status.Code(err), time.Since(start)
		return resp, err
	}
}
```

The status is easy here: it's `status.Code(err)`, `OK` when `err` is nil. No recorder needed.

**The built-in Chain.** `grpc.ChainUnaryInterceptor(a, b, c)` is 7.3's `Chain`: `a` is outermost. `grpc.ChainStreamInterceptor` does the same for streams. Both are server options, and they add up if given more than once, so `GRPCServer(opts...)` can still accept more from a caller. (The older `grpc.UnaryInterceptor` option takes exactly one interceptor and panics if given twice; prefer the chain.)

```go
grpc.NewServer(
	grpc.MaxRecvMsgSize(MaxRecvMsgSize),
	grpc.ChainUnaryInterceptor(UnaryRecover(s.Logger),
		UnaryRequestID(), UnaryAccessLog(s.Logger)),
	grpc.ChainStreamInterceptor(StreamRecover(s.Logger),
		StreamRequestID(), StreamAccessLog(s.Logger)),
)
```

**Metadata.** gRPC's headers are *metadata*: lowercase keys, each with a list of string values. The client attaches some with `metadata.AppendToOutgoingContext`; the server reads them with `metadata.FromIncomingContext(ctx)`. The server sends metadata back in two places: *headers*, sent before the first response message, and *trailers*, sent after the last one, together with the status. A client reads them with the call options `grpc.Header(&md)` and `grpc.Trailer(&md)`.

```go
md, _ := metadata.FromIncomingContext(ctx)
ids := md.Get("x-request-id") // []string, keys are lowercase
grpc.SetTrailer(ctx, metadata.Pairs("x-request-id", id))
```

**Why the ID goes in the trailers.** A gRPC response that fails before sending anything is *trailers-only*: one frame with the status. Once a server has sent headers, the call is *committed*, and the client's retry machinery may no longer retry it, because it can't know what the server did. G.6 turns on retries for `Get`, and a `RequestID` that called `grpc.SetHeader` would quietly switch them off: every error response would carry headers. Trailers are always sent, on success and failure, so they lose nothing. (Streams follow the same rule; `ss.SetTrailer`.)

**Stream interceptors and wrapping the stream.** A `grpc.StreamServerInterceptor` gets `srv`, a `grpc.ServerStream`, `info` and `handler`. There's no request argument: the handler reads messages from the stream. And there's no context argument either: the context is `ss.Context()`. To give the handler a context with the request ID in it, hand it a stream whose `Context` returns yours. Embed the interface, override one method:

```go
type wrappedStream struct {
	grpc.ServerStream // SendMsg, RecvMsg, SetHeader... forwarded
	ctx  context.Context
	sent int
}

func (w *wrappedStream) Context() context.Context { return w.ctx }

func (w *wrappedStream) SendMsg(m any) error {
	err := w.ServerStream.SendMsg(m)
	if err == nil {
		w.sent++
	}
	return err
}
```

It's the `statusRecorder` from 7.3 again: the embedded interface supplies every method you don't write. The access log uses the same wrapper to count messages sent, which is the stream's answer to 7.3's byte count. Generated stream types (`grpc.ServerStreamingServer[burrowpb.Event]` in G.5) are built on top of whatever `ServerStream` the handler receives, so your override is what the handler's `stream.Context()` returns.

**Recover with a named result.** A deferred function can only change what a function returns if the result has a name. That's how `Recover` replaces a panic with an error:

```go
return func(ctx context.Context, req any,
	info *grpc.UnaryServerInfo,
	h grpc.UnaryHandler) (resp any, err error) {
	defer func() {
		if p := recover(); p != nil {
			// log p and debug.Stack() at Error
			resp, err = nil, status.Error(codes.Internal,
				"internal error")
		}
	}()
	return h(ctx, req)
}
```

Without it, a panic in a handler goroutine kills the whole process. The client gets `"internal error"`, never the panic value: panics carry internal state, and that goes to the log, not over the wire.

## What to do

1. Create `grpc/kvserver/interceptors.go` with `RequestIDHeader`, an unexported context key, and `RequestIDFrom(ctx)`.
2. `UnaryRequestID()`: take the first `x-request-id` from incoming metadata if it is 1 to 128 bytes, else 16 bytes from `crypto/rand` as hex. `grpc.SetTrailer` it, put it in the context, call the handler with that context.
3. `StreamRequestID()`: the same, with `ss.SetTrailer`, and call the handler with a wrapped stream whose `Context()` carries the ID.
4. `UnaryAccessLog(l)` and `StreamAccessLog(l)`: after the handler returns, log `msg` `"rpc"` at Info with `id`, `method`, `code` and `duration`, plus `sent` for streams (wrap the stream to count). A nil logger logs nothing.
5. `UnaryRecover(l)` and `StreamRecover(l)`: on a panic, log `msg` `"panic"` at Error with the method, the value and `debug.Stack()`, and return `status.Error(codes.Internal, "internal error")`.
6. Add both chains to `GRPCServer`, before `opts`. In `burrow-grpc`, set `Logger` to `slog.New(slog.NewJSONHandler(os.Stderr, nil))`.

## Example

```bash
$ grpcurl -plaintext -v -H 'x-request-id: trace-42' \
    -d '{"key":"missing"}' localhost:50051 burrow.v1.KV/Get
...
Response trailers received:
x-request-id: trace-42
Sent 1 request and received 0 responses
ERROR:
  Code: NotFound
  Message: store: not found
```

On `burrow-grpc`'s stderr, one line per call:

```json
{"time":"2026-10-31T09:41:03.120Z","level":"INFO","msg":"rpc",
 "id":"trace-42","method":"/burrow.v1.KV/Get","code":"NotFound",
 "duration":183000}
```

## Done when

- [ ] Every response, success or error, carries `x-request-id` in its trailers: 32 hex characters, unique over 200 calls, and never in the headers
- [ ] A client's own `x-request-id` (up to 128 bytes) comes back unchanged and appears in the log; a longer one is replaced
- [ ] `RequestIDFrom(ctx)` inside the handler returns the same ID, for unary calls and inside a stream's `Context()`
- [ ] One JSON `"rpc"` line per call with `id`, `method`, `code` (`status.Code(err).String()`) and a numeric `duration`; streams also log `sent`
- [ ] A panicking handler, unary or streaming, gives `Internal` without the panic value, logs the value at Error, and the server keeps serving
- [ ] A panic inside `AccessLog` is still caught, because `Recover` is outermost
- [ ] With a nil `Logger` nothing is logged and nothing crashes

## Reference

```go
// grpc/kvserver
const RequestIDHeader = "x-request-id"

type Server struct {
	burrowpb.UnimplementedKVServer
	Store  store.Store
	Logger *slog.Logger // nil: no access log, panics not logged
}

// RequestIDFrom returns the ID the RequestID interceptor stored, or "".
func RequestIDFrom(ctx context.Context) string

// Honour an incoming x-request-id of 1..128 bytes, else 16 random
// bytes in hex. Echo it with SetTrailer (never SetHeader: a response
// that sent headers can't be retried). Store it in the context.
func UnaryRequestID() grpc.UnaryServerInterceptor
func StreamRequestID() grpc.StreamServerInterceptor

// msg "rpc", Info: id, method (info.FullMethod),
// code (status.Code(err).String()), duration; streams add sent.
func UnaryAccessLog(l *slog.Logger) grpc.UnaryServerInterceptor
func StreamAccessLog(l *slog.Logger) grpc.StreamServerInterceptor

// panic -> status.Error(codes.Internal, "internal error");
// msg "panic", Error: method, panic, stack.
func UnaryRecover(l *slog.Logger) grpc.UnaryServerInterceptor
func StreamRecover(l *slog.Logger) grpc.StreamServerInterceptor

// GRPCServer adds, before opts:
//   grpc.ChainUnaryInterceptor(UnaryRecover(l), UnaryRequestID(),
//       UnaryAccessLog(l))
//   grpc.ChainStreamInterceptor(StreamRecover(l), StreamRequestID(),
//       StreamAccessLog(l))
```

Decisions the tests rely on:

- The suite registers its own streaming test service on the `*grpc.Server` that `GRPCServer()` returns, so the stream chain must be server-wide (it is, as a server option), and `GRPCServer` must not call `Serve`.
- The log is a `slog.NewJSONHandler`; `duration` is slog's number of nanoseconds; `sent` is a number.
- The order test uses a logger that panics on the `"rpc"` line. With `Recover` anywhere but outermost, that panic crashes the test binary with a message saying so.

## The tests check

- 200 calls, half of them errors: each trailer has one 32-hex `x-request-id`, all different; the headers of an error response have none
- An incoming `trace-abc-123` is echoed and logged with code `OK` and a numeric duration; a 200-byte ID is replaced
- A fake store reads `RequestIDFrom(ctx)` and sees the trailer's ID; a bare context gives `""`
- A store whose `Get` panics: `Internal`, the panic value absent from the error, an `x-request-id` trailer, an Error-level `"panic"` line with the value, and the next call works
- A logger that panics on `"rpc"`: the call still ends with `Internal`
- A test streaming service: `RequestIDFrom(ss.Context())` is set inside the handler, three messages arrive, the trailer has the ID, the log line has code `OK` and `sent` 3; a panicking stream handler gives `Internal` without the value, the value is logged, the server keeps serving
- A nil `Logger` with a panicking store: `Internal`, then normal calls

## Try it

```bash
cd grpc
go test -race -run LG_4 ./kvserver
go run ./cmd/burrow-grpc -mem 2>&1 | grep '"msg":"rpc"' &
grpcurl -plaintext -v -d '{"key":"k"}' localhost:50051 burrow.v1.KV/Get
```

## In the wild

grpc-ecosystem/go-grpc-middleware ships logging, recovery, auth and rate-limit interceptors in exactly this shape; OpenTelemetry's gRPC instrumentation wraps the stream the same way; Envoy and most load balancers put their request ID in `x-request-id`.

## New in Go

unary and stream interceptors · grpc.ChainUnaryInterceptor as the built-in Chain · metadata: incoming, outgoing, headers and trailers · committed calls and retries · wrapping grpc.ServerStream by embedding · named results changed by a deferred recover

Standard library: `log/slog`, `crypto/rand`, `encoding/hex`, `runtime/debug`; plus `google.golang.org/grpc/metadata`
