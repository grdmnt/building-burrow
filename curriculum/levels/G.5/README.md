# G.5 · Event Stream

> 8.3 had to invent framing, heartbeats and resume on top of HTTP. A server-streaming RPC has the first two built in; you only bring the IDs.

## Your goal

Implement `Watch`, the one streaming method in the `KV` service. A client asks for a key prefix and gets a stream of `Event` messages: first a `READY` marker carrying the last ID it has covered, then every put, delete and expiry under that prefix, as they happen, until it cancels. With `after` set it resumes: the server replays what it still has after that ID, then hands over to live events with no gap and no duplicate. This is 8.3 and 8.4 over gRPC, and most of it is already in your broker. The server's job is choosing the subscription, sending `READY`, forwarding, and stopping cleanly in every way a stream can end.

## What you're using from before

**Interceptors (G.4 · Signal Chain).** `Watch` is a stream, so it goes through the stream chain: `StreamRequestID` gives its context an ID, `StreamAccessLog` writes one line when the stream ends, with `sent` counting `READY` and every event, and `StreamRecover` catches a panic in it.

**Broker (8.2 · Fan-out) and Resumable stream (8.4 · Resume).** The server takes the two subscribe methods, behind an interface of its own, so any broker (or a test's fake) fits:

```go
type Broker interface {
	Subscribe(prefix string) (<-chan watch.Event, uint64, func())
	SubscribeFrom(prefix string, afterID uint64) (
		<-chan watch.Event, func(), error)
}
```

`*watch.Broker` satisfies it as it is. `Subscribe` gives live events and the last ID before subscribing; `SubscribeFrom` replays everything retained after `afterID` and continues live, or returns `watch.ErrCannotResume`. The handover race that 8.4 solved under the broker's lock stays solved; the server never touches the ring.

The stream's store is wrapped with `watch.Wrap`, so a `Put` through the KV service publishes, and `OnEvict` turns expiries into events, as in 8.2:

```go
b := watch.NewBroker(64, 1024)
var ws *watch.Store
m := store.NewMemory(store.WithOnEvict(
	func(e store.Entry) { ws.OnEvict(e) }))
ws = watch.Wrap(m, b)
srv := &kvserver.Server{Store: ws, Broker: b}
```

## What's new here

**A server-streaming handler.** For `rpc Watch(WatchRequest) returns (stream Event)` the generated interface method takes the request and a typed stream, and returns an error that ends the stream:

```go
func (s *Server) Watch(req *burrowpb.WatchRequest,
	stream grpc.ServerStreamingServer[burrowpb.Event]) error
```

`stream.Send(ev)` sends one message; it blocks while the client's flow-control window is full, so a slow reader slows its own stream and nobody else's. `stream.Context()` ends when the client cancels, its deadline passes, or the connection drops. Returning `nil` ends the stream with `OK`; returning a status error ends it with that code. The client sees each `Send` as one `Recv`, and the end as `io.EOF` (OK) or the error.

What 8.3 built by hand comes free here. Messages are framed (G.1's 5 bytes), so there's no `id:` / `data:` text protocol. Flushing is per message. And heartbeats are HTTP/2 pings: `grpc.KeepaliveParams(keepalive.ServerParameters{Time: 30 * time.Second, Timeout: 10 * time.Second})` makes the server ping a connection that has been quiet for 30 s and close it if no answer comes in 10 s, so a watcher whose laptop lid closed doesn't hold a subscription forever.

**Presence picks the subscription.** `WatchRequest.after` is `optional`, and the three cases are different requests:

```text
after unset   Subscribe(prefix)         READY id = last, then live
after = 0     SubscribeFrom(prefix, 0)  READY id = 0, every retained
                                        event, then live
after = n     SubscribeFrom(prefix, n)  READY id = n, replay, live
              ErrCannotResume           OutOfRange, CANNOT_RESUME
```

`READY` means what it meant in 8.4: "you have everything up to this ID; what follows is above it". It carries no entry. 8.4's handler answered a stale ID with `event: reset` and carried on with a fresh stream. A gRPC server can do better: return `OutOfRange` (through G.3's `toStatus`, with reason `CANNOT_RESUME`) and let the client decide whether to start over, since only the client knows whether it can.

**The forwarding loop.** One `select`, two cases:

```go
defer stop()
for {
	select {
	case <-ctx.Done():
		return status.FromContextError(ctx.Err()).Err()
	case ev, ok := <-ch:
		if !ok {
			return status.Error(codes.Unavailable,
				"watcher fell behind; resume from the last id")
		}
		if err := stream.Send(eventProto(ev)); err != nil {
			return err
		}
	}
}
```

Three ways out, and each one matters. The client goes away: `ctx.Done()`, the handler returns, `defer stop()` unsubscribes. Without the `ctx` case a quiet stream would wait on `ch` forever after its client left, holding a subscription and making `GracefulStop` hang. The broker drops a subscriber that fell `buffer` events behind by closing its channel: that's `Unavailable`, which tells the client "retry", and a resume from its last ID gets the missed events from the ring. `Send` fails: the stream is already broken, return its error.

**Converting events.** `watch.Event.Type` strings become `EventType` values (`put`, `delete`, `expire`), and the entry goes through G.3's `toProto`. A delete event's entry has only the key.

## What to do

1. Add `Broker` (the interface) and a `Broker Broker` field to `Server`. A nil `Broker` makes `Watch` return `Unimplemented`.
2. Write `Watch` in `grpc/kvserver/watch.go`: pick the subscription from `req.After`, `defer stop()`, send `READY`, then the forwarding loop. Map `ErrCannotResume` with `toStatus` (add the `OutOfRange` / `CANNOT_RESUME` case).
3. Add `grpc.KeepaliveParams` to `GRPCServer`.
4. In `burrow-grpc -mem`, build the broker (`NewBroker(64, 1024)`) and `watch.Wrap` the memory store as above. Over the frame client there's no broker: writes from other `burrowd` clients would never be seen, so `Watch` stays `Unimplemented` there.
5. `go test -race ./kvserver` in `grpc/`.

## Example

```bash
$ grpcurl -plaintext -d '{"prefix":"user:"}' \
    localhost:50051 burrow.v1.KV/Watch
{
  "id": "3",
  "type": "EVENT_TYPE_READY"
}
{
  "id": "4",
  "type": "EVENT_TYPE_PUT",
  "entry": {
    "key": "user:3",
    "value": "Y3k=",
    "version": "1"
  }
}
```

Resuming after 2 replays 3 and 4, then waits for live events:

```bash
$ grpcurl -plaintext -d '{"prefix":"user:","after":"2"}' \
    localhost:50051 burrow.v1.KV/Watch
{ "id": "2", "type": "EVENT_TYPE_READY" }
{ "id": "3", "type": "EVENT_TYPE_PUT", ... }
{ "id": "4", "type": "EVENT_TYPE_PUT", ... }
```

## Done when

- [ ] An unset `after` gives `READY` with the broker's last ID, then live puts and deletes under the prefix only, with entries
- [ ] An expiry on a fake clock arrives as `EVENT_TYPE_EXPIRE` with the entry
- [ ] `after` = 2 gives `READY` 2, then 3, 4, 5 from the ring, then live; `after` = 0 replays everything retained
- [ ] With history 8 and 20 events, `after` 11 and `after` 99 end with `OutOfRange` and reason `CANNOT_RESUME`, and `after` 12 gives 13 to 20; a refused resume subscribes nothing
- [ ] A writer runs while a client reconnects 100 times with its last ID: the IDs are contiguous
- [ ] Cancelling a stream gives the client `Canceled`, and every subscription is closed afterwards
- [ ] A closed broker channel ends the stream with `Unavailable`; no `Broker` gives `Unimplemented`
- [ ] The access log has one line for the stream with code `Canceled` and `sent` counting `READY` and the events
- [ ] No goroutines leak, even with a stream left open when the connection closes

## Reference

```go
// grpc/kvserver
type Broker interface {
	Subscribe(prefix string) (<-chan watch.Event, uint64, func())
	SubscribeFrom(prefix string, afterID uint64) (
		<-chan watch.Event, func(), error)
}

type Server struct {
	burrowpb.UnimplementedKVServer
	Store  store.Store
	Broker Broker       // nil: Watch is Unimplemented
	Logger *slog.Logger
}

// Watch:
//   after unset: Subscribe; READY{id: last}
//   after set:   SubscribeFrom(prefix, after); READY{id: after}
//                ErrCannotResume -> OutOfRange, reason CANNOT_RESUME
//   then one Event per broker event (type PUT|DELETE|EXPIRE, entry)
//   ctx done        -> status.FromContextError(ctx.Err())
//   channel closed  -> Unavailable
//   always unsubscribes (defer stop())
func (s *Server) Watch(req *burrowpb.WatchRequest,
	stream grpc.ServerStreamingServer[burrowpb.Event]) error

// GRPCServer adds grpc.KeepaliveParams(keepalive.ServerParameters{
//     Time: 30 * time.Second, Timeout: 10 * time.Second})
// burrow-grpc -mem: NewBroker(64, 1024), watch.Wrap, OnEvict wired.
```

Decisions the tests rely on:

- The suite passes a broker of its own that wraps a real `*watch.Broker` and counts open subscriptions through the stop functions. So `Watch` must go through the `Broker` field, never a broker it built itself, and must call the stop function it got.
- `READY` is the first message of every successful stream, sent after subscribing, so a client that has `READY` is guaranteed to see every later event.
- A refused resume (`ErrCannotResume`) must not leave a subscription open.

## The tests check

- READY with last = 3 after three writes; a put of `user:3` arrives as id 4 with key, value and version; `cfg:y` is filtered out; a delete of `user:1` arrives as 6
- `ttl_ms` 1000 on a `FakeClock`, the clock moved 2 s and the key read: an EXPIRE event with the entry and its expiry
- Five writes; `after` 2 gives READY 2, 3, 4, 5, then a live 6; `after` 0 gives READY 0 and 1 to 6; unset gives READY 6 and a live 7
- History 8, twenty events: `after` 11 and 99 are OutOfRange with CANNOT_RESUME; `after` 12 replays 13 to 20; one subscription open afterwards
- A writer every 100 µs while a client reads 1 to 20 events and reconnects with its last ID, 100 times: every resumed READY equals the ID it asked for, and all IDs are contiguous
- Three streams (one with `after` 0), each cancelled: `Recv` gives Canceled, and the open subscription count returns to 0
- The stream's access log line, found by an incoming `x-request-id`: code `Canceled`, `sent` 2
- A broker whose channels are already closed: READY with its ID, then Unavailable; no broker: Unimplemented
- No goroutine leaks after `GracefulStop`, with one stream never cancelled by its client (counted after a warm-up)

## Try it

```bash
cd grpc
go test -race -run LG_5 ./kvserver
go run ./cmd/burrow-grpc -mem &
grpcurl -plaintext -d '{"prefix":""}' \
    localhost:50051 burrow.v1.KV/Watch &
grpcurl -plaintext -d '{"key":"a","value":"eA=="}' \
    localhost:50051 burrow.v1.KV/Put
```

## In the wild

etcd's `Watch` is this RPC (bidirectional there), with revisions as IDs and `ErrCompacted` for an ID that fell out of history; Kubernetes' watch on the API server resumes with `resourceVersion` and answers `410 Gone` when it can't; Google Pub/Sub's StreamingPull is a long-lived gRPC stream with keepalives.

## New in Go

server-streaming handlers · grpc.ServerStreamingServer[T] · Send and flow control · stream.Context() for cancellation · ending a stream with a status · keepalive instead of heartbeats · accepting an interface for a concrete dependency

Standard library: `context`; plus `google.golang.org/grpc/keepalive`
