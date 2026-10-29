# 8.5 · Watch Client

A streaming response as an iterator. `for ev, err := range c.Watch(ctx, prefix, 0)` reads like a loop over a slice, reconnects on its own, and stops cleanly when you break.

## Build

```go
func (c *Client) Watch(ctx context.Context, prefix string, afterID uint64) iter.Seq2[watch.Event, error]
// Reconnects with backoff on connection loss using the last seen ID; yields a non-nil error once
// and stops only for ctx errors or a reset the caller must handle.
```

- An SSE parser on bufio.Reader
- Watch as a range-over-func iterator
- Reconnect with Last-Event-ID

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/8.5/files/`, matching the earlier chapters.

## Pass when

A consumer `for ev, err := range c.Watch(ctx, "user:", 0)` sees every write in order across a server restart, and breaking out of the loop closes the HTTP response and leaves no goroutine.

The suite will cover:

- Events in order across a forced server close/reopen (httptest.Server.CloseClientConnections)
- break stops the iterator and the response body is closed (testkit.NoLeaks)
- ctx cancel ends the loop with context.Canceled
- A reset event is surfaced as watch.ErrReset

## Uses

- Resumable stream (built in 8.4 · Resume)
- HTTP client lib (built in 7.5 · Client Library)

## In the wild

etcd clientv3.Watcher; Kubernetes client-go informers; iter.Seq2 is the Go 1.23 shape for exactly this

## New in Go

iter.Seq2 producers · bufio line parsing · resource cleanup in iterators

Standard library: `iter`, `bufio`, `net/http`, `context`
