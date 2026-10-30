# 9.2 · Interconnect

> One wire between two nodes, carrying a thousand conversations at once.

## Your goal

Write an `rpc` package so one Burrow node can call another. *RPC* (remote procedure call) means "run this function over there and give me the result". The twist is *multiplexing*: instead of opening a new TCP connection for every call, one connection carries many calls at the same time. Each request gets an id; the reply carries the same id; a single goroutine reads replies and hands each one to the caller that is waiting for it. When the connection dies, every caller still waiting must get an error right away, not hang.

Nodes don't use this for anything yet. Chapter 10 builds on it.

## What you're using from before

**Record codec (2.1 · Record).** `wire.WriteRecord` and `wire.ReadRecord` move one length-prefixed blob at a time. Every RPC message is one record: the envelope header followed by the body. You never touch raw TCP framing here; the codec already solved it.

```go
// send: build the envelope bytes, then
err := wire.WriteRecord(conn, env)
// receive, in the reader goroutine:
env, err := wire.ReadRecord(conn)
```

**Accept loop (3.1 · Loopback).** `wire.ServeConns(ln, handle, logger)` accepts connections and runs `handle(conn)` in a goroutine each, returning nil when the listener closes. `Server.Serve` is `ServeConns` with a handler that reads envelopes in a loop and dispatches them:

```go
func (s *Server) Serve(ln net.Listener) error {
	return wire.ServeConns(ln, s.serveConn, s.Logger)
}
```

**Deadline-aware client (3.4 · Deadlines).** The rules from 3.4 carry over to `Call`: a call past `ctx`'s deadline returns `context.DeadlineExceeded` promptly, a cancel returns `context.Canceled`, and a late reply is never handed to a different caller. The difference is that 3.4 closed the connection on a timeout, because it had one call in flight. Here a timed-out call just stops waiting; the connection stays open for everyone else, and its reply is dropped when it finally arrives.

## What's new here

**Multiplexing and request correlation.** A connection is a byte stream, so two callers can't both read from it and hope to get their own reply. Instead every request carries an id, and the reply echoes it. The envelope is a fixed header in front of the body, encoded with `encoding/binary`:

```text
id (8 bytes, big-endian) | method (1 byte) | flags (1 byte) | body
flags: bit 0 = reply, bit 1 = error (body is the error text)
```

`binary.BigEndian.PutUint64(buf[0:8], id)` writes the id; `binary.BigEndian.Uint64(buf[0:8])` reads it back. The whole envelope goes in one record, so the reader gets exactly one message per `ReadRecord`.

**A map of channels under a mutex.** The client keeps `pending map[uint64]chan reply`. `Call` makes a channel with buffer 1, stores it under a fresh id (a counter, incremented under the lock), writes the request, and waits on the channel. The reader goroutine looks the id up, deletes it, and sends the reply. Buffer 1 is what lets the reader send without waiting for a caller that may already have given up.

```go
c.mu.Lock()
id := c.nextID
c.nextID++
ch := make(chan reply, 1)
c.pending[id] = ch
c.mu.Unlock()

select {
case r := <-ch:
	return r.body, r.err
case <-ctx.Done():
	c.mu.Lock()
	delete(c.pending, id) // the reader will drop the late reply
	c.mu.Unlock()
	return nil, ctx.Err()
}
```

**One reader goroutine.** Exactly one goroutine reads from the connection, started in `Dial`. Writers take a separate mutex around `WriteRecord` so two requests can't interleave bytes. When `ReadRecord` fails, the reader is finished, and before it exits it walks the pending map and sends the error to every waiting channel. That is the "fail all pending" rule: a dead connection fails every in-flight call at once, and `Call` after that returns the same error immediately.

```go
func (c *Conn) readLoop() {
	for {
		env, err := wire.ReadRecord(c.conn)
		if err != nil {
			// every pending channel gets err; mark closed
			c.failAll(err)
			return
		}
		// decode, look up pending[id], deliver
	}
}
```

On the server side the mirror image: one goroutine reads requests off the connection and starts a goroutine per call, so a slow handler never blocks the next request on the same connection. Replies are written under the write mutex in whatever order handlers finish; the ids keep them straight.

## What to do

1. Create `rpc/envelope.go`: an envelope struct with `ID uint64`, `Method byte`, `Flags byte`, `Body []byte`, with `encode() []byte` and `decode([]byte) (envelope, error)`. Reject a record shorter than 10 bytes.
2. Create `rpc/server.go`: `Server.Serve` on `wire.ServeConns`. Per connection: read envelopes in a loop; for each, `go` a function that calls `s.Handle(ctx, method, body)`, builds a reply with the same id, the reply flag, and either the result body or the error flag plus `err.Error()` as the body, then writes it under the connection's write mutex. Close the connection when the read loop ends.
3. Create `rpc/conn.go` with `Dial`, `Call`, `Close` and `RemoteError`.
   - `Dial` uses `net.Dialer.DialContext`, starts the reader goroutine, returns the `Conn`.
   - `Call` registers a pending channel, writes the request under the write mutex, then waits on the channel or `ctx.Done()`. A reply with the error flag becomes `*RemoteError{Msg: string(body)}`.
   - The reader decodes each reply, finds and removes the pending channel, and sends. An id with no pending entry (a late reply) is dropped.
   - When the reader exits, record the error, fail every pending call with it, and make later `Call`s return it at once.
   - `Close` closes the socket, which makes the reader exit and fail the rest; it then waits for the reader so nothing is left running. Safe to call twice.
4. Run everything with `-race` and `testkit.NoLeaks`. A connection with a reader goroutine and a per-call goroutine on the server is exactly where leaks hide.

## Example

A server whose only method is "upper-case the body", and a client making calls at the same time on one connection:

```go
upper := func(ctx context.Context, m byte, p []byte) ([]byte, error) {
	if m != 1 {
		return nil, fmt.Errorf("unknown method %d", m)
	}
	return bytes.ToUpper(p), nil
}
srv := &rpc.Server{Handle: upper}
ln, _ := net.Listen("tcp", "127.0.0.1:0")
go srv.Serve(ln)

c, _ := rpc.Dial(ctx, ln.Addr().String())
defer c.Close()

var wg sync.WaitGroup
for i := range 3 {
	wg.Add(1)
	go func() {
		defer wg.Done()
		out, err := c.Call(ctx, 1, []byte(fmt.Sprint("hello ", i)))
		fmt.Println(string(out), err)
	}()
}
wg.Wait()
// HELLO 2 <nil>
// HELLO 0 <nil>
// HELLO 1 <nil>   (any order; each caller got its own reply)

_, err := c.Call(ctx, 9, nil)
var re *rpc.RemoteError
fmt.Println(errors.As(err, &re), re.Msg) // true unknown method 9

ln.Close()                                 // server goes away
_, err = c.Call(ctx, 1, []byte("x"))
fmt.Println(err) // a connection error, at once
```

## Done when

- [ ] 1,000 concurrent `Call`s on one connection each get their own reply back, matched by id
- [ ] Closing the server while calls are in flight fails every pending call within 1 s with a connection error, and later calls fail at once
- [ ] A `ctx` deadline on a slow handler returns `context.DeadlineExceeded` for that call only, and the `Conn` keeps working for the next call
- [ ] A handler error arrives as a `*RemoteError` with the message
- [ ] 100 `Dial`/`Close` cycles leave no goroutine behind

## Reference

```go
package rpc

type Handler func(ctx context.Context, method byte, payload []byte) (
	[]byte, error)

type Server struct {
	Handle Handler
	Logger *slog.Logger
}
func (s *Server) Serve(ln net.Listener) error

type Conn struct { /* yours */ }
func Dial(ctx context.Context, addr string) (*Conn, error)
func (c *Conn) Call(ctx context.Context, method byte, payload []byte) (
	[]byte, error)
func (c *Conn) Close() error

// RemoteError is a handler error that travelled back as text.
type RemoteError struct{ Msg string }
func (e *RemoteError) Error() string

// Envelope, one Record per message:
//   id uint64 | method 1B | flags 1B (reply/error) | body
// Errors travel as text bodies and come back as *RemoteError.
```

- Envelope encode/decode on the Record codec
- `Conn` with a `pending map[uint64]chan reply` and one reader goroutine; writers lock only around `WriteRecord`
- Server dispatch with a goroutine per call
- `ctx` deadlines per call; fail every pending call when the reader exits

## The tests check

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/9.2/files/`, matching the earlier chapters.

Pass when: 1,000 concurrent calls from n1 to n2 over one TCP connection all return correctly; killing n2 mid-flight fails every pending call promptly instead of hanging.

The suite will cover:

- 1,000 concurrent calls, replies matched by id
- Server close fails all pending calls within 1 s with a connection error
- ctx deadline on a slow handler returns DeadlineExceeded and the Conn stays usable
- No goroutine leaks across 100 Dial/Close cycles

## Try it

```bash
./burrow watch
go test -race -run . ./rpc
# then read $(go env GOROOT)/src/net/rpc/client.go: it is this design
```

## In the wild

net/rpc, gRPC over HTTP/2 streams, Cap'n Proto; yamux for the multiplexing alone.

## New in Go

multiplexing · request correlation · reader goroutines · map of channels under a mutex

Standard library: `net`, `sync`, `context`, `encoding/binary`
