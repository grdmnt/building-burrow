# 8.5 · Watch Client

> A stream you can `range` over, that reconnects on its own and stops the moment you `break`.

## Your goal

Give `burrow.Client` a `Watch` method. The caller writes `for ev, err := range c.Watch(ctx, "user:", 0)` and gets events one at a time, as if looping over a slice. Underneath, `Watch` opens the SSE stream from 8.3, parses it line by line, remembers the last ID it saw, and when the connection drops it reconnects with `Last-Event-ID` so the caller misses nothing. When the caller breaks out of the loop or cancels `ctx`, the HTTP response is closed and no goroutine is left behind.

## What you're using from before

**Resumable stream (8.4 · Resume).** `GET /v1/watch?prefix=` with a `Last-Event-ID` header replays what the client missed, then goes live, and answers `event: reset` when the ID is too old. The client sends the header on every reconnect and treats `reset` as an error the caller must handle:

```text
GET /v1/watch?prefix=user:
Accept: text/event-stream
Last-Event-ID: 41
```

**HTTP client lib (7.5 · Client Library).** `Watch` is a method on the same `*Client`, uses its base URL and its configured `http.Client`, and builds the request with `http.NewRequestWithContext(ctx, ...)` so cancelling `ctx` tears down the connection. The retry rules from 7.5 do not apply here: a stream is not retried, it is reconnected, with the backoff function from `Options`.

```go
u := c.base + "/v1/watch?" + q
req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
req.Header.Set("Last-Event-ID", strconv.FormatUint(lastID, 10))
resp, err := c.http.Do(req)
```

## What's new here

**Iterators with `iter.Seq2`.** Since Go 1.23, `range` works over functions. An `iter.Seq2[K, V]` is a function that takes a `yield` callback and calls it once per element; the loop body *is* the `yield`. When the caller breaks, `yield` returns false and the iterator function must return:

```go
func Countdown(n int) iter.Seq2[int, error] {
	return func(yield func(int, error) bool) {
		for i := n; i > 0; i-- {
			if !yield(i, nil) {
				return // the caller broke out of the loop
			}
		}
	}
}

for i := range Countdown(3) {
	fmt.Println(i) // 3 2 1
}
```

The second type parameter is the error, which is the Go convention for a stream that can fail part way: `for ev, err := range ...` with `if err != nil { break }` inside. The iterator function is also where the resources live. `defer resp.Body.Close()` inside it runs when the function returns, which happens either when the stream ends or when `yield` returns false. That is what makes "break closes the response" automatic.

**Line parsing with `bufio`.** `bufio.NewReader(resp.Body)` turns a byte stream into lines with `ReadString('\n')` (or a `bufio.Scanner`). SSE is parsed by accumulating fields until a blank line:

```text
id: 42           -> id = 42
event: put       -> event = "put"
data: {...}      -> data = {...}
                 -> blank line: one event is complete, emit it
: heartbeat      -> comment, ignore
```

Strip the field name and the single optional space after the colon. A `data` field holds the JSON `KV` from 7.2, which `json.Unmarshal` turns back into a `store.Entry` for the `watch.Event`.

**Cleanup inside an iterator.** The three ways the loop can end, and what each must do:

- The caller breaks: `yield` returns false. Return; the deferred `Close` runs.
- `ctx` is cancelled: `resp.Body` reads fail. Yield `ctx.Err()` once, then return.
- The connection drops for any other reason: do not yield anything. Close the body, wait `Backoff(attempt)` (watching `ctx`), reconnect with the last ID, keep going.

A `reset` event means the server could not replay: yield `watch.ErrReset` once and return. The caller decides whether to re-list keys and start again from 0.

## What to do

1. Add `var ErrReset` to package `watch`.
2. Write a small SSE parser in `burrow/`: a function that takes a `*bufio.Reader` and returns the next complete event (id, event, data) or an error. Comments and unknown fields are skipped. `io.EOF` mid-stream is a dropped connection.
3. Write `Watch(ctx, prefix, afterID) iter.Seq2[watch.Event, error]`. Inside the returned function:
   - Keep `lastID := afterID` and `attempt := 0`.
   - Loop: build the request with `ctx`, the prefix and `Last-Event-ID: lastID` (send it even for 0). `Do` it. A non-200 status is an error: yield it once and return.
   - Read events. For each: if `event` is `reset`, yield `(watch.Event{}, watch.ErrReset)` and return. Otherwise decode `data`, set `lastID`, reset `attempt`, and `if !yield(ev, nil) { return }`.
   - On a read error: close the body. If `ctx.Err() != nil`, yield it once and return. Otherwise `attempt++`, sleep `Backoff(attempt)` with a `select` on `ctx.Done()`, and reconnect.
4. Close the response body on every path out of the function, including the one where `yield` returns false. One `defer` per connection, in a helper that handles a single connection, is the clean way.
5. Run the tests with `-race` and `testkit.NoLeaks`: after `break`, the goroutine count must return to what it was.

## Example

```go
c, _ := burrow.New("http://127.0.0.1:8080", burrow.Options{})
ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
defer cancel()

for ev, err := range c.Watch(ctx, "user:", 0) {
	if errors.Is(err, watch.ErrReset) {
		fmt.Println("too far behind, re-list and start over")
		break
	}
	if err != nil {
		fmt.Println("stopped:", err) // context deadline exceeded
		break
	}
	fmt.Println(ev.ID, ev.Type, ev.Entry.Key)
	if ev.ID == 10 {
		break // closes the response, nothing left running
	}
}
```

Drop the connection while this loop runs (a proxy restart, a flaky network, or `CloseClientConnections` in a test). The loop pauses, the client reconnects with `Last-Event-ID: 8`, the server replays what was missed, and the numbers continue without a gap:

```text
7 put user:3
8 put user:4
          (connection dropped here; reconnect with Last-Event-ID: 8)
9 put user:5
10 delete user:3
```

## Done when

- [ ] Events arrive in order across a server that closes all client connections mid-stream (`httptest.Server.CloseClientConnections`), with the client reconnecting on its own
- [ ] `break` stops the iterator, the response body is closed and `testkit.NoLeaks` sees no goroutine left
- [ ] Cancelling `ctx` ends the loop with one `context.Canceled` error
- [ ] A `reset` event from the server is yielded as `watch.ErrReset`
- [ ] `go test -race ./burrow` is clean

## Reference

```go
// burrow/watch.go
package burrow

// Watch streams events for prefix, starting after afterID. It
// reconnects with backoff on connection loss using the last seen ID. It
// yields a non-nil error once and stops only for ctx errors or a reset
// the caller must handle.
func (c *Client) Watch(ctx context.Context, prefix string,
	afterID uint64) iter.Seq2[watch.Event, error]

// package watch
// ErrReset: the suite refers to it by name.
var ErrReset = errors.New("watch: reset")
```

- An SSE parser on `bufio.Reader`: accumulate `id`, `event`, `data` until a blank line, then emit
- `Watch` as a range-over-func iterator that owns its response: `defer resp.Body.Close()` inside it, and return when `yield` returns false
- Reconnect with `Last-Event-ID` set to the last ID seen, using `Options.Backoff`

## The tests check

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/8.5/files/`, matching the earlier chapters.

Pass when: a consumer `for ev, err := range c.Watch(ctx, "user:", 0)` sees every write in order across a server restart, and breaking out of the loop closes the HTTP response and leaves no goroutine.

The suite will cover:

- Events in order across a forced server close/reopen (httptest.Server.CloseClientConnections)
- break stops the iterator and the response body is closed (testkit.NoLeaks)
- ctx cancel ends the loop with context.Canceled
- A reset event is surfaced as watch.ErrReset

## Try it

```bash
./burrow watch
go run ./cmd/burrowd serve -http 127.0.0.1:8080 &
# a scratch main with the loop above; in another terminal:
for i in 1 2 3; do
  curl -s -X PUT localhost:8080/v1/kv/user:$i -d v$i
done
kill %1; go run ./cmd/burrowd serve -http 127.0.0.1:8080 &  # reconnects
```

## In the wild

etcd clientv3.Watcher; Kubernetes client-go informers; iter.Seq2 is the Go 1.23 shape for exactly this.

## New in Go

iter.Seq2 producers · bufio line parsing · resource cleanup in iterators

Standard library: `iter`, `bufio`, `net/http`, `context`
