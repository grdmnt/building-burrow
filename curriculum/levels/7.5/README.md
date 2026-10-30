# 7.5 · Client Library

> A client library is the server's API as other programs see it. Get the errors, the deadlines and the retries right, and nobody downstream has to think about HTTP.

## Your goal

Write package `burrow`, a Go client for the HTTP API that satisfies `store.Store`, the same interface the memory store, the durable store and the frame client satisfy. A caller gets `Get`, `Set`, `CompareAndSwap` and the rest, and the store's own errors (`store.ErrNotFound`, `store.ErrVersionMismatch`) come back from them, so code that uses a `store.Store` cannot tell it is talking over HTTP. Underneath: one configured `http.Client`, a context on every request, a typed `APIError` carrying the server's error code and request ID, and retries with backoff only where a retry cannot apply a write twice. `burrowctl` gains `-http` so the same commands work over either protocol.

## What you're using from before

**JSON API (7.2 · JSON In and Out).** The client always asks for JSON (`Accept: application/json`), sends `PutRequest` bodies with `Content-Type: application/json`, decodes `KV` and `ErrorResponse`, and `Batch` posts `{"ops": [...]}` to `/v1/batch`. The `api` package's types are the wire format, so reuse them:

```go
var kv api.KV
err := c.do(ctx, "PUT", "/v1/kv/"+url.PathEscape(key),
    api.PutRequest{Value: value, IfVersion: &ifVersion}, &kv)
// kv.Version, kv.ExpiresAt -> store.Entry
```

**Deadline guard (7.4 · Watchdog).** The server answers `503 unavailable` when a request runs past its budget and drops connections that idle too long. The client treats `503` as retryable (for idempotent calls) and gives every request its own `ctx`, so the caller's deadline, not the server's, decides how long to wait.

**Frame client (3.3 · Remote Store).** The method set is the same as `client.Client`'s, and the same conformance suites (`storetest.Run`, `RunCAS`, `RunTTL`) judge it. `burrowctl` keeps its subcommands and output; `-http` just swaps which client it builds.

## What's new here

**`http.Client` and `http.Transport`.** `http.Client` is the high-level API (`Do`, cookies, redirects); the `Transport` underneath owns the TCP connections, including a pool of idle keep-alive connections it reuses between requests. `http.DefaultClient` has no timeouts anywhere, so a library builds its own:

```go
dialer := &net.Dialer{Timeout: 5 * time.Second}
tr := &http.Transport{
    DialContext:           dialer.DialContext,
    ResponseHeaderTimeout: 10 * time.Second, // until the first byte
    MaxIdleConnsPerHost:   16,
    IdleConnTimeout:       90 * time.Second,
}
hc := &http.Client{Transport: tr}
```

A caller who already has a client (a test with `httptest.NewServer`, or a program with its own tracing transport) passes it in `Options.HTTPClient`.

**`http.NewRequestWithContext`.** Build the request with the caller's context and the client's `Do` honours it: a deadline or cancel ends the request, and `Do` returns an error wrapping `context.DeadlineExceeded` or `context.Canceled`. Never `http.Get`, which has no context.

```go
req, err := http.NewRequestWithContext(ctx, "GET", c.base+path, nil)
req.Header.Set("Accept", "application/json")
resp, err := c.hc.Do(req)
```

**Always read and close the body.** A response body is a live stream from the connection. Until it is read to the end and closed, the Transport cannot put the connection back in the pool, so a client that forgets leaks connections and opens a new one for every call. The idiom, even on an error status:

```go
defer resp.Body.Close()
// ... decode what you need, then:
io.Copy(io.Discard, resp.Body) // drain whatever is left
```

**Idempotency and retry safety.** A retry is safe only when doing the operation twice is the same as doing it once, or when you know the first attempt never happened. `GET`, `DELETE` with `If-Match` and a CAS `PUT` are idempotent: the same version check can succeed at most once. A plain `Set` is not: if the first attempt reached the server and the response was lost, a retry writes the value again and bumps the version a second time. So the rule has two halves. Before any response arrived (connection refused, reset, timeout dialing), retry anything. After a response (a `500`, a `503`), retry only idempotent operations.

**Exponential backoff with jitter.** Waiting a fixed time between retries makes every client that failed together retry together. Double the wait each attempt, cap it, and add randomness so the retries spread out:

```go
func defaultBackoff(attempt int) time.Duration {
    d := 50 * time.Millisecond << attempt // 100ms, 200ms, 400ms ...
    if d > time.Second {
        d = time.Second
    }
    return d/2 + rand.N(d/2) // math/rand/v2: half fixed, half random
}
```

Wait in a `select` so a cancelled context ends the backoff at once instead of after the sleep:

```go
select {
case <-time.After(c.backoff(attempt)):
case <-ctx.Done():
    return ctx.Err()
}
```

**One error, two ways to ask about it.** `APIError` is a struct with the status, code, message and request ID. Give it an `Unwrap() error` method that returns the matching store sentinel (`store.ErrNotFound` for `not_found`, `store.ErrVersionMismatch` for `version_mismatch`, `store.ErrInvalidKey` for `invalid_key`). Then `errors.Is(err, store.ErrNotFound)` is true for generic code, and `errors.As(err, &apiErr)` gives a caller the request ID for a log line. One value, both questions answered:

```go
_, err := c.Get(ctx, "missing")
errors.Is(err, store.ErrNotFound)  // true
var apiErr *burrow.APIError
if errors.As(err, &apiErr) {
    log.Println(apiErr.Status, apiErr.Code, apiErr.RequestID)
}
```

## What to do

1. Create `burrow/client.go` with `Options`, `Client`, `New` and `APIError`. `New` trims a trailing `/` from the base URL, fills in the default `http.Client`, `MaxRetries` 3 and the default backoff when the options are zero.
2. Write one private `do(ctx, method, path, body, out any) (*http.Response, error)`:
   - Encode `body` as JSON when it is not nil, set `Accept` and `Content-Type`, build the request with `http.NewRequestWithContext`.
   - Loop up to `MaxRetries` extra times. Retry a transport error (no response) always; retry a `5xx` only when the call is marked idempotent. Back off between attempts, cancellable by `ctx`.
   - On a `2xx`, decode into `out` when it is not nil. On anything else, decode the `ErrorResponse` into an `*APIError`.
   - Read and close the body on every path.
3. Write the public methods. Each one names the endpoint, marks whether it is idempotent, and turns the `KV` into a `store.Entry` (`ExpiresAt` from the pointer, zero when nil). Validate the key locally with `store.ValidateKey` first, as the frame client does.
4. `Keys` decodes a JSON array (nil for an empty one is fine); `Batch` posts the ops and returns the results. Type aliases (`type BatchOp = api.BatchOp`, `type BatchResult = api.BatchResult`) give the `burrow` package those names without copying the structs.
5. In `cmd/burrowctl`, add `-http <url>`, reject it together with `-addr`, and build a `burrow.Client` instead of a `client.Client`. Add a `batch` subcommand that reads a JSON array of ops from stdin and prints the results as JSON.

## Example

```go
c, err := burrow.New("http://127.0.0.1:8080", burrow.Options{})
e, err := c.Set(ctx, "colour", []byte("green"))
// e.Version == 1
e, err = c.CompareAndSwap(ctx, "colour", 1, []byte("blue"))
// e.Version == 2
_, err = c.CompareAndSwap(ctx, "colour", 1, []byte("red"))
// errors.Is(err, store.ErrVersionMismatch) == true
```

With a server that fails twice and then works, `Get` returns the value on the third attempt; with a server that answers `500` to a `Set`, the error comes back after one attempt and the server saw exactly one request.

```bash
$ echo '[{"op":"put","key":"a","value":"aGk="},
        {"op":"get","key":"a"}]' |
    go run ./cmd/burrowctl -http http://127.0.0.1:8080 batch
[{"ok":true,"entry":{"key":"a","value":"aGk=","version":1}},
 {"ok":true,"entry":{"key":"a","value":"aGk=","version":1}}]
$ go run ./cmd/burrowctl -http http://127.0.0.1:8080 get a
hi
$ go run ./cmd/burrowctl -http http://127.0.0.1:8080 -addr :7070 get a
burrowctl: -http and -addr are mutually exclusive
```

## Done when

- [ ] `storetest.Run`, `RunCAS` and `RunTTL` pass with a `burrow.Client` pointed at an `httptest.NewServer` running `api.Server`
- [ ] `404` is `store.ErrNotFound` and also an `*APIError` with the request ID; `412` is `store.ErrVersionMismatch`; `400 invalid_key` is `store.ErrInvalidKey`
- [ ] A server that answers `503` twice then `200` makes `Get` succeed in 3 attempts; a `Set` that got a `500` is not retried; a connection reset before any response is retried for `Set` too
- [ ] The backoff function is called with attempts 1 and 2, and a context cancelled during the wait returns `context.Canceled` promptly
- [ ] A server that sleeps 5 s with a 100 ms context returns `context.DeadlineExceeded` within 1 s, and the connection is not reused afterwards
- [ ] `Keys` and `Batch` decode correctly; a `Batch` of 0 ops works
- [ ] `burrowctl -http` `get`, `set`, `cas` and `batch` print what the frame mode prints and exit with the same codes

## Reference

The exact contract the tests will hold you to.

```go
// burrow/client.go
package burrow

type Options struct {
	// nil: a client whose Transport has timeouts and keep-alive set
	HTTPClient *http.Client
	// default 3; applies to GET, DELETE-with-If-Match, CAS puts, and
	// connection errors
	MaxRetries int
	// default exponential with jitter, capped at 1s
	Backoff func(attempt int) time.Duration
}

type Client struct { /* yours */ }
func New(baseURL string, opts Options) (*Client, error)
func (c *Client) Get(ctx context.Context,
	key string) (store.Entry, error)
func (c *Client) Set(ctx context.Context, key string,
	value []byte) (store.Entry, error)
func (c *Client) SetWithTTL(ctx context.Context, key string,
	value []byte, ttl time.Duration) (store.Entry, error)
func (c *Client) CompareAndSwap(ctx context.Context, key string,
	ifVersion uint64, value []byte) (store.Entry, error)
func (c *Client) CompareAndSwapWithTTL(ctx context.Context,
	key string, ifVersion uint64, value []byte,
	ttl time.Duration) (store.Entry, error)
func (c *Client) CompareAndDelete(ctx context.Context, key string,
	ifVersion uint64) error
func (c *Client) Delete(ctx context.Context, key string) error
func (c *Client) Touch(ctx context.Context, key string,
	ttl time.Duration) (store.Entry, error)
func (c *Client) Keys(ctx context.Context,
	prefix string) ([]string, error)
func (c *Client) Batch(ctx context.Context,
	ops []BatchOp) ([]BatchResult, error)

var _ store.Store = (*Client)(nil)

// APIError carries the server's ErrorResponse and status. It unwraps
// to the store sentinels for not_found, version_mismatch and
// invalid_key, so errors.Is works as with every other Store.
type APIError struct {
	Status    int
	Code      string
	Message   string
	RequestID string
}

// cmd/burrowctl gains -http http://127.0.0.1:8080 (mutually exclusive
// with -addr) and a `batch` subcommand reading JSON ops from stdin.
```

## The tests check

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/7.5/files/`, matching the earlier chapters.

Pass when: storetest.Run, RunCAS and RunTTL pass through the HTTP client against api.Server, a flaky httptest server that fails the first two attempts still succeeds within the retry budget, a non-idempotent Set is never retried after a response, and burrowctl -http round-trips.

The suite will cover:

- storetest.Run, RunCAS, RunTTL against burrow.New(httptest.NewServer(api.Server{...}.Handler()).URL)
- Errors: 404 -> store.ErrNotFound and errors.As *APIError with RequestID; 412 -> store.ErrVersionMismatch; 400 invalid_key -> store.ErrInvalidKey
- Retries: a handler that returns 503 twice then 200 makes Get succeed with 3 attempts; a Set that got a 500 is not retried; a connection reset before any response is retried for Set too
- Backoff is called with attempt 1, 2 and respects ctx: a ctx cancelled during backoff returns context.Canceled promptly
- Deadlines: a server that sleeps 5 s and a 100 ms ctx return context.DeadlineExceeded within 1 s; the Transport's connection is not reused after a timeout (connection count on the server rises)
- Keys and Batch decode correctly; Batch with 0 ops is fine
- burrowctl -http get/set/cas/batch outputs and exit codes mirror the frame mode

## Try it

```bash
./burrow test
go run ./cmd/burrowd serve &
go run ./cmd/burrowctl -http http://127.0.0.1:8080 set colour green
go run ./cmd/burrowctl get colour     # the frame port, same store
```

## In the wild

hashicorp/go-retryablehttp, the Stripe/GitHub Go SDKs; the retry rules are RFC 9110 section 9.2.2 (idempotent methods).

## New in Go

http.Client and http.Transport settings · http.NewRequestWithContext · reading and closing response bodies · idempotency and retry safety · exponential backoff with jitter · an Unwrap method that maps to sentinels

Standard library: `net/http`, `encoding/json`, `context`, `time`, `math/rand/v2`, `io`
