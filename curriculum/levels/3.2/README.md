# 3.2 · Switchboard

A request/reply loop: decode a frame, call the store, and map every outcome to exactly one reply. The server takes any `store.Store`, which is also what lets the tests inject a slow fake.

## Build

```go
// wire/server.go
package wire

type Server struct {
	Store  store.Store
	Logger *slog.Logger // nil means no logging
}

func (s *Server) Serve(ln net.Listener) error // runs on ServeConns

// Reply table (every reply echoes the request's Key):
//   OpGet     -> OpValue{Value, Version, TTL remaining} | OpNotFound   (TTL is rounded up to at least 1ns when an expiry exists, so 0 always means no expiry)
//   OpSet     -> OpOK{Version}
//   OpDelete  -> OpOK | OpNotFound
//   OpKeys    -> OpKeyList{Value: keys joined by \n, empty for none}
//   OpCAS     -> OpOK{Version} | OpMismatch{current Version} | OpNotFound   (TTL > 0: CompareAndSwapWithTTL; TTL < 0: OpError)
//   OpCAD     -> OpOK | OpMismatch{current Version} | OpNotFound
//   OpSetTTL  -> OpOK{Version} | OpError (invalid ttl)
//   OpTouch   -> OpOK{Value, Version, TTL just set} | OpNotFound | OpError (invalid ttl)   (the one OpOK that carries Value and TTL; every other OpOK carries only Version)
//   OpPing    -> OpPong echoing Value
//   invalid key          -> OpError with the error text; connection stays open. OpKeys and OpPing skip key validation (a prefix may be empty or partial); every other op validates.
//   reply op as request  -> OpError; connection stays open
//   unknown op byte      -> one OpError, then close (the stream can't be trusted)
//   oversized frame      -> close (optionally after one OpError)
// Requests on one connection are handled in order; each store call gets context.Background() for now.
// A panic inside a store call must not take the server down: recover it in the connection goroutine
// (per connection or per request), log it at Error with the panic value, and either answer OpError
// or close that one connection. Every other connection, and Serve itself, carries on.
```

- `wire.Server{Store, Logger}` and `Serve`
- A per-connection loop: ReadFrame, dispatch, WriteFrame
- The reply table above, including error mapping with errors.Is
- A deferred `recover` in the connection goroutine, so a panicking store call costs one connection, not the process

Decisions the tests rely on:

- Each request runs with `context.Background()` for now. 3.5 adds shutdown; the HTTP side gets real deadlines in 7.4.
- `OpKeys` echoes the prefix in `Key`; `OpPing` echoes `Value`. `OpKeyList` has an empty `Value` for no keys.
- `OpKeys` and `OpPing` skip key validation (a prefix may be empty or partial). Every other op validates the key and answers `OpError` for a bad one.
- `OpTouch` is the one `OpOK` that carries `Value` and `TTL` (the ttl just set) as well as `Version`; every other `OpOK` carries only `Version`.
- `OpValue.TTL` is the time remaining, rounded up to at least 1ns when an expiry exists, so 0 always means no expiry.
- `OpCAS` with `TTL < 0` is `OpError`. Fields a request op does not use (a `Value` on `OpGet`) are ignored.
- A panic in a store call is recovered in that connection's goroutine and logged at `Error`. The connection that triggered it gets an `OpError` or is closed, your choice; other open connections and new connections are unaffected and `Serve` keeps running. Note that a panic in a goroutine you started is not caught by a `recover` in the goroutine that started it: the `defer` has to be in the goroutine that panics.

## The tests check

- Each row of the reply table through a raw net.Conn with WriteFrame/ReadFrame, including Version and TTL fields on OpValue and OpOK
- OpCAS with TTL 5s creates the key with an expiry; a following OpGet reports TTL remaining > 0
- OpTouch's OpOK carries Value, Version and the TTL just set; OpSet's OpOK carries only Version (Value empty, TTL 0)
- OpKeyList for 0, 1 and 1000 keys; the list is sorted; an empty prefix lists all
- Invalid key gets OpError with text containing `invalid key` and the next request on the same connection works
- A reply op sent as a request gets OpError and the connection stays open
- Unknown op 0x7f: one OpError then EOF on the next read
- A frame with a 9 MiB value: the connection closes
- 100 requests written in one Write get 100 replies in order
- 40 concurrent clients doing Set/Get on their own keys never see another client's key in a reply
- A fake store.Store whose Get blocks on one connection doesn't delay another connection
- Serve returns nil after the listener closes; no goroutines leak
- OpKeys with an invalid prefix such as `a b` is served (OpKeyList, possibly empty), not OpError; OpGet with that key is OpError
- TestL3_2_PanicInStoreIsContained: a fake Store whose Get("boom") panics. That connection gets an OpError or is closed (either passes); a connection opened before the panic and one opened after both keep working; Serve is still running

## Uses

- Accept loop (built in 3.1 · Loopback)
- KV Frame (built in 2.2 · Frame)
- Memory store (built in 1.1 · Register File)
- CAS (built in 1.3 · Versions)
- Expiry (built in 1.4 · Decay)

## In the wild

A Redis server's command dispatch; net/rpc's server loop

## New in Go

request/reply loops · switch on typed constants · mapping errors to replies with errors.Is · interfaces as injection points (a fake Store in tests) · defer and recover per goroutine · min and max for TTL rounding

Standard library: `net`, `io`, `errors`, `strings`, `log/slog`
