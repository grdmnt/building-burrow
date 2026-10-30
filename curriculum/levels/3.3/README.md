# 3.3 · Remote Store

A client package that behaves like a local store. `client.Client` satisfies `store.Store`, so the conformance suite from chapter 1 runs unchanged over TCP. A `burrowctl` CLI rides along so you stop hand-typing frames.

## Build

```go
// client/client.go
package client

var ErrClosed = errors.New("client: closed")

// ServerError is returned for OpError replies.
type ServerError struct{ Msg string }
func (e *ServerError) Error() string

type Client struct { /* yours: one conn, a mutex so calls don't interleave */ }

func Dial(ctx context.Context, addr string) (*Client, error)
func (c *Client) Get(ctx context.Context, key string) (store.Entry, error)                   // store.ErrNotFound when missing
func (c *Client) Set(ctx context.Context, key string, value []byte) (store.Entry, error)
func (c *Client) Delete(ctx context.Context, key string) error
func (c *Client) Keys(ctx context.Context, prefix string) ([]string, error)
func (c *Client) CompareAndSwap(ctx context.Context, key string, ifVersion uint64, value []byte) (store.Entry, error) // store.ErrVersionMismatch
func (c *Client) CompareAndSwapWithTTL(ctx context.Context, key string, ifVersion uint64, value []byte, ttl time.Duration) (store.Entry, error)
func (c *Client) CompareAndDelete(ctx context.Context, key string, ifVersion uint64) error
func (c *Client) SetWithTTL(ctx context.Context, key string, value []byte, ttl time.Duration) (store.Entry, error)
func (c *Client) Touch(ctx context.Context, key string, ttl time.Duration) (store.Entry, error)
func (c *Client) Ping(ctx context.Context) error
func (c *Client) Close() error

var _ store.Store = (*Client)(nil) // the remote store is a Store

// Entry.ExpiresAt on the client is now + TTL remaining from the reply (zero when TTL is 0).
// Invalid keys are rejected locally with store.ErrInvalidKey before anything is sent, for every op
// except Keys and Ping, which skip validation on both sides. Touch returns the full Entry (value
// unchanged, new ExpiresAt) from the OpOK reply without a second round trip.
// An already-done ctx fails with ctx.Err() before anything is sent. (Mid-call deadlines: 3.4.)

// cmd/burrowctl
//   burrowctl [-addr 127.0.0.1:7070] [-timeout 5s] get <key> | set <key> <value> | del <key> | keys [prefix] | cas [-ttl 10s] <key> <version> <value> | cad <key> <version> | ttl <key> <duration> <value> | touch <key> <duration> | ping
// Prints the value (get), `OK <version>` (set/cas/ttl/touch), `OK` (del/cad), one key per line (keys), `PONG`.
// Errors to stderr with exit 1 (`not found`, `version mismatch`, `invalid key`, connection errors). Usage mistakes exit 2.
// Same run(args, stdout, stderr) int shape as burrowd.
```

- `client.Dial` and one method per Store operation, mapping reply ops back to the store's sentinel errors
- A mutex per call so 50 goroutines can share one Client
- `cmd/burrowctl` with `flag.FlagSet`, subcommands and exit codes

Decisions the tests rely on:

- Every op except `Keys` and `Ping` validates the key locally and returns `store.ErrInvalidKey` without sending anything; `Keys` and `Ping` never validate.
- `Touch` returns the full `Entry` (value and version unchanged, new `ExpiresAt`) from the `OpOK` reply, with no second round trip.
- `Client.Keys` returns nil for an empty list.
- `ExpiresAt` is computed on the client as `time.Now()` plus the reply's `TTL`, so it is approximate to a few milliseconds; zero when `TTL` is 0.
- `Dial` connects and returns; it does not send a `Ping`.
- `burrowctl get` prints the value followed by a newline. The `-ttl` flag of `cas` comes right after the word `cas`.

Two traps on the way:

- A typed nil is not a nil interface. `var e *ServerError; return e` from a function returning `error` gives the caller a non-nil `error` holding a nil pointer, and `err != nil` is true. Declare `var err error` and only assign a `*ServerError` when there is one, or return the literal `nil`.
- `errors.As(err, &target)` walks the `Unwrap` chain; a type assertion `err.(*ServerError)` only looks at the outermost value and breaks the moment anyone wraps the error with `%w`. Use the assertion only when you hold the value you just made.

## The tests check

- storetest.Run, storetest.RunCAS and storetest.RunTTL pass with a factory that starts a wire.Server and dials it (RunTTL is on the real clock with 60 ms TTLs)
- Entries carry Version and ExpiresAt: after a SetWithTTL of 1 minute, Get's ExpiresAt is about now + 1 minute; a key without TTL has a zero ExpiresAt
- Missing keys are store.ErrNotFound and mismatches are store.ErrVersionMismatch, so code can't tell a remote store from a local one
- An OpError reply becomes *client.ServerError (errors.As) carrying the server's text
- Validation is local: an invalid key (including the empty key) is store.ErrInvalidKey and an already-cancelled ctx is context.Canceled, with zero requests reaching the server; Keys with prefix `a b` is sent as exactly one request
- Touch returns the full Entry (unchanged Value and Version, new ExpiresAt) and the server saw exactly one request
- Binary values with NUL bytes and a 1 MiB value
- 50 goroutines x 100 calls sharing one Client: every Get returns that goroutine's own value
- Dial to a closed port fails fast; calls after Close return client.ErrClosed; a second Close returns nil
- burrowctl: set prints `OK 1`, get prints the value, cas (with and without -ttl), ttl, touch, keys (all and by prefix), cad, del, ping; `version mismatch`, `not found` and `invalid key` exit 1 with the message on stderr; usage mistakes exit 2 and explain on stderr; an unreachable server exits 1, names the address and gives up quickly

## Try it

```bash
go run ./cmd/burrowd serve &
go run ./cmd/burrowctl set name gerard
go run ./cmd/burrowctl cas -ttl 30s name 1 gerard2
go run ./cmd/burrowctl get name
```

## Uses

- KV Frame (built in 2.2 · Frame)
- Frame server (built in 3.2 · Switchboard)
- burrowd skeleton (built in 0.1 · Power On)

## In the wild

redis-go, etcd clientv3, memcache clients: a client package whose methods mirror the server's commands and whose errors are the server's sentinels

## New in Go

package API design · errors.As and custom error types · errors.As vs a type assertion · the typed-nil interface trap · mapping errors across a network · mutex-guarded request/reply pairing · flag.FlagSet · subcommands

Standard library: `net`, `context`, `errors`, `flag`, `os`, `time`, `strings`
