# 7.5 · Client Library

A client library on `http.Client` done properly: a configured Transport, a context per call, typed errors, and retries only where they cannot double-apply a write. This client is what the lock, the watch client, devcluster and the load generator all use.

## Build

```go
// burrow/client.go
package burrow

type Options struct {
	HTTPClient *http.Client  // nil: a client with a Transport configured for timeouts and keep-alive
	MaxRetries int           // default 3; applies to GET, DELETE-with-If-Match, CAS puts, and connection errors
	Backoff    func(attempt int) time.Duration // default exponential with jitter, capped at 1s
}

type Client struct { /* yours */ }
func New(baseURL string, opts Options) (*Client, error)
func (c *Client) Get(ctx context.Context, key string) (store.Entry, error)
func (c *Client) Set(ctx context.Context, key string, value []byte) (store.Entry, error)
func (c *Client) SetWithTTL(ctx context.Context, key string, value []byte, ttl time.Duration) (store.Entry, error)
func (c *Client) CompareAndSwap(ctx context.Context, key string, ifVersion uint64, value []byte) (store.Entry, error)
func (c *Client) CompareAndSwapWithTTL(ctx context.Context, key string, ifVersion uint64, value []byte, ttl time.Duration) (store.Entry, error)
func (c *Client) CompareAndDelete(ctx context.Context, key string, ifVersion uint64) error
func (c *Client) Delete(ctx context.Context, key string) error
func (c *Client) Touch(ctx context.Context, key string, ttl time.Duration) (store.Entry, error)
func (c *Client) Keys(ctx context.Context, prefix string) ([]string, error)
func (c *Client) Batch(ctx context.Context, ops []BatchOp) ([]BatchResult, error)

var _ store.Store = (*Client)(nil) // the third Store

// APIError carries the server's ErrorResponse and status; store sentinels are returned for
// not_found and version_mismatch so errors.Is works as with every other Store.
type APIError struct { Status int; Code string; Message string; RequestID string }

// cmd/burrowctl gains -http http://127.0.0.1:8080 (mutually exclusive with -addr) and a `batch` subcommand reading JSON ops from stdin.
```

- `burrow.Client` on a configured `http.Client`
- Typed errors and store sentinels
- Retries with backoff only for idempotent operations and connection errors
- `burrowctl -http`

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/7.5/files/`, matching the earlier chapters.

## Pass when

storetest.Run, RunCAS and RunTTL pass through the HTTP client against api.Server, a flaky httptest server that fails the first two attempts still succeeds within the retry budget, a non-idempotent Set is never retried after a response, and burrowctl -http round-trips.

The suite will cover:

- storetest.Run, RunCAS, RunTTL against burrow.New(httptest.NewServer(api.Server{...}.Handler()).URL)
- Errors: 404 -> store.ErrNotFound and errors.As *APIError with RequestID; 412 -> store.ErrVersionMismatch; 400 invalid_key -> store.ErrInvalidKey
- Retries: a handler that returns 503 twice then 200 makes Get succeed with 3 attempts; a Set that got a 500 is not retried; a connection reset before any response is retried for Set too
- Backoff is called with attempt 1, 2 and respects ctx: a ctx cancelled during backoff returns context.Canceled promptly
- Deadlines: a server that sleeps 5 s and a 100 ms ctx return context.DeadlineExceeded within 1 s; the Transport's connection is not reused after a timeout (connection count on the server rises)
- Keys and Batch decode correctly; Batch with 0 ops is fine
- burrowctl -http get/set/cas/batch outputs and exit codes mirror the frame mode

## Uses

- JSON API (built in 7.2 · JSON In and Out)
- Deadline guard (built in 7.4 · Watchdog)
- Frame client (built in 3.3 · Remote Store)

## In the wild

hashicorp/go-retryablehttp, the Stripe/GitHub Go SDKs; the retry rules are RFC 9110 section 9.2.2 (idempotent methods)

## New in Go

http.Client and http.Transport settings · http.NewRequestWithContext · reading and closing response bodies · idempotency and retry safety · exponential backoff with jitter · errors.As with custom types

Standard library: `net/http`, `encoding/json`, `context`, `time`, `math/rand/v2`, `io`
