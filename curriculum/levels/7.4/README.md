# 7.4 · Watchdog

Make slow clients and slow disks unable to hang your server. Deadlines run end to end: server timeouts, a per-request budget in middleware, and store calls that stop when the context ends.

## Build

```go
// api/middleware.go addition
func Timeout(d time.Duration) Middleware // per-request ctx deadline; a handler that returns ctx's error -> 503 ErrorResponse{code: unavailable}

// Handlers pass r.Context() to every store call. A store that blocks (test fake) makes
// the request fail with 503 after the budget, and the store observes ctx.Done().
// http.Server is configured with ReadHeaderTimeout 5s, ReadTimeout 30s, WriteTimeout 30s, IdleTimeout 60s.
// burrowd serve: on SIGINT/SIGTERM, http.Server.Shutdown and wire.Server.Shutdown run together under one 10 s budget.
```

- `http.Server` with all four timeouts
- `Timeout` middleware and the 503 mapping
- r.Context() threaded into every store call
- Graceful HTTP shutdown next to the frame port's

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/7.4/files/`, matching the earlier chapters.

## Pass when

A store with an injected 5 s delay makes requests fail with 503 after the 1 s budget and sees its context cancelled; a slowloris client is dropped by ReadHeaderTimeout; Ctrl-C drains HTTP and TCP together.

The suite will cover:

- A fake Store that blocks until ctx is done: GET returns 503 unavailable after ~1 s, and the fake recorded that ctx.Err() was context.DeadlineExceeded
- A client that disconnects mid-request: the store's ctx is cancelled (context.Canceled)
- ReadHeaderTimeout: a raw connection that sends half a request line is closed after the timeout; IdleTimeout closes an idle keep-alive connection
- A request that finishes within budget is unaffected; the Timeout middleware never writes a 503 after the handler already wrote
- The real binary: SIGTERM while an HTTP request and a frame request are in flight; both complete; exit 0 within the budget

## Uses

- Middleware chain (built in 7.3 · Signal Chain)
- Shutdown (built in 3.5 · Power Down)

## In the wild

net/http.TimeoutHandler does a version of this; Kubernetes readiness and ingress timeouts assume your server has these set

## New in Go

context deadlines and propagation · http.Server timeouts · http.Server.Shutdown · select on ctx.Done()

Standard library: `net/http`, `context`, `time`
