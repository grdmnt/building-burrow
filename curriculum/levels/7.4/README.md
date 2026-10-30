# 7.4 · Watchdog

> A server with no deadlines is a server that can be hung by one slow client or one slow disk. Give every wait a limit.

## Your goal

Make it impossible for a slow client or a slow store to tie up `burrowd`'s HTTP side. Three things, at three layers. The `http.Server` gets its four timeouts, so a client that sends half a request and stops (a "slowloris") is dropped instead of holding a connection forever. A `Timeout` middleware gives each request a budget by putting a deadline on its context, and handlers pass that context to every store call, so a store that blocks is cancelled and the client gets a `503 Service Unavailable` instead of waiting. And on Ctrl-C the HTTP server shuts down gracefully, next to the frame server, under the same 10 s budget.

## What you're using from before

**Middleware chain (7.3 · Signal Chain).** `Timeout(d)` is one more `Middleware` in the same shape as `RequestID` and `AccessLog`. It goes inside the chain so the access log still records the `503`:

```go
return Chain(mux, Recover(), RequestID(), AccessLog(s.Logger),
    Timeout(time.Second))
```

The `503` itself is written by the same `writeError` helper every other error uses, with the code `unavailable`.

**Shutdown (3.5 · Power Down).** `burrowd serve` already stops the frame server with `wire.Server.Shutdown(ctx)` under a 10 s budget from `signal.NotifyContext`. The HTTP server gets the same treatment with `http.Server.Shutdown(ctx)`, and the two run together, not one after the other:

```go
<-ctx.Done()
budget := 10 * time.Second
sctx, cancel := context.WithTimeout(context.Background(), budget)
defer cancel()
var wg sync.WaitGroup
wg.Add(2)
go func() { defer wg.Done(); frameSrv.Shutdown(sctx) }()
go func() { defer wg.Done(); httpSrv.Shutdown(sctx) }()
wg.Wait()
```

This replaces the `httpSrv.Close()` from 7.1.

## What's new here

**Context deadlines, and passing them down.** `context.WithTimeout(parent, d)` returns a child context whose `Done()` channel closes after `d` (or when the parent ends, whichever is first) and whose `Err()` then reports `context.DeadlineExceeded`. Every `*http.Request` already carries a context, `r.Context()`, which the server cancels when the client disconnects. The middleware derives a child with a deadline and hands the request on with `r.WithContext(ctx)`:

```go
func Timeout(d time.Duration) Middleware {
    return func(next http.Handler) http.Handler {
        fn := func(w http.ResponseWriter, r *http.Request) {
            ctx, cancel := context.WithTimeout(r.Context(), d)
            defer cancel() // always release the timer, even on success
            next.ServeHTTP(w, r.WithContext(ctx))
        }
        return http.HandlerFunc(fn)
    }
}
```

That only helps if the context reaches the code that waits. Every store method takes a `ctx` as its first argument (the rule since 1.1), so handlers pass `r.Context()` instead of `context.Background()`, and the deadline travels from the middleware through the handler into the store.

**`select` on `ctx.Done()`.** The store side of the bargain is to stop when the context ends. Anything that waits does so in a `select` with `ctx.Done()` as one of the cases:

```go
select {
case <-ctx.Done():
    // ctx.Err() is context.DeadlineExceeded or context.Canceled
    return store.Entry{}, ctx.Err()
case e := <-result:
    return e, nil
}
```

The test suite uses a fake store that does exactly this, blocking until `ctx.Done()` and recording `ctx.Err()`, to prove the handler passed the right context.

**Mapping the error, once.** A handler that gets `ctx.Err()` back from the store answers `503` with code `unavailable`. Put that mapping in `writeError` next to the others (`errors.Is(err, context.DeadlineExceeded)`), not in the middleware. The middleware cannot know whether the handler already started writing a response, and two writers on one response is a corrupted reply, so it never writes anything itself.

**`http.Server` timeouts.** A zero-value `http.Server` has no timeouts at all, and `http.ListenAndServe` uses one. The four fields:

- `ReadHeaderTimeout`: how long the server waits for the request line and headers. This is the slowloris defence.
- `ReadTimeout`: the whole request, body included.
- `WriteTimeout`: from the end of the request headers until the response is written.
- `IdleTimeout`: how long a keep-alive connection may sit between requests.

```go
srv := &http.Server{
    Handler:           h,
    ReadHeaderTimeout: 5 * time.Second,
    ReadTimeout:       30 * time.Second,
    WriteTimeout:      30 * time.Second,
    IdleTimeout:       60 * time.Second,
}
```

**`http.Server.Shutdown`.** `Shutdown(ctx)` closes the listener, waits for in-flight requests to finish and idle connections to close, and returns `ctx.Err()` if the budget runs out first. While it runs, `Serve` returns `http.ErrServerClosed`, which is not a failure and should not be logged as one. The same states (new, active, idle) that your `conntrack` tracked in 3.5 are what `Shutdown` tracks internally.

## What to do

1. Add `Timeout(d)` to `api/middleware.go` and put it innermost in `Server.Handler()`'s chain with a 1 s budget.
2. Change every store call in `api` from `context.Background()` to `r.Context()`.
3. Map `context.DeadlineExceeded` (and `context.Canceled`) to `503` with code `unavailable` in `writeError`.
4. In `cmd/burrowd`, build the `http.Server` with all four timeouts.
5. On `SIGINT`/`SIGTERM`, run `httpSrv.Shutdown` and the frame server's `Shutdown` concurrently under one 10 s context, wait for both, then snapshot and close the store as in 5.1. Treat `http.ErrServerClosed` from `Serve` as normal.

## Example

A request against a store that never answers, with the 1 s budget:

```bash
$ time curl -s localhost:8080/v1/kv/slow
{"error":"request timed out","code":"unavailable"}
real    0m1.01s
```

A client that sends half a request line and stops is cut off by `ReadHeaderTimeout`:

```bash
$ (printf 'GET /v1/kv/x HT'; sleep 10) | nc localhost 8080
# nc exits after about 5 s with no response: the server closed it
```

And in a test, the fake store that proves the context arrived:

```go
type blockingStore struct {
    store.Store
    gotErr error
}

func (b *blockingStore) Get(ctx context.Context, key string) (
    store.Entry, error) {
    <-ctx.Done()
    b.gotErr = ctx.Err()
    return store.Entry{}, ctx.Err()
}
// GET through api.Server: 503 after ~1 s,
// b.gotErr == context.DeadlineExceeded
```

## Done when

- [ ] A `GET` against a store that blocks until `ctx.Done()` returns `503 unavailable` after about 1 s, and the store saw `context.DeadlineExceeded`
- [ ] A client that disconnects mid-request makes the store's context end with `context.Canceled`
- [ ] A raw connection that sends half a request line is closed by `ReadHeaderTimeout`; an idle keep-alive connection is closed by `IdleTimeout`
- [ ] A request that finishes within the budget is unaffected, and nothing writes a `503` after the handler has already written
- [ ] `SIGTERM` with an HTTP request and a frame request in flight lets both finish and exits 0 within the budget

## Reference

The exact contract the tests will hold you to.

```go
// api/middleware.go addition
// per-request ctx deadline; a handler that returns ctx's error
// -> 503 ErrorResponse{code: unavailable}
func Timeout(d time.Duration) Middleware

// Handlers pass r.Context() to every store call. A store that blocks
// (test fake) makes the request fail with 503 after the budget, and the
// store observes ctx.Done().
// http.Server is configured with ReadHeaderTimeout 5s, ReadTimeout 30s,
// WriteTimeout 30s, IdleTimeout 60s.
// burrowd serve: on SIGINT/SIGTERM, http.Server.Shutdown and
// wire.Server.Shutdown run together under one 10 s budget.
```

## The tests check

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/7.4/files/`, matching the earlier chapters.

Pass when: a store with an injected 5 s delay makes requests fail with 503 after the 1 s budget and sees its context cancelled; a slowloris client is dropped by ReadHeaderTimeout; Ctrl-C drains HTTP and TCP together.

The suite will cover:

- A fake Store that blocks until ctx is done: GET returns 503 unavailable after ~1 s, and the fake recorded that ctx.Err() was context.DeadlineExceeded
- A client that disconnects mid-request: the store's ctx is cancelled (context.Canceled)
- ReadHeaderTimeout: a raw connection that sends half a request line is closed after the timeout; IdleTimeout closes an idle keep-alive connection
- A request that finishes within budget is unaffected; the Timeout middleware never writes a 503 after the handler already wrote
- The real binary: SIGTERM while an HTTP request and a frame request are in flight; both complete; exit 0 within the budget

## Try it

```bash
./burrow test
go run ./cmd/burrowd serve &
(printf 'GET / HT'; sleep 10) | nc localhost 8080   # dropped after 5 s
kill -INT %1
```

## In the wild

net/http.TimeoutHandler does a version of this; Kubernetes readiness and ingress timeouts assume your server has these set.

## New in Go

context.WithTimeout and propagation through r.Context() · select on ctx.Done() · http.Server timeouts · http.Server.Shutdown

Standard library: `net/http`, `context`, `time`
