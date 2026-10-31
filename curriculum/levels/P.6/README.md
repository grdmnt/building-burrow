# P.6 · Concurrency

> One worker follows one wire at a time. A machine with many workers needs rules about who touches what, and a way to know when everyone is done.

## Your goal

Write package `primer/crawl`, a concurrent web crawler that never touches the network. A `Fetcher` is anything that can fetch a URL; `FakeFetcher` is one that serves pages from a map. `TimeoutFetcher` wraps any fetcher and gives up on slow pages, using a channel and `select`. `Crawl` starts at one URL, follows links one level at a time up to a depth, fetches every page of a level at once in its own goroutine, fetches each URL only once, counts each page's words, and reports every failure as a `shapes.FetchError`. It must stay correct under the race detector.

This is the Primer's last level and its machine: the crawler is assembled from your P.3 `WordCount`, P.4 `FetchError` and P.5 `Set`. It follows the Tour of Go section "Concurrency" (goroutines, channels, buffered channels, range and close, select, `sync.Mutex`) and ends, as the Tour does, with its Web Crawler exercise, made stricter.

## What you're using from before

**Collections kit (P.3 · Collections).** Each fetched page's body goes through `collections.WordCount`, and the result is stored in the report under the page's URL.

**Interfaces kit (P.4 · Methods and Interfaces).** Every failed fetch is reported as a `*shapes.FetchError` that wraps the fetcher's error, so callers can use `errors.As` to find the URL and `errors.Is` to find the cause:

```go
rep.Errors = append(rep.Errors, &shapes.FetchError{URL: u, Err: err})
```

`Fetcher` is an interface (P.4) and `TimeoutFetcher` wraps one the way `Rot13Reader` wraps an `io.Reader`.

**Generics kit (P.5 · Generics).** A `generic.Set[string]` remembers every URL the crawl has already taken, and `Add`'s result tells you whether a URL is new. The Set isn't safe for concurrent use, which is exactly what this level's mutex is for:

```go
seen := generic.NewSet[string]()
if seen.Add(link) { /* first time: crawl it next level */ }
```

## What's new here

**1. Goroutines.** A *goroutine* is a function running at the same time as the rest of your program. `go f(x)` starts one: `x` is evaluated right away, then `f` runs on its own while the line after the `go` carries on immediately. Goroutines are cheap (a few kilobytes each, so a program can run hundreds of thousands), and the Go runtime spreads them over all your CPU cores. Ruby's `Thread.new { ... }` is the nearest thing, but Go has no global lock, so goroutines really do run in parallel.

```go
go fmt.Println("from a goroutine")
fmt.Println("from main")
time.Sleep(10 * time.Millisecond) // a crude wait; see topic 2
```

When `main` returns, the program ends, and any goroutines still running are stopped without warning.

**2. Waiting with `sync.WaitGroup`.** Sleeping and hoping is not a plan. A `sync.WaitGroup` is a counter: `Add(1)` before starting each goroutine, `Done()` when it finishes (usually with `defer`, from P.2), and `Wait()` blocks until the counter is back to zero. Its zero value is ready to use. Call `Add` before the `go` statement, not inside the goroutine, or `Wait` might run before anything was added.

```go
var wg sync.WaitGroup
for _, name := range []string{"ann", "bob"} {
	wg.Add(1)
	go func() {
		defer wg.Done()
		fmt.Println("hello", name)
	}()
}
wg.Wait() // both have printed by here, in either order
```

`go func() { ... }()` starts a goroutine running a function literal (P.3's closures), which is the most common shape. Since Go 1.22 each loop iteration gets its own `name`, so every goroutine sees the value from its own iteration.

**3. Data races.** When two goroutines use the same variable at the same time and at least one of them writes, that's a *data race*: the result depends on timing, and the program can read half-written values or crash. Go maps are a common victim; concurrent reads of a map are fine, but a write while anyone else reads or writes it can stop the program with "concurrent map writes". The *race detector*, turned on with `go test -race` (which `burrow` always uses), watches every memory access and fails the test when it sees a race, with stack traces for both sides.

```go
count := 0
var wg sync.WaitGroup
for range 100 {
	wg.Add(1)
	go func() { defer wg.Done(); count++ }() // a race
}
wg.Wait()
fmt.Println(count) // often less than 100; -race reports it
```

**4. `sync.Mutex`.** A *mutex* (mutual exclusion lock) lets one goroutine at a time into a section of code. `mu.Lock()` waits until no one else holds it, `mu.Unlock()` lets the next one in, and `defer mu.Unlock()` right after `Lock` makes sure it's released however the function returns. Lock around every access to the shared data, reads included, and keep the locked part short: do slow work like fetching before you take the lock. Ruby's `Mutex#synchronize` does the same job. Never copy a mutex once it's in use; keep it in one place, or pass a pointer.

```go
var (
	mu    sync.Mutex
	total int
)

add := func(n int) {
	mu.Lock()
	defer mu.Unlock()
	total += n
}
```

**5. Channels.** A *channel* is a typed pipe between goroutines. `make(chan int)` makes one, `ch <- v` sends a value into it, and `v := <-ch` receives one. On this kind of channel a send waits until another goroutine receives, and a receive waits until another goroutine sends, so a channel both moves the value and makes the two goroutines meet. Ruby's `Thread::Queue` is similar. The Go saying is "don't communicate by sharing memory; share memory by communicating".

```go
ch := make(chan string)
go func() { ch <- "ping" }()
msg := <-ch // waits for the goroutine's send
fmt.Println(msg) // ping
```

**6. Buffered channels.** `make(chan int, 3)` makes a channel with room for 3 values. Sends only wait when the buffer is full, and receives only wait when it's empty. A buffer lets a sender drop a value off and move on without waiting for anyone to collect it.

```go
ch := make(chan int, 2)
ch <- 1
ch <- 2 // doesn't wait: there is room
// ch <- 3 would wait here until someone receives
fmt.Println(<-ch, <-ch) // 1 2
```

**7. Range and close.** A sender can `close(ch)` to say "no more values". `for v := range ch` receives until the channel is closed and empty, then the loop ends. `v, ok := <-ch` gives `ok == false` once a channel is closed and drained. Only the sender should close, sending on a closed channel panics, and you only need to close when the receiver has to know the stream ended, as `range` does.

```go
func countTo(n int) <-chan int {
	ch := make(chan int)
	go func() {
		defer close(ch)
		for i := 1; i <= n; i++ {
			ch <- i
		}
	}()
	return ch
}

for v := range countTo(3) {
	fmt.Print(v, " ") // 1 2 3
}
```

The return type `<-chan int` is a receive-only channel: the caller can take values out but can't send or close.

**8. `select`.** `select` is a `switch` for channel operations: it waits until one of its cases can go ahead, then runs that case. If several are ready at once it picks one at random. It is how a goroutine waits on more than one thing at a time.

```go
select {
case msg := <-messages:
	fmt.Println("got", msg)
case <-quit:
	fmt.Println("quitting")
}
```

**9. Timeouts.** `time.After(d)` returns a channel that delivers one value after the duration `d`. Put it in a `select` beside the channel you're waiting on and whichever comes first wins. A `time.Duration` is written with units, like `50 * time.Millisecond`.

```go
select {
case v := <-results:
	fmt.Println(v)
case <-time.After(time.Second):
	fmt.Println("gave up after a second")
}
```

**10. Default selection.** A `select` with a `default` case never waits: if no other case is ready right now, `default` runs. That makes a "try to receive" or "try to send" that skips instead of blocking.

```go
select {
case v := <-ch:
	fmt.Println("had one:", v)
default:
	fmt.Println("nothing waiting")
}
```

**11. Goroutine leaks.** A goroutine that waits forever never finishes, and the runtime never cleans it up: that's a *goroutine leak*, and a server that leaks one per request eventually runs out of memory. The classic leak is a send that nobody will ever receive. In the code below, if the timeout wins, `slowSquare`'s goroutine is left trying to send on an unbuffered channel that no one reads any more, forever. Topic 6 has what it needs.

```go
func slowSquare(n int, d time.Duration) (int, bool) {
	ch := make(chan int)
	go func() { ch <- heavyMath(n) }() // leaks on timeout
	select {
	case v := <-ch:
		return v, true
	case <-time.After(d):
		return 0, false
	}
}
```

The tests count goroutines before and after to catch this.

**12. Wrapping an interface.** Like `Rot13Reader` in P.4, a small struct holding a `Fetcher` can be a `Fetcher` itself and add behaviour, here a time limit, without the code that uses it knowing. This *decorator* shape is everywhere in Go: readers wrapping readers, HTTP handlers wrapping handlers.

**13. Crawling one level at a time.** Think of the links as a map of roads. The *distance* of a page is the fewest links you must follow from the start to reach it, and a crawl to depth 3 should fetch exactly the pages at distance 0, 1 and 2. A recursive crawler that dives down each link on its own can reach a page by a long route first, mark it as taken, and then stop short of pages it should have reached by the short route. The fix is to crawl in *levels*: the *frontier* is the list of URLs at the current distance; fetch all of them at once, wait for all of them, and collect the links that haven't been seen yet into the next frontier. Repeat until the depth is used up or the frontier is empty.

```text
frontier = [start]            seen = {start}
level 1:  fetch start                     -> links a, b
frontier = [a, b]             seen = {start, a, b}
level 2:  fetch a and b at once, wait     -> links c, start
frontier = [c]                start was already seen
```

## What to do

1. Create `primer/crawl/fetch.go` with `package crawl`, and declare two sentinel errors with `errors.New`: `ErrNotFound` and `ErrTimeout`.
2. Declare the `Fetcher` interface with one method, `Fetch(url string) (body string, links []string, err error)`.
3. Declare `Page` (fields `Body string` and `Links []string`) and `type FakeFetcher map[string]Page`. Give `FakeFetcher` a value-receiver `Fetch`: a URL in the map returns its page's body and links; a missing one returns `""`, nil and an error that wraps `ErrNotFound` and names the URL (`fmt.Errorf` with `%w`).
4. Declare `TimeoutFetcher` with fields `Fetcher Fetcher` and `Timeout time.Duration`, and a value-receiver `Fetch` that:
   - runs the inner `Fetch` in a new goroutine, which sends all three results back on a channel (a small struct type holding them is the tidy way)
   - uses `select` to return whichever comes first: those results, unchanged, or `""`, nil and an error wrapping `ErrTimeout` once `Timeout` has passed
   - leaks no goroutine when the timeout wins
5. Create `primer/crawl/crawl.go` with `type Report struct { Pages map[string]map[string]int; Errors []error }` and `Crawl(start string, depth int, f Fetcher) Report`:
   - depth 1 fetches only `start`, depth 2 also the pages it links to, and so on; 0 or less fetches nothing
   - crawl level by level (topic 13), fetching all of a level's URLs in parallel, one goroutine each, and waiting with a `sync.WaitGroup` before the next level
   - fetch every URL at most once, using a `generic.Set[string]` of URLs already taken; mark the start as taken before you begin
   - for a page that loads, set `Pages[url]` to `collections.WordCount(body)`; for one that fails, append `&shapes.FetchError{URL: url, Err: err}` to `Errors`, in any order, and don't follow its links
   - guard the report, the set and the next frontier with a `sync.Mutex`
6. Run the tests with `-race` (`burrow watch` does), and keep `gofmt -l .` and `go vet ./primer/...` quiet.

## Example

```go
site := crawl.FakeFetcher{
	"/":  {Body: "Hello hello world", Links: []string{"/a", "/b"}},
	"/a": {Body: "page a", Links: []string{"/", "/gone"}},
	"/b": {Body: "page b"},
}

rep := crawl.Crawl("/", 3, site)
rep.Pages["/"]   // map[hello:2 world:1]
len(rep.Pages)   // 3: "/", "/a" and "/b"
rep.Errors[0]    // fetch /gone: crawl: not found: /gone

var fe *shapes.FetchError
errors.As(rep.Errors[0], &fe)               // true; fe.URL is "/gone"
errors.Is(rep.Errors[0], crawl.ErrNotFound) // true

crawl.Crawl("/", 1, site).Pages // just "/"

slow := crawl.TimeoutFetcher{Fetcher: site, Timeout: time.Second}
slow.Fetch("/b") // "page b", nil, nil
```

## Done when

- [ ] `FakeFetcher` serves its pages and wraps `ErrNotFound`, naming the URL, for the rest
- [ ] `TimeoutFetcher` passes fast results through unchanged, gives up on slow ones with `ErrTimeout` in good time, and leaks no goroutine
- [ ] `Crawl` finds exactly the pages within the depth, counts their words with `WordCount`, and reports each failure as a `*shapes.FetchError`
- [ ] Every URL is fetched at most once, even with cycles, self-links and duplicate links
- [ ] A slow short route still beats a fast long route to the same page
- [ ] The pages of one level are fetched at the same time
- [ ] Everything passes under `-race`, and `primer/crawl` imports `burrow/primer/collections`, `burrow/primer/shapes` and `burrow/primer/generic`

## Reference

```go
// primer/crawl
package crawl

var (
	ErrNotFound = errors.New("crawl: not found")
	ErrTimeout  = errors.New("crawl: timed out")
)

type Fetcher interface {
	Fetch(url string) (body string, links []string, err error)
}

type Page struct {
	Body  string
	Links []string
}

// A missing URL returns "", nil and an error wrapping ErrNotFound
// that names the URL.
type FakeFetcher map[string]Page

func (f FakeFetcher) Fetch(url string) (string, []string, error)

// Fetch returns the inner results unchanged, or "", nil and an
// error wrapping ErrTimeout once Timeout passes. No goroutine leaks.
type TimeoutFetcher struct {
	Fetcher Fetcher
	Timeout time.Duration
}

func (t TimeoutFetcher) Fetch(url string) (string, []string, error)

type Report struct {
	Pages  map[string]map[string]int // URL -> WordCount(body)
	Errors []error                   // *shapes.FetchError, any order
}

// Crawl fetches every page whose link distance from start is less
// than depth (depth 1: just start), each URL at most once, one level
// at a time with each level's fetches running concurrently.
func Crawl(start string, depth int, f Fetcher) Report
```

## The tests check

- `FakeFetcher` returns a page's body and links; a missing URL gives `""`, nil and an error wrapping `ErrNotFound` that contains the URL
- `TimeoutFetcher` with a fast inner fetcher returns its results and errors unchanged (and a fast failure is not a timeout)
- With a 30ms timeout and a 300ms fetch it returns an `ErrTimeout` error within 200ms, with `""` and nil, and within two seconds no goroutine is still running code from package crawl (a hint about buffered channels if one is)
- `Crawl` of a six-page site to depth 4: exactly the six pages, each equal to `collections.WordCount` of its body, and one error for the missing page, a `*shapes.FetchError` with its URL wrapping `ErrNotFound`
- Depths -1, 0, 1, 2, 3 and 4 find the right pages and errors and fetch only those URLs
- 60 pages linked in a cycle, with jumps, self-links and duplicate links: every URL fetched exactly once
- A slow page on the short route to a page and fast pages on a longer route: depth 4 still reaches the page beyond it, depth 3 doesn't, nothing fetched twice
- Of four pages linked from the start, at least two are in flight at the same time (a fetcher holds each one until the others arrive, or 300ms pass)
- Under `-race` (the suite fails without it): 400 pages linked as a tree with cross links, all found with the right word counts
- A crawl through a `TimeoutFetcher` where one page is slow: it finishes quickly, the slow page is a `*shapes.FetchError` wrapping `ErrTimeout`, the pages behind it are not crawled, and no goroutine is left behind
- `primer/crawl` imports `burrow/primer/collections`, `burrow/primer/shapes` and `burrow/primer/generic`
- Every `Crawl` call must return within 10 seconds

## Try it

```bash
./burrow watch
go test -race -v -run TestLP_6_ShortestPathWins ./primer/crawl
```

To meet the race detector on purpose, comment out your `mu.Lock()` and `mu.Unlock()` lines and run `go test -race -run TestLP_6_ManyPages ./primer/crawl`. Read both stack traces it prints, then put the lock back.

## In the wild

`golang.org/x/sync/errgroup` is a WaitGroup that also collects the first error, and `semaphore` from the same module limits how many goroutines run at once; both are built from exactly these parts. Real crawlers such as `gocolly/colly` add depth limits, per-host limits and politeness delays on top of the same parts, and `context.Context`, coming in chapter 1, is the standard way to cancel the goroutines a crawl starts.

## New in Go

goroutines · go func() { ... }() · sync.WaitGroup · per-iteration loop variables · data races · the race detector · concurrent map access · sync.Mutex and defer Unlock · channels · unbuffered channels as a meeting point · buffered channels · close and range over a channel · comma ok on a receive · receive-only channel types · select · time.After and timeouts · default in select · goroutine leaks · decorating an interface · breadth-first crawling

Standard library: `sync`, `time`, `errors`, `fmt`
