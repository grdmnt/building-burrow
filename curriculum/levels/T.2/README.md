# T.2 · Read-Through Cache

> A hundred people ask the librarian the same question at once. She looks it up once and tells them all.

## Your goal

Put a cache in front of the store so repeated reads of hot keys never touch it. Two parts. An `LRU` (*least recently used*: when full, throw out whatever was used longest ago), generic over key and value types and bounded by bytes rather than by count. And `Wrap`, which takes any `store.Store` and returns one that answers `Get` from the cache, fills it on a miss, and forgets a key the moment anything writes to it. The miss path uses *single-flight*: when a hundred requests miss on the same cold key at the same moment, one of them reads the store and the other ninety-nine wait for that answer instead of each reading too.

## What you're using from before

**Memory store (1.1 · Register File).** `store.Store` is the interface and `store.Entry` is the value. `Wrap` is a *decorator*: a `Store` that holds another `Store` and forwards everything to it, intercepting `Get` to look in the cache first and every write to invalidate. The tests put a `Memory` (or a slow fake) underneath:

```go
entrySize := func(e store.Entry) int {
	return len(e.Key) + len(e.Value)
}
lru := cache.NewLRU[string, store.Entry](64<<20, entrySize)
st := cache.Wrap(store.NewMemory(), lru) // st is a store.Store
```

**Rate limiter (T.1 · Token Bucket).** The load test at the end runs through both: the cached store goes into `api.Server`, and the server's handler goes behind `RateLimit`. An abusive client is throttled by the limiter; everyone else is served mostly from the cache.

```go
srv := &api.Server{Store: st} // st from cache.Wrap above
h := api.Chain(srv.Handler(), api.RateLimit(50, 100))
ts := httptest.NewServer(h)
```

## What's new here

**Generics.** A type or function can take *type parameters* in square brackets. `LRU[K comparable, V any]` is one type that works for any key type usable as a map key (`comparable` is the built-in constraint for that) and any value type at all. Inside the type's methods `K` and `V` are ordinary types. You instantiate by naming the types, and Go infers them from arguments when it can:

```go
type Pair[K comparable, V any] struct {
	Key K
	Val V
}

func First[T any](xs []T) T { return xs[0] }

p := Pair[string, int]{"a", 1}
n := First([]int{4, 5})       // T inferred as int
```

The "two instantiations compile" check is the point: `LRU[string, store.Entry]` and `LRU[int, []byte]` from the same code, with no `interface{}` and no casts.

**`container/list`.** A doubly linked list. `list.New()`, `l.PushFront(v)` returns a `*list.Element`, `l.MoveToFront(e)`, `l.Back()`, `l.Remove(e)`. `e.Value` is `any`, so you type-assert it back: `e.Value.(entry)`. An LRU is this list (front is most recent, back is least) plus a `map[K]*list.Element` for O(1) lookup. Every hit moves the element to the front; every insert pushes to the front; eviction removes from the back until the byte total fits.

**Bounded by bytes.** `NewLRU` takes `size func(V) int` so the cache can hold "64 MiB of entries" rather than "10,000 entries" of unknown size. Keep a running total; add on insert, subtract on remove or evict. A single value larger than `maxBytes` is never cached.

**Cache invalidation.** The cache is only correct if it never serves a value the store has since changed. The rule is simple and must be total: every method that can change a key (`Set`, `SetWithTTL`, `Delete`, `CompareAndSwap`, `CompareAndSwapWithTTL`, `CompareAndDelete`, `Touch`) removes that key from the cache, whether or not the write succeeded. Remove, don't update: trying to be clever and store the new entry is where version and expiry bugs come from. Test this case first; forgetting one method means stale data, and nothing crashes to tell you. One race to watch: a miss that read the store just before a write can finish just after it and put the old value back into the cache. Keep a counter that every invalidation bumps, note it before the read, and only `Put` the result if it hasn't moved.

**Single-flight.** On a miss, before reading the store, look in `inflight map[string]*call`. If there is no entry, you are the leader: create one, unlock, read the store, record the result, and wake the others. If there is one, you are a follower: wait on it and share its result. A `sync.WaitGroup` used this way is a *latch*: `Add(1)` once by the leader, `Done()` once when the result is in, and any number of `Wait()`s that all release together:

```go
type call struct {
	wg  sync.WaitGroup
	val store.Entry
	err error
}
// leader:   c := &call{}; c.wg.Add(1); m[key] = c; unlock
//           load; c.wg.Done()
// follower: c := m[key]; unlock; c.wg.Wait(); use c.val, c.err
```

Delete the `inflight` entry when the call finishes, so the next miss on that key (after an invalidation, say) starts a fresh read. An error is shared with the followers of *that* call but never cached: the next miss tries again.

**Counters with `expvar`.** As on the admin port in 7.6: `expvar.NewInt("cache_hits")` and `expvar.NewInt("cache_misses")` show up in `/debug/vars`, so the hit rate is something you read rather than guess.

## What to do

1. Create a `cache` package with `LRU[K comparable, V any]` and `NewLRU`. Fields: the list, the map from `K` to `*list.Element`, the byte total, `maxBytes`, `size`, and a mutex.
2. Write `Get(k) (V, bool)` (moves to front on a hit), `Put(k, v)` (insert or replace, then evict from the back while over budget) and `Remove(k)`. Names are yours; keep them small.
3. Write `Wrap(st, lru) store.Store`: a struct holding both, with every `store.Store` method.
   - `Get`: cache hit returns a copy. Miss goes through single-flight, puts the result in the cache on success, and returns it.
   - Every mutating method: call the inner store, then `Remove` the key from the cache, whatever the result.
   - `Keys` passes straight through.
4. Add the `inflight` map and `call` struct for single-flight, guarded by the same mutex or its own.
5. Register hit and miss counters with `expvar`.
6. Write the tests in this order: invalidation, eviction order, single-flight with a store that sleeps 50 ms on `Get`, then a load test through `RateLimit` and the cache.

## Example

```go
byLen := func(s string) int { return len(s) }
lru := cache.NewLRU[string, string](10, byLen)
lru.Put("a", "aaaa")   // 4 bytes
lru.Put("b", "bbbb")   // 8
lru.Get("a")           // a is now most recent
lru.Put("c", "cccc")   // 12 > 10: evict the back, which is b
_, ok := lru.Get("b")  // ok == false
_, ok = lru.Get("a")   // ok == true
```

Single-flight against a slow store, the shape of the test:

```go
slow := &countingStore{ // a test double counting Gets
	Store: store.NewMemory(), delay: 50 * time.Millisecond}
slow.Set(ctx, "hot", []byte("v"))
lru := cache.NewLRU[string, store.Entry](1<<20, entrySize)
st := cache.Wrap(slow, lru)

var wg sync.WaitGroup
for range 100 {
	wg.Add(1)
	go func() { defer wg.Done(); st.Get(ctx, "hot") }()
}
wg.Wait()
slow.gets.Load()   // 1: one read for a hundred concurrent misses
st.Get(ctx, "hot") // a hit now; slow.gets stays 1
st.Set(ctx, "hot", []byte("v2"))
st.Get(ctx, "hot") // a miss again; slow.gets is 2, value is v2
```

## Done when

- [ ] With a small byte budget, the keys that fall out are the least recently used ones, and a `Get` counts as use
- [ ] After any write to a key through the wrapper, the next `Get` reads the new value from the store, never the old one from the cache
- [ ] 100 concurrent `Get`s of one cold key against a 50 ms store cause exactly one store read, and all 100 get the value
- [ ] A store error on a miss reaches every waiter of that read and is not cached: the next `Get` reads the store again
- [ ] `LRU[string, store.Entry]` and a second instantiation with different types both compile and work
- [ ] Through `RateLimit` and the cache, an abusive client sees 429s while the others get a 95% hit rate

## Reference

The exact contract the tests will hold you to.

```go
type LRU[K comparable, V any] struct{...}
func NewLRU[K comparable, V any](maxBytes int,
	size func(V) int) *LRU[K, V]
func Wrap(st store.Store, lru *LRU[string, store.Entry]) store.Store
```

What this level builds:

- Generic LRU
- Invalidate on every mutating method
- Single-flight map[string]*call
- Load test through RateLimit + cache

## The tests check

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/T.2/files/`, matching the earlier chapters.

Pass when: tests prove eviction order; with a 50 ms store, 100 concurrent GETs for one cold key cause exactly 1 store read and all 100 get the value; the load test through the rate limiter shows 429s for the abuser and a 95% hit rate for the rest.

The suite will cover:

- Eviction order
- Invalidate before serving stale
- Single flight: exactly one read, shared error not cached
- Two instantiations compile

## Try it

```bash
./burrow watch
go test ./cache -run SingleFlight -race -v
curl -s localhost:7071/debug/vars | jq '{cache_hits, cache_misses}'
```

## In the wild

golang.org/x/sync/singleflight, hashicorp/golang-lru, groupcache

## New in Go

generics: type parameters and constraints · container/list · WaitGroup as a latch · cache invalidation

Standard library: `container/list`, `sync`, `expvar`
