# 9.2 · Interconnect

Nodes talk to each other over your own record codec. Many calls share one connection; a reader goroutine routes replies by id and fails every pending call when the link dies.

## Build

```go
package rpc

type Handler func(ctx context.Context, method byte, payload []byte) ([]byte, error)
type Server struct { Handle Handler; Logger *slog.Logger }
func (s *Server) Serve(ln net.Listener) error

type Conn struct { /* yours */ }
func Dial(ctx context.Context, addr string) (*Conn, error)
func (c *Conn) Call(ctx context.Context, method byte, payload []byte) ([]byte, error)
func (c *Conn) Close() error
// Envelope payload: id uint64 | method 1B | flags 1B (reply/error) | body. Errors travel as text bodies -> *RemoteError.
```

- Envelope encode/decode on the Record codec
- Conn with a pending map[uint64]chan reply and one reader goroutine
- Server dispatch with per-call goroutines
- ctx deadlines and fail-all-pending on reader exit

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/9.2/files/`, matching the earlier chapters.

## Pass when

1,000 concurrent calls from n1 to n2 over one TCP connection all return correctly; killing n2 mid-flight fails every pending call promptly instead of hanging.

The suite will cover:

- 1,000 concurrent calls, replies matched by id
- Server close fails all pending calls within 1 s with a connection error
- ctx deadline on a slow handler returns DeadlineExceeded and the Conn stays usable
- No goroutine leaks across 100 Dial/Close cycles

## Uses

- Record codec (built in 2.1 · Record)
- Accept loop (built in 3.1 · Loopback)
- Deadline-aware client (built in 3.4 · Deadlines)

## In the wild

net/rpc, gRPC over HTTP/2 streams, Cap'n Proto; yamux for the multiplexing alone

## New in Go

multiplexing · request correlation · reader goroutines · map of channels under a mutex

Standard library: `net`, `sync`, `context`, `encoding/binary`
