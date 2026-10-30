# 3.3 · Remote Store

> A store on the other end of a wire should feel exactly like one in the same process. Same methods, same errors.

## Your goal

Write the client side: a `client` package whose `Client` type dials the frame server and offers the same methods as `store.Memory`. Because `*client.Client` satisfies `store.Store`, the conformance suite you passed in chapter 1 runs again here, unchanged, with a TCP connection in the middle. Code that uses a `store.Store` can't tell whether it is talking to memory or to a server.

Alongside it, write `burrowctl`, a command-line tool for talking to the server by hand. The `ctl` suffix is the Unix habit for "control" tools that drive a daemon (`systemctl`, `kubectl`). Until now you poked the server with raw frames in a test; from here on you type `burrowctl get name`.

## What you're using from before

**KV Frame** (2.2 · Frame). Every client method is one request frame out and one reply frame in. `WriteFrame` sends the request, `ReadFrame` collects the reply:

```go
req := wire.Frame{Op: wire.OpGet, Key: key}
if err := wire.WriteFrame(c.conn, req); err != nil {
	return store.Entry{}, err
}
reply, err := wire.ReadFrame(c.conn)
```

**Frame server** (3.2 · Switchboard). The reply table from 3.2 is the client's spec, read backwards: `OpNotFound` becomes `store.ErrNotFound`, `OpMismatch` becomes `store.ErrVersionMismatch`, `OpError` becomes a `*ServerError` carrying the text. The tests start a real `wire.Server` on a loopback port and point your `Dial` at it.

**burrowd skeleton** (0.1 · Power On). `burrowctl` is built the same way as `burrowd`: a one-line `main` and a `run(args, stdout, stderr) int` that the tests call with buffers:

```go
func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
```

## What's new here

**Designing a package API.** The exported surface of `client` is small and mirrors the server's commands: `Dial`, one method per operation, `Close`. Everything they share, the lock, the write, the read, lives in one unexported helper, and each public method is a few lines that build a request and interpret a reply:

```go
// the shared core, unexported
func (c *Client) call(ctx context.Context, req wire.Frame) (
	wire.Frame, error)

// one public method, a thin wrapper
func (c *Client) Delete(ctx context.Context, key string) error {
	reply, err := c.call(ctx, wire.Frame{Op: wire.OpDelete, Key: key})
	// ... map reply.Op to nil, store.ErrNotFound, or an error
}
```

Dialing with a context uses `net.Dialer`, whose `DialContext` gives up when the context does:

```go
var d net.Dialer
conn, err := d.DialContext(ctx, "tcp", addr)
```

**Custom error types and `errors.As`.** A sentinel like `store.ErrNotFound` is one fixed value. When an error needs to carry data (here, the server's message), you define a type with an `Error()` method:

```go
type ServerError struct{ Msg string }

func (e *ServerError) Error() string { return "server: " + e.Msg }
```

`errors.Is` asks "is this error that value?". `errors.As` asks "is there an error of this type in the chain, and if so give it to me":

```go
var se *client.ServerError
if errors.As(err, &se) {
	fmt.Println(se.Msg) // "disk full"
}
```

**`errors.As` versus a type assertion.** `err.(*ServerError)` only looks at the outermost value. The moment anything wraps the error with `fmt.Errorf("...: %w", err)`, the assertion fails while `errors.As` still finds it, because it walks the `Unwrap` chain. Reach for the assertion only when you are holding a value you just made yourself.

**The typed-nil trap.** An interface value is nil only when both its type and its value are nil. This function always returns a non-nil error:

```go
func lookup() error {
	var se *ServerError // nil pointer
	return se           // a non-nil error holding a nil *ServerError
}
// lookup() != nil is true. Callers will think something went wrong.
```

Declare `var err error` and assign a `*ServerError` to it only when there really is one, or return the literal `nil`.

**Mapping errors across a network.** The server can't send a Go error value; it sends an op byte and maybe some text. The client's job is to turn that back into the same sentinels a local store would return, so `errors.Is(err, store.ErrNotFound)` works either way. One `switch` on `reply.Op` does it.

**One connection, one call at a time.** The client keeps a single connection, and the protocol has no request IDs: the reply that comes back is the reply to whatever was sent last. If two goroutines wrote requests at the same time, their replies would get crossed. A `sync.Mutex` held from the write through the read keeps each request/reply pair together:

```go
c.mu.Lock()
defer c.mu.Unlock()
// write the request, read the reply; nobody else touches conn meanwhile
```

**`flag.FlagSet` and subcommands.** The `flag` package parses `-name value` options. The package-level `flag.Parse` reads `os.Args` and exits on error, which is useless in a testable `run`. A `flag.FlagSet` is the same parser as a value you control: you tell it where to print, hand it `args`, and it stops at the first non-flag word, which is your subcommand:

```go
fs := flag.NewFlagSet("burrowctl", flag.ContinueOnError)
fs.SetOutput(stderr)
addr := fs.String("addr", "127.0.0.1:7070", "server address")
timeout := fs.Duration("timeout", 5*time.Second, "per-command timeout")
if err := fs.Parse(args); err != nil {
	return 2 // the FlagSet already printed the problem to stderr
}
rest := fs.Args() // ["get", "name"]: the subcommand and its words
```

A subcommand with its own flags (`cas -ttl 10s ...`) is a second `FlagSet` parsed over `rest[1:]`.

## What to do

1. Create `client/client.go` with `ErrClosed`, `ServerError`, and a `Client` holding a `net.Conn` and a `sync.Mutex`.
2. Write `Dial`: connect with `net.Dialer.DialContext`, return a `*Client`. Don't send anything.
3. Write the unexported `call`: fail with `ctx.Err()` if the context is already done, with `ErrClosed` if the client is closed; otherwise lock, `WriteFrame`, `ReadFrame`, unlock, return the reply.
4. Write the public methods, one per `store.Store` operation plus `Ping`:
   - Every op except `Keys` and `Ping` validates the key with `store.ValidateKey` first and returns the error without sending anything.
   - Build the request frame, call `call`, then map the reply: `OpOK`/`OpValue` to an `Entry`, `OpNotFound` to `store.ErrNotFound`, `OpMismatch` to `store.ErrVersionMismatch`, `OpError` to `&ServerError{Msg: string(reply.Value)}`.
   - Set `Entry.ExpiresAt` to `time.Now()` plus the reply's `TTL` when `TTL > 0`, else leave it zero.
   - `Keys` splits `reply.Value` on `\n`; an empty value is `nil`.
5. Write `Close`: close the connection, mark the client closed, return `nil` on a second call.
6. Add `var _ store.Store = (*Client)(nil)` so the compiler confirms the client is a Store.
7. Create `cmd/burrowctl/main.go` with the same `main`/`run` shape as `burrowd`:
   - Parse `-addr` and `-timeout` with a `FlagSet`, then `switch` on the subcommand.
   - Wrong usage (no command, unknown command, wrong number of words, a version or duration that doesn't parse) prints an explanation to stderr and returns 2.
   - Dial, run the one command with `context.WithTimeout`, and print the result from the table in the Reference. A store error or a connection error goes to stderr with exit 1, and the connection error names the address.
8. Run `gofmt -l .` and `go vet ./...` until both print nothing.

## Example

From Go, the client reads like the local store:

```go
c, err := client.Dial(ctx, "127.0.0.1:7070")
e, _ := c.Set(ctx, "name", []byte("gerard")) // e.Version == 1
e, _ = c.Get(ctx, "name")                     // "gerard", version 1
_, err = c.CompareAndSwap(ctx, "name", 9, []byte("x"))
errors.Is(err, store.ErrVersionMismatch)           // true
_, err = c.Get(ctx, "nope")
errors.Is(err, store.ErrNotFound)                  // true
_, err = c.Set(ctx, "bad key", nil)
errors.Is(err, store.ErrInvalidKey) // true, nothing was sent
```

From the shell, with a server listening on the default port:

```bash
$ burrowctl set name gerard
OK 1
$ burrowctl get name
gerard
$ burrowctl cas name 1 gerard2
OK 2
$ burrowctl cas -ttl 30s lock 0 me
OK 1
$ burrowctl keys
lock
name
$ burrowctl get nope
not found
$ echo $?
1
$ burrowctl cas name 1 stale
version mismatch
$ burrowctl get
usage: burrowctl [-addr host:port] [-timeout 5s] <command> ...
$ echo $?
2
$ burrowctl -addr 127.0.0.1:1 ping
dial 127.0.0.1:1: connection refused
```

## Done when

- [ ] `storetest.Run`, `RunCAS` and `RunTTL` all pass against a `Client` dialled into a real `wire.Server`
- [ ] Entries carry `Version`, and `ExpiresAt` is about now plus the TTL remaining (zero when there is no expiry)
- [ ] Missing keys are `store.ErrNotFound`, stale versions are `store.ErrVersionMismatch`, and an `OpError` reply comes back as a `*client.ServerError` holding the server's text
- [ ] An invalid key or an already-cancelled context fails before anything reaches the server; `Keys` with `a b` is sent as one request
- [ ] `Touch` is one round trip and returns the full entry
- [ ] 50 goroutines sharing one `Client` never get each other's replies
- [ ] `Dial` to a closed port fails fast; after `Close`, calls return `client.ErrClosed` and a second `Close` is `nil`
- [ ] `burrowctl` prints exactly the outputs in the table and uses exit 0, 1 and 2 as described
- [ ] `gofmt -l .` and `go vet ./...` print nothing

## Reference

The exact contract the tests hold you to.

```go
// client/client.go
package client

var ErrClosed = errors.New("client: closed")

// ServerError is returned for OpError replies.
type ServerError struct{ Msg string }
func (e *ServerError) Error() string

// Client holds one connection and a mutex so calls don't interleave.
type Client struct { /* yours */ }

func Dial(ctx context.Context, addr string) (*Client, error)
func (c *Client) Get(ctx context.Context, key string) (
	store.Entry, error)
func (c *Client) Set(ctx context.Context, key string, value []byte) (
	store.Entry, error)
func (c *Client) Delete(ctx context.Context, key string) error
func (c *Client) Keys(ctx context.Context, prefix string) (
	[]string, error)
func (c *Client) CompareAndSwap(ctx context.Context, key string,
	ifVersion uint64, value []byte) (store.Entry, error)
func (c *Client) CompareAndSwapWithTTL(ctx context.Context, key string,
	ifVersion uint64, value []byte, ttl time.Duration) (
	store.Entry, error)
func (c *Client) CompareAndDelete(ctx context.Context, key string,
	ifVersion uint64) error
func (c *Client) SetWithTTL(ctx context.Context, key string,
	value []byte, ttl time.Duration) (store.Entry, error)
func (c *Client) Touch(ctx context.Context, key string,
	ttl time.Duration) (store.Entry, error)
func (c *Client) Ping(ctx context.Context) error
func (c *Client) Close() error

var _ store.Store = (*Client)(nil) // the remote store is a Store
```

| Reply op | Client returns |
|---|---|
| `OpOK`, `OpValue` | the `Entry` (Key, Value, Version, ExpiresAt) and `nil` |
| `OpNotFound` | `store.ErrNotFound` |
| `OpMismatch` | `store.ErrVersionMismatch` |
| `OpError` | `&ServerError{Msg: string(Value)}` |
| `OpPong` | `nil` (from `Ping`) |
| `OpKeyList` | `Value` split on `\n`; `nil` for an empty list |

`burrowctl`:

```text
burrowctl [-addr 127.0.0.1:7070] [-timeout 5s] <command>
  get <key>                              the value, then a newline
  set <key> <value>                      OK <version>
  del <key>                              OK
  keys [prefix]                          one key per line
  cas [-ttl 10s] <key> <version> <value> OK <version>
  cad <key> <version>                    OK
  ttl <key> <duration> <value>           OK <version>
  touch <key> <duration>                 OK <version>
  ping                                   PONG
```

| Outcome | Stream | Exit |
|---|---|---|
| Success | stdout | 0 |
| `not found`, `version mismatch`, `invalid key`, a connection error (naming the address) | stderr | 1 |
| Usage mistake (no command, unknown command, wrong arity, bad number or duration, unknown flag) | stderr | 2 |

Decisions the tests rely on:

- Every op except `Keys` and `Ping` validates the key locally and returns `store.ErrInvalidKey` without sending anything; `Keys` and `Ping` never validate.
- An already-done ctx fails with `ctx.Err()` before anything is sent. (Mid-call deadlines come in 3.4.)
- `Touch` returns the full `Entry` (value and version unchanged, new `ExpiresAt`) from the `OpOK` reply, with no second round trip.
- `Client.Keys` returns nil for an empty list.
- `ExpiresAt` is computed on the client as `time.Now()` plus the reply's `TTL`, so it is approximate to a few milliseconds; zero when `TTL` is 0.
- `Dial` connects and returns; it does not send a `Ping`.
- `burrowctl get` prints the value followed by a newline. The `-ttl` flag of `cas` comes right after the word `cas`.

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
./burrow watch
```

`burrowd` can't serve yet (that arrives in 3.5), so to use `burrowctl` by hand start a server from a throwaway `main`:

```go
ln, _ := net.Listen("tcp", "127.0.0.1:7070")
log.Fatal((&wire.Server{Store: store.NewMemory()}).Serve(ln))
```

Then:

```bash
go run ./cmd/burrowctl set name gerard
go run ./cmd/burrowctl cas -ttl 30s name 1 gerard2
go run ./cmd/burrowctl get name
```

## In the wild

redis-go, etcd clientv3, memcache clients: a client package whose methods mirror the server's commands and whose errors are the server's sentinels.

## New in Go

package API design · errors.As and custom error types · errors.As vs a type assertion · the typed-nil interface trap · mapping errors across a network · mutex-guarded request/reply pairing · net.Dialer.DialContext · flag.FlagSet · subcommands

Standard library: `net`, `context`, `errors`, `flag`, `os`, `time`, `strings`
