# T.1 · Token Bucket

> A bucket with a slow tap. Every request scoops a cup; when it's empty, you wait for the drip.

## Your goal

Stop one client from hogging the API. Write `RateLimit`, a middleware that gives every client IP a *token bucket*: a counter that starts full, drops by one for each request, and refills at a steady rate. A request that finds the bucket empty gets `429 Too Many Requests` and a `Retry-After` header saying how long to wait. Buckets for clients you haven't seen in a while are swept away by a background goroutine so the map doesn't grow forever.

## What you're using from before

**Middleware chain (7.3 · Signal Chain).** `Middleware` is `func(http.Handler) http.Handler`, and `Chain` nests them. `RateLimit` returns one, so it slots in next to the others, after `AccessLog` so refused requests still get logged:

```go
h := api.Chain(mux,
	api.Recover(), api.RequestID(), api.AccessLog(l),
	api.RateLimit(10, 20), // 10 requests/s, bursts of 20
)
```

**Janitor (1.5 · Janitor).** The sweeper is the goroutine shape from 1.5, unchanged: started by the constructor, a `time.Ticker` for wake-ups, a `select` on the ticker and a `stop` channel, `Close` closing `stop` inside a `sync.Once` and then waiting on `done`. The only difference is what a tick does: instead of deleting expired entries it deletes buckets nobody has used lately.

## What's new here

**The token bucket.** Each client has a bucket holding up to `burst` tokens. A request takes one token; no token, no request. Tokens come back at `rate` per second. That gives two behaviours in one rule: a short burst of up to `burst` requests goes straight through, and a sustained stream is held to `rate` per second. You don't need a goroutine topping the buckets up. Store the token count and the time you last touched it, and refill *lazily* whenever a request arrives:

```go
elapsed := now.Sub(b.last)                 // time.Duration
b.tokens = min(b.tokens+elapsed.Seconds()*rate, float64(burst))
b.last = now
```

**Time arithmetic.** `time.Time` is an instant; `time.Duration` is a length of time (an `int64` of nanoseconds). `t2.Sub(t1)` gives a `Duration`, `d.Seconds()` gives a `float64`, and `time.Duration(x * float64(time.Second))` goes the other way. Tokens are a `float64` because half a token is a real thing: at 2 per second, 250 ms of waiting earns half a token.

**An injected clock.** `time.Now` is a function, so it can be a field: `now func() time.Time`, defaulting to `time.Now`, with an option to replace it, exactly like `store.WithClock` from 1.4. A test then uses `testkit.FakeClock`, calls `Advance(500 * time.Millisecond)` and knows precisely how many tokens came back, with no sleeping.

**The client's address.** `r.RemoteAddr` is `"ip:port"` (`"192.0.2.7:51234"`, or `"[::1]:51234"` for IPv6). The port changes on every connection, so key the bucket on the host part only. `net.SplitHostPort` does the split and handles the brackets.

**A map of state behind a mutex.** `map[string]*bucket`, one entry per client IP, guarded by a `sync.Mutex`. Pointer values matter: with `map[string]bucket` you would get a copy out, change it, and have to write it back; with `*bucket` you change the one in the map. Lock around the whole "look up, refill, take a token" sequence: two requests from the same client on two goroutines must not both see the last token.

**429 and `Retry-After`.** `http.StatusTooManyRequests` is 429. `Retry-After` is a whole number of seconds the client should wait; round up, because `Retry-After: 0` is a lie when the next token is 400 ms away. The body is yours; a JSON `ErrorResponse` keeps it consistent with every other error the API sends.

**Sweeping.** A bucket that has had time to fill completely carries no information: a new one would look the same. So a tick can delete every bucket whose `last` is older than `burst / rate` seconds. That bound is the whole sweeping rule.

## What to do

1. Define a bucket (`tokens float64`, `last time.Time`) and a limiter holding the map, a mutex, `rate`, `burst`, the clock, and the sweeper's `stop` and `done` channels.
2. Write `RateLimit(rate float64, burst int) Middleware`. Give the limiter a way to take a clock and a `Close`, in the shape of 1.4's `WithClock` and 1.5's `Close`; the exact spelling is yours until the suite is written.
3. In the handler: read the clock once, split the IP out of `r.RemoteAddr`, lock, find or create the bucket, refill, and either take a token and call the next handler, or compute the wait, unlock, and answer 429 with `Retry-After`.
4. Start the sweeper in the constructor. On each tick, under the lock, delete buckets whose `last` is older than `burst / rate` seconds by the injected clock.
5. Write `Close` with the 1.5 discipline: `sync.Once`, close `stop`, wait on `done`, idempotent.
6. Write a test of your own with `testkit.FakeClock`: fill the bucket, advance the clock, and check exactly the right number of requests pass.

## Example

Twenty-five quick requests against `RateLimit(5, 20)`:

```bash
$ for i in $(seq 25); do
    curl -s -o /dev/null -w '%{http_code} ' localhost:8080/v1/keys
  done; echo
200 200 200 200 200 200 200 200 200 200 200 200 200 200 200 200 200 200
200 200 429 429 429 429 429
$ curl -si localhost:8080/v1/keys | head -3
HTTP/1.1 429 Too Many Requests
Content-Type: application/json
Retry-After: 1
```

And the refill, on a fake clock:

```go
clock := testkit.NewFakeClock()
lim := newLimiter(2, 4, clock.Now)   // 2 tokens/s, burst 4; name yours
// 4 requests pass, the 5th is 429
clock.Advance(500 * time.Millisecond) // one token back
// 1 passes, the next is 429 with Retry-After: 1
clock.Advance(10 * time.Second)       // full again, capped at 4
// 4 pass
```

## Done when

- [ ] Twenty quick requests from one client give 200s up to the burst, then 429s, every 429 carrying `Retry-After`
- [ ] On a `FakeClock`, advancing by `1/rate` seconds lets exactly one more request through, and a long wait refills only to `burst`
- [ ] Two clients with different IPs have separate buckets; one being throttled doesn't affect the other
- [ ] A bucket unused for longer than `burst / rate` seconds is gone after the next sweep
- [ ] `Close` stops the sweeper (no goroutine left) and can be called twice

## Reference

The exact contract the tests will hold you to.

```go
// api/ratelimit.go
func RateLimit(rate float64, burst int) Middleware
```

What this level builds:

- Bucket math with an injected clock (WithClock)
- 429 + Retry-After
- Sweeper with Close

## The tests check

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/T.1/files/`, matching the earlier chapters.

Pass when: twenty quick requests show 200s then 429s with Retry-After, and a FakeClock test proves refill at the right rate.

The suite will cover:

- Burst then 429
- Refill at the configured rate on a FakeClock
- Stale buckets are swept
- Per-IP isolation

## Try it

```bash
./burrow watch
for i in $(seq 25); do
  curl -s -o /dev/null -w '%{http_code} ' localhost:8080/v1/keys
done; echo
curl -si localhost:8080/v1/keys | grep -i retry-after
```

## In the wild

golang.org/x/time/rate; Envoy's local rate limit filter

## New in Go

time arithmetic · maps of state behind a mutex · net.SplitHostPort · cleanup goroutines · http.StatusTooManyRequests and Retry-After

Standard library: `net`, `net/http`, `sync`, `time`
