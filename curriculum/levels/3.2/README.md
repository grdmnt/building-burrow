# 3.2 · Switchboard

> A switchboard operator takes each call, plugs it into the right line, and relays the answer. Same job, with frames.

## Your goal

Turn the accept loop into a real key-value server. `wire.Server` reads a request frame from a client, calls the matching method on a `store.Store`, and writes exactly one reply frame back, for every possible outcome. Then it reads the next request. This is a *request/reply loop*: one request in, one reply out, in order, for as long as the client stays connected.

The server holds its store as the `store.Store` interface, not as `*store.Memory`. That is what lets the tests hand it a fake store (one that blocks, or one that panics) and check the server copes.

## What you're using from before

**Accept loop** (3.1 · Loopback). `ServeConns` accepts connections and runs a handler per connection. `Server.Serve` is one call to it; your per-connection loop is the handler:

```go
func (s *Server) Serve(ln net.Listener) error {
	return ServeConns(ln, s.handleConn, s.Logger)
}
```

**KV Frame** (2.2 · Frame). `ReadFrame` and `WriteFrame` move one `Frame` across a connection, and the `Op` constants name the request and reply kinds. The loop reads with one and answers with the other:

```go
req, err := wire.ReadFrame(conn)   // io.EOF when the client hangs up
// ... decide the reply ...
err = wire.WriteFrame(conn, reply)
```

**Memory store** (1.1 · Register File). `store.Store` is the interface the server calls, and `store.ErrNotFound` is the error you turn into `OpNotFound`:

```go
e, err := s.Store.Get(context.Background(), req.Key)
```

**CAS** (1.3 · Versions). `CompareAndSwap` and `CompareAndDelete` serve `OpCAS` and `OpCAD`; `store.ErrVersionMismatch` becomes `OpMismatch`, and the reply carries the version that is actually stored so the client can retry.

**Expiry** (1.4 · Decay). `SetWithTTL`, `CompareAndSwapWithTTL` and `Touch` serve `OpSetTTL`, `OpCAS` with a TTL, and `OpTouch`. `store.ErrInvalidTTL` becomes `OpError`. The `ExpiresAt` on an entry is how you fill in `OpValue.TTL`, the time left before the key expires.

## What's new here

**A request/reply loop.** The handler for one connection is a loop with three steps: read a frame, work out the reply, write the frame. It leaves the loop when the read fails (the client went away) or when it decides the connection can no longer be trusted:

```go
for {
	req, err := ReadFrame(conn)
	if err != nil {
		return // EOF or a broken stream: just leave
	}
	reply, keep := s.handle(req)
	if err := WriteFrame(conn, reply); err != nil || !keep {
		return
	}
}
```

Splitting `handle(req) (Frame, bool)` out of the loop is worth it: it is a pure function from request to reply that you can test without a socket.

**Switching on a typed constant.** `wire.Op` is a named byte type, so a `switch` over it reads like a dispatch table. Each case calls one store method and builds one reply:

```go
switch req.Op {
case OpGet:
	e, err := st.Get(ctx, req.Key)
	// ...
case OpSet:
	e, err := st.Set(ctx, req.Key, req.Value)
	// ...
case OpPing:
	return Frame{Op: OpPong, Key: req.Key, Value: req.Value}, true
default:
	// unknown op: one OpError, then close
}
```

**Mapping errors to replies with `errors.Is`.** The store reports outcomes as sentinel errors. The server translates each one into a reply op, in one place, with `errors.Is` so wrapping never breaks it:

```go
switch {
case err == nil:
	// OpOK or OpValue, depending on the op
case errors.Is(err, store.ErrNotFound):
	// OpNotFound
case errors.Is(err, store.ErrVersionMismatch):
	// OpMismatch, with the current version in Version
default:
	// OpError, with err.Error() as the Value
}
```

**Interfaces as injection points.** Because `Server.Store` is an interface, a test can wrap your real store in a type that changes one method. Embedding the interface gives the wrapper every other method for free:

```go
type blockingStore struct {
	store.Store              // all methods come from here...
	release chan struct{}
}

func (b *blockingStore) Get(
	ctx context.Context, k string) (store.Entry, error) {
	if k == "slow" {
		<-b.release // ...except Get, which can now stall on purpose
	}
	return b.Store.Get(ctx, k)
}
```

Your server never knows the difference. That is the whole value of taking an interface.

**`defer` and `recover`, per goroutine.** A panic unwinds the goroutine it happened in. If nothing recovers it there, the entire process dies, every connection included. A `recover` only works inside a deferred function, and only in the goroutine that is panicking; a `defer` in the goroutine that *started* the connection goroutine catches nothing. So each connection goroutine protects itself:

```go
func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()
	defer func() {
		if r := recover(); r != nil {
			s.Logger.Error("panic in handler", "panic", r)
		}
	}()
	// ... the loop ...
}
```

After a recover the connection is in an unknown state; closing it is fine. The other connections, and `Serve`, carry on.

**`min` and `max` for TTL rounding.** `OpValue.TTL` is the time remaining, and `0` is reserved for "no expiry". A key with an expiry 1ns in the future would otherwise report `0` and look permanent, so round it up: `max(remaining, time.Nanosecond)`. `min` and `max` are builtins since Go 1.21 and work on any ordered type, `time.Duration` included.

## What to do

1. Create `wire/server.go` with the `Server` struct from the Reference.
2. Write `Serve`: one call to `ServeConns` with your connection handler and the server's logger.
3. Write the connection handler:
   - Defer closing the connection and a `recover` that logs the panic value at `Error`.
   - Loop: `ReadFrame`, dispatch, `WriteFrame`. Stop on a read or write error.
4. Write the dispatch (a `handle(req Frame) (Frame, bool)` function is the easy shape):
   - Reject a frame whose op is a reply op (`!req.Op.IsRequest()`) with `OpError`; keep the connection.
   - Reject an unknown op byte with `OpError`, then close the connection.
   - For every op except `OpKeys` and `OpPing`, validate `req.Key` with `store.ValidateKey`; a bad key is `OpError` with the error text, connection kept.
   - `switch` on `req.Op`, call the store with `context.Background()`, and build the reply from the table in the Reference. Every reply copies `req.Key` into `Key`.
   - Map errors in one place: nil, `ErrNotFound`, `ErrVersionMismatch`, anything else.
5. Handle the oversized frame: `ReadFrame` fails with `ErrFrameTooLarge` or `ErrRecordTooLarge`; close the connection (one `OpError` first is allowed).
6. Fill in `OpValue.TTL`: zero `ExpiresAt` means `0`; otherwise the time until `ExpiresAt`, rounded up to at least 1ns.
7. Run `gofmt -l .` and `go vet ./...` until both print nothing.

## Example

Talking to the server from Go, with the raw frame functions, the way the tests do:

```go
st := store.NewMemory()
ln, _ := net.Listen("tcp", "127.0.0.1:0")
go (&wire.Server{Store: st}).Serve(ln)

c, _ := net.Dial("tcp", ln.Addr().String())

wire.WriteFrame(c,
	wire.Frame{Op: wire.OpSet, Key: "k", Value: []byte("v")})
r, _ := wire.ReadFrame(c)
// r.Op == wire.OpOK, r.Key == "k", r.Version == 1

wire.WriteFrame(c, wire.Frame{Op: wire.OpGet, Key: "k"})
r, _ = wire.ReadFrame(c)
// r.Op == wire.OpValue, r.Value == []byte("v"), r.Version == 1,
// r.TTL == 0

wire.WriteFrame(c, wire.Frame{Op: wire.OpGet, Key: "nope"})
r, _ = wire.ReadFrame(c)
// r.Op == wire.OpNotFound, r.Key == "nope"

wire.WriteFrame(c, wire.Frame{Op: wire.OpCAS, Key: "k", Version: 7})
r, _ = wire.ReadFrame(c)
// r.Op == wire.OpMismatch, r.Version == 1 (the version actually stored)

wire.WriteFrame(c, wire.Frame{Op: wire.OpSet, Key: "has space"})
r, _ = wire.ReadFrame(c)
// r.Op == wire.OpError, string(r.Value) contains "invalid key"
// and the connection is still open: the next request works
```

## Done when

- [ ] Every request op gets the reply from the table, with `Key` echoed and `Version`/`TTL`/`Value` filled in where the table says
- [ ] `OpCAS` with a TTL creates a key that expires; `OpTouch`'s `OpOK` carries `Value`, `Version` and the TTL just set; `OpSet`'s `OpOK` carries only `Version`
- [ ] `OpKeys` returns the sorted keys joined by newlines (empty `Value` for none) and accepts any prefix, even an invalid key like `a b`
- [ ] An invalid key or a reply op sent as a request gets `OpError` and the connection stays usable
- [ ] An unknown op gets one `OpError` and then the connection is closed; a 9 MiB value closes the connection
- [ ] 100 requests sent in one write get 100 replies, in order
- [ ] A connection stuck inside the store doesn't delay another connection
- [ ] A store method that panics costs at most that one connection; other connections and `Serve` keep going
- [ ] `Serve` returns `nil` after the listener closes and leaves no goroutines behind
- [ ] `gofmt -l .` and `go vet ./...` print nothing

## Reference

The exact contract the tests hold you to.

```go
// wire/server.go
package wire

type Server struct {
	Store  store.Store
	Logger *slog.Logger // nil means no logging
}

func (s *Server) Serve(ln net.Listener) error // runs on ServeConns
```

Reply table. Every reply echoes the request's `Key`.

| Request | Replies |
|---|---|
| `OpGet` | `OpValue{Value, Version, TTL remaining}` or `OpNotFound` |
| `OpSet` | `OpOK{Version}` |
| `OpDelete` | `OpOK` or `OpNotFound` |
| `OpKeys` | `OpKeyList{Value: keys joined by \n, empty for none}` |
| `OpCAS` | `OpOK{Version}`, `OpMismatch{current Version}` or `OpNotFound`; TTL > 0 means `CompareAndSwapWithTTL`; TTL < 0 is `OpError` |
| `OpCAD` | `OpOK`, `OpMismatch{current Version}` or `OpNotFound` |
| `OpSetTTL` | `OpOK{Version}` or `OpError` (invalid ttl) |
| `OpTouch` | `OpOK{Value, Version, TTL just set}`, `OpNotFound` or `OpError` (invalid ttl) |
| `OpPing` | `OpPong` echoing `Value` |

| Problem | Reply | Connection |
|---|---|---|
| Invalid key (any op except `OpKeys`, `OpPing`) | `OpError` with the error text | stays open |
| A reply op sent as a request | `OpError` | stays open |
| Unknown op byte | one `OpError` | closed (the stream can't be trusted) |
| Oversized frame | optionally one `OpError` | closed |

Decisions the tests rely on:

- Requests on one connection are handled in order. Each store call gets `context.Background()` for now.
- `OpKeys` echoes the prefix in `Key`; `OpPing` echoes `Value`. `OpKeyList` has an empty `Value` for no keys.
- `OpKeys` and `OpPing` skip key validation (a prefix may be empty or partial). Every other op validates the key and answers `OpError` for a bad one.
- `OpTouch` is the one `OpOK` that carries `Value` and `TTL` (the ttl just set) as well as `Version`; every other `OpOK` carries only `Version`.
- `OpValue.TTL` is the time remaining, rounded up to at least 1ns when an expiry exists, so 0 always means no expiry.
- `OpCAS` with `TTL < 0` is `OpError`. Fields a request op does not use (a `Value` on `OpGet`) are ignored.
- A panic in a store call is recovered in that connection's goroutine and logged at `Error`. The connection that triggered it gets an `OpError` or is closed, your choice; other open connections and new connections are unaffected and `Serve` keeps running. A `defer` has to be in the goroutine that panics; one in the goroutine that started it catches nothing.

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

## Try it

```bash
./burrow watch
go test -run 'TestL3_2_ReplyTable' -v ./wire   # one row at a time
```

There is no client yet (that is the next level), so the Example above, pasted into a throwaway `main`, is the quickest way to poke the server by hand.

## In the wild

A Redis server's command dispatch; net/rpc's server loop.

## New in Go

request/reply loops · switch on typed constants · mapping errors to replies with errors.Is · interfaces as injection points (a fake Store in tests) · defer and recover per goroutine · min and max for TTL rounding

Standard library: `net`, `io`, `errors`, `strings`, `log/slog`
