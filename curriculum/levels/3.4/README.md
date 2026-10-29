# 3.4 · Deadlines

A blocking network call has to obey its context. Deadlines, cancels mid-call, late replies and a server that hangs up are all cases the client must survive without ever handing one caller another caller's reply.

## Build

```go
// No new exported API. Behaviour added to every Client method:
//   - a call past ctx's deadline returns within a second with an error matching context.DeadlineExceeded
//   - cancelling ctx mid-call returns context.Canceled promptly
//   - after any mid-call error the connection is closed and the next call redials
//   - Dial honours its ctx
// burrowctl's -timeout flag now actually bounds each command.
```

- Set the connection deadline from ctx.Deadline() for the duration of each call
- `context.AfterFunc(ctx, ...)` to interrupt a blocked read on cancel
- Discard and redial after any mid-call error; report ctx.Err() when the context caused it

Decisions the tests rely on:

- After a mid-call failure the client returns `ctx.Err()` if the context is done, otherwise the network error. Redialing is lazy: the next call does it.

## The tests check

- A fake server that sleeps 5 s: Get with a 50 ms deadline returns within 1 s with errors.Is(err, context.DeadlineExceeded)
- Cancel after 50 ms mid-call: returns within 1 s with context.Canceled
- No stale replies: the first call times out, its reply arrives 300 ms later; the next call must get its own value and the fake server has seen a second connection
- The server hangs up mid-call: that call errors, the next call redials and succeeds
- Dial with an already-cancelled ctx fails with context.Canceled without connecting; Dial to an unroutable address with a 100 ms context deadline returns within 2 s
- No goroutines leak across 100 timed-out calls (testkit.NoLeaks)
- burrowctl -timeout 50ms against a server that never replies exits 1 within 2 s and prints `timeout`

## Uses

- Frame client (built in 3.3 · Remote Store)

## In the wild

net/http's Transport does exactly this per request; database/sql drivers and gRPC clients too. context.AfterFunc (Go 1.21) replaced a goroutine-per-call idiom

## New in Go

net.Conn.SetDeadline · context.AfterFunc · net.Error.Timeout · errors.Is across layers · connection state machines

Standard library: `net`, `context`, `time`, `errors`
