# 2.4 · Client

A frame server, a Go client package that behaves well when the network doesn't, and a CLI.

## Build

```go
// wire/server.go
type Server struct {
	Store  store.Store
	Logger *slog.Logger // nil means no logging
}

func (s *Server) Serve(ln net.Listener) error
```

Server replies, each echoing the request's key:

| Request | Reply |
|---|---|
| `OpGet` | `OpValue` with the value, or `OpNotFound` |
| `OpSet` | `OpOK` |
| `OpDelete` | `OpOK` or `OpNotFound` |
| `OpPing` | `OpPong` echoing the request's value |
| invalid key | `OpError`, value is the error text; connection stays open |
| a reply op (e.g. `OpOK`) sent as a request | `OpError` |
| unknown op byte | one `OpError`, then close: the stream can't be trusted |
| oversized frame | close (optionally after one `OpError`) |

```go
// client/client.go
var ErrClosed = errors.New("client closed")

type ServerError struct{ Msg string } // returned for OpError replies

func Dial(ctx context.Context, addr string) (*Client, error)
func (c *Client) Get(ctx context.Context, key string) ([]byte, error) // missing: store.ErrNotFound
func (c *Client) Set(ctx context.Context, key string, value []byte) error
func (c *Client) Delete(ctx context.Context, key string) error
func (c *Client) Ping(ctx context.Context) error
func (c *Client) Close() error
```

```
// cmd/burrowctl
burrowctl [-addr 127.0.0.1:7070] [-timeout 5s] get <key> | set <key> <value> | del <key> | ping
```

Prints the value, `OK` or `PONG`. Errors go to stderr with exit 1 (`not found`, `invalid key`, can't connect, timeout). Usage mistakes exit 2. Use the same testable `run(args, stdout, stderr) int` shape as `burrowd`.

## The tests check

- Every server row above, pipelining (100 requests in one write), 40 concurrent clients.
- The client returns `store.ErrNotFound` for missing keys, so code can't tell a remote store from a local one.
- 50 goroutines sharing one `Client` never get each other's replies.
- **Deadlines:** a call past its context deadline returns within a second with an error matching `context.DeadlineExceeded`, not just a network timeout.
- **Cancellation:** cancelling mid-call returns `context.Canceled` promptly. A deadline alone can't see a cancel.
- An already-cancelled context fails without sending anything.
- **No stale replies:** after a timeout, the late reply is still in flight. The next call must not receive it.
- When the server hangs up mid-call, that call errors and the next one reconnects.
- `Dial` respects its context, fails fast on a closed port, and calls after `Close` return `ErrClosed`.
- CLI exit codes, messages and `-timeout`.

## New in Go

`net.Dialer.DialContext` · `SetDeadline` · `context.AfterFunc` · mapping errors across a network · `errors.As` · `flag.FlagSet`
