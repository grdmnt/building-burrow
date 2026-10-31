package crawl_test

import (
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"burrow/internal/testkit"
	"burrow/primer/collections"
	"burrow/primer/crawl"
	"burrow/primer/shapes"
)

// lp6Site is a small made-up site. "/missing" is linked but has no page.
//
//	/ ── /docs ── /docs/wire ── /missing
//	│      └───── /docs/wal
//	└─── /blog ── /blog/2026
var lp6Site = crawl.FakeFetcher{
	"/": {
		Body:  "Burrow is a key value store",
		Links: []string{"/docs", "/blog"},
	},
	"/docs": {
		Body:  "Docs for the store",
		Links: []string{"/", "/docs/wire", "/docs/wal"},
	},
	"/docs/wire": {
		Body:  "The wire protocol speaks to the store",
		Links: []string{"/docs", "/missing"},
	},
	"/docs/wal": {
		Body:  "The write ahead log keeps the store safe",
		Links: []string{"/docs"},
	},
	"/blog": {
		Body:  "Blog posts about the store and the wire",
		Links: []string{"/", "/blog/2026"},
	},
	"/blog/2026": {Body: "Twenty twenty six was a good year"},
}

// lp6Counting wraps pages, counts calls per URL and can delay some.
type lp6Counting struct {
	pages crawl.FakeFetcher
	delay map[string]time.Duration

	mu    sync.Mutex
	calls map[string]int
}

func (c *lp6Counting) Fetch(url string) (string, []string, error) {
	c.mu.Lock()
	if c.calls == nil {
		c.calls = map[string]int{}
	}
	c.calls[url]++
	c.mu.Unlock()
	time.Sleep(c.delay[url])
	return c.pages.Fetch(url)
}

func (c *lp6Counting) snapshot() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return maps.Clone(c.calls)
}

func lp6Crawl(t *testing.T, start string, depth int,
	f crawl.Fetcher) crawl.Report {
	t.Helper()
	var rep crawl.Report
	testkit.Within(t, 10*time.Second, "Crawl", func() {
		rep = crawl.Crawl(start, depth, f)
	})
	return rep
}

func lp6Keys(m map[string]map[string]int) []string {
	return slices.Sorted(maps.Keys(m))
}

func TestLP_6_RaceDetectorOn(t *testing.T) {
	testkit.RequireRace(t)
}

func TestLP_6_FakeFetcher(t *testing.T) {
	body, links, err := lp6Site.Fetch("/docs")
	if err != nil {
		t.Fatalf("Fetch(\"/docs\") returned error %v", err)
	}
	if body != "Docs for the store" ||
		!slices.Equal(links, []string{"/", "/docs/wire", "/docs/wal"}) {
		t.Errorf("Fetch(\"/docs\") = %q, %q; want the page's Body and "+
			"Links", body, links)
	}
	body, links, err = lp6Site.Fetch("/missing")
	if !errors.Is(err, crawl.ErrNotFound) {
		t.Fatalf("Fetch of a URL with no page should return an error "+
			"wrapping ErrNotFound, got %v", err)
	}
	if !strings.Contains(err.Error(), "/missing") {
		t.Errorf("the not-found error should name the URL, got %q", err)
	}
	if body != "" || links != nil {
		t.Errorf("a failed Fetch should return \"\" and nil links, got "+
			"%q, %q", body, links)
	}
}

func TestLP_6_TimeoutFetcherPassesThrough(t *testing.T) {
	tf := crawl.TimeoutFetcher{Fetcher: lp6Site, Timeout: time.Second}
	var _ crawl.Fetcher = tf
	body, links, err := tf.Fetch("/blog")
	if err != nil || body != lp6Site["/blog"].Body ||
		!slices.Equal(links, lp6Site["/blog"].Links) {
		t.Errorf("a fast fetch through TimeoutFetcher = %q, %q, %v; want "+
			"the inner fetcher's results unchanged", body, links, err)
	}
	_, _, err = tf.Fetch("/nope")
	if !errors.Is(err, crawl.ErrNotFound) {
		t.Errorf("an error from the inner fetcher should come back "+
			"as-is, got %v", err)
	}
	if errors.Is(err, crawl.ErrTimeout) {
		t.Error("a fast failure is not a timeout")
	}
}

// lp6NoLeak fails the test if, 2s after it ends, more goroutines are
// running than when it started.
func lp6NoLeak(t *testing.T, hint string) {
	t.Helper()
	before := runtime.NumGoroutine()
	t.Cleanup(func() {
		if t.Failed() {
			return
		}
		deadline := time.Now().Add(2 * time.Second)
		for runtime.NumGoroutine() > before {
			if time.Now().After(deadline) {
				t.Errorf("goroutine leak: %d goroutines before, %d "+
					"after. %s", before, runtime.NumGoroutine(), hint)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
}

const lp6LeakHint = "When the timeout wins the select, nobody will " +
	"ever receive from the result channel. If it's unbuffered, the " +
	"goroutine running the slow Fetch blocks on its send forever. Give " +
	"the channel room for one value: make(chan result, 1)."

func TestLP_6_TimeoutFetcherGivesUp(t *testing.T) {
	lp6NoLeak(t, lp6LeakHint)
	slow := &lp6Counting{
		pages: lp6Site,
		delay: map[string]time.Duration{"/": 300 * time.Millisecond},
	}
	tf := crawl.TimeoutFetcher{Fetcher: slow, Timeout: 30 * time.Millisecond}
	start := time.Now()
	body, links, err := tf.Fetch("/")
	took := time.Since(start)
	if !errors.Is(err, crawl.ErrTimeout) {
		t.Fatalf("a fetch slower than Timeout should fail with an error "+
			"wrapping ErrTimeout, got %v (after %v)", err, took)
	}
	if took > 200*time.Millisecond {
		t.Errorf("TimeoutFetcher took %v with a 30ms timeout; select on "+
			"the result and time.After(Timeout), whichever comes first",
			took)
	}
	if body != "" || links != nil {
		t.Errorf("a timed-out Fetch should return \"\" and nil links, "+
			"got %q, %q", body, links)
	}
}

func TestLP_6_Crawl(t *testing.T) {
	rep := lp6Crawl(t, "/", 4, lp6Site)
	want := []string{"/", "/blog", "/blog/2026", "/docs", "/docs/wal",
		"/docs/wire"}
	if got := lp6Keys(rep.Pages); !slices.Equal(got, want) {
		t.Fatalf("Crawl(\"/\", 4) found pages %q, want %q", got, want)
	}
	for url, words := range rep.Pages {
		want := collections.WordCount(lp6Site[url].Body)
		if !reflect.DeepEqual(words, want) {
			t.Errorf("Pages[%q] = %v, want collections.WordCount of its "+
				"body: %v", url, words, want)
		}
	}
	if len(rep.Errors) != 1 {
		t.Fatalf("Crawl should report one error (for /missing), got %d: "+
			"%v", len(rep.Errors), rep.Errors)
	}
	err := rep.Errors[0]
	var fe *shapes.FetchError
	if !errors.As(err, &fe) {
		t.Fatalf("a failed URL should be reported as a "+
			"*shapes.FetchError, got %T: %v", err, err)
	}
	if fe.URL != "/missing" {
		t.Errorf("the FetchError is for %q, want \"/missing\"", fe.URL)
	}
	if !errors.Is(err, crawl.ErrNotFound) {
		t.Errorf("the FetchError should wrap the fetcher's error "+
			"(ErrNotFound), got %v", err)
	}
}

func TestLP_6_Depth(t *testing.T) {
	tests := []struct {
		depth  int
		pages  []string
		errors int
	}{
		{-1, nil, 0},
		{0, nil, 0},
		{1, []string{"/"}, 0},
		{2, []string{"/", "/blog", "/docs"}, 0},
		{3, []string{"/", "/blog", "/blog/2026", "/docs", "/docs/wal",
			"/docs/wire"}, 0},
		{4, []string{"/", "/blog", "/blog/2026", "/docs", "/docs/wal",
			"/docs/wire"}, 1},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("depth %d", tt.depth), func(t *testing.T) {
			c := &lp6Counting{pages: lp6Site}
			rep := lp6Crawl(t, "/", tt.depth, c)
			if got := lp6Keys(rep.Pages); !slices.Equal(got, tt.pages) {
				t.Errorf("Crawl(\"/\", %d) found %q, want %q. Depth 1 "+
					"is just the start page; each extra level follows "+
					"one more link", tt.depth, got, tt.pages)
			}
			if len(rep.Errors) != tt.errors {
				t.Errorf("Crawl(\"/\", %d) reported %d errors, want %d: "+
					"%v", tt.depth, len(rep.Errors), tt.errors, rep.Errors)
			}
			fetched := len(c.snapshot())
			if want := len(tt.pages) + tt.errors; fetched != want {
				t.Errorf("Crawl(\"/\", %d) fetched %d URLs, want %d: %v",
					tt.depth, fetched, want, c.snapshot())
			}
		})
	}
}

func TestLP_6_FetchesEachURLOnce(t *testing.T) {
	const n = 60
	site := crawl.FakeFetcher{}
	for i := range n {
		u := func(j int) string { return "/p" + strconv.Itoa(j%n) }
		site[u(i)] = crawl.Page{
			Body: "page " + strconv.Itoa(i),
			// a cycle, a jump, a self-link and a duplicate
			Links: []string{u(i + 1), u(i * 7), u(i), u(i + 1), "/p0"},
		}
	}
	c := &lp6Counting{pages: site}
	rep := lp6Crawl(t, "/p0", n+5, c)
	if len(rep.Pages) != n || len(rep.Errors) != 0 {
		t.Errorf("crawling %d linked pages found %d pages and %d errors",
			n, len(rep.Pages), len(rep.Errors))
	}
	for url, calls := range c.snapshot() {
		if calls != 1 {
			t.Errorf("%s was fetched %d times; every URL should be "+
				"fetched once. Record each URL in a generic.Set before "+
				"fetching it, and check Add's result", url, calls)
		}
	}
}

// Two ways to reach /t: start → /a → /c → /t (3 links) and
// start → /b → /t (2 links). /b is slow, so a crawler that runs ahead
// down /a reaches /t by the long way first.
func TestLP_6_ShortestPathWins(t *testing.T) {
	site := crawl.FakeFetcher{
		"/":  {Body: "start", Links: []string{"/a", "/b"}},
		"/a": {Body: "a", Links: []string{"/c"}},
		"/b": {Body: "b", Links: []string{"/t"}},
		"/c": {Body: "c", Links: []string{"/t"}},
		"/t": {Body: "t", Links: []string{"/u"}},
		"/u": {Body: "u"},
	}
	slowB := map[string]time.Duration{"/b": 150 * time.Millisecond}

	c := &lp6Counting{pages: site, delay: slowB}
	rep := lp6Crawl(t, "/", 4, c)
	if _, ok := rep.Pages["/u"]; !ok {
		t.Errorf("/u is 3 links from the start (/ → /b → /t → /u), so "+
			"depth 4 should fetch it, but the crawl found only %q. A "+
			"longer path (/ → /a → /c → /t) reached /t first and used up "+
			"its depth. Crawl one level at a time: fetch every page at "+
			"distance d before any page at distance d+1",
			lp6Keys(rep.Pages))
	}
	for url, calls := range c.snapshot() {
		if calls != 1 {
			t.Errorf("%s was fetched %d times, want once", url, calls)
		}
	}

	c = &lp6Counting{pages: site, delay: slowB}
	rep = lp6Crawl(t, "/", 3, c)
	if _, ok := rep.Pages["/u"]; ok {
		t.Error("with depth 3, /u (3 links away) should not be fetched")
	}
}

// lp6Barrier holds each fetch (except the start page's) until need
// fetches are running at once, or wait has passed.
type lp6Barrier struct {
	pages crawl.FakeFetcher
	start string
	need  int
	wait  time.Duration

	mu       sync.Mutex
	inFlight int
	most     int
}

func (b *lp6Barrier) Fetch(url string) (string, []string, error) {
	if url == b.start {
		return b.pages.Fetch(url)
	}
	b.mu.Lock()
	b.inFlight++
	b.most = max(b.most, b.inFlight)
	b.mu.Unlock()
	deadline := time.Now().Add(b.wait)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		done := b.most >= b.need
		b.mu.Unlock()
		if done {
			break
		}
		time.Sleep(time.Millisecond)
	}
	b.mu.Lock()
	b.inFlight--
	b.mu.Unlock()
	return b.pages.Fetch(url)
}

func TestLP_6_FetchesConcurrently(t *testing.T) {
	site := crawl.FakeFetcher{
		"/":  {Body: "hub", Links: []string{"/1", "/2", "/3", "/4"}},
		"/1": {Body: "one"}, "/2": {Body: "two"},
		"/3": {Body: "three"}, "/4": {Body: "four"},
	}
	b := &lp6Barrier{pages: site, start: "/", need: 4,
		wait: 300 * time.Millisecond}
	rep := lp6Crawl(t, "/", 2, b)
	if len(rep.Pages) != 5 {
		t.Fatalf("expected 5 pages, got %q", lp6Keys(rep.Pages))
	}
	b.mu.Lock()
	most := b.most
	b.mu.Unlock()
	if most < 2 {
		t.Errorf("the 4 pages linked from the start were fetched one at "+
			"a time (at most %d in flight). Start a goroutine per URL "+
			"with `go`, and wait for them with a sync.WaitGroup", most)
	}
}

func TestLP_6_ManyPagesUnderRace(t *testing.T) {
	testkit.RequireRace(t)
	const n = 400
	site := crawl.FakeFetcher{}
	u := func(i int) string { return "/n" + strconv.Itoa(i) }
	for i := range n {
		var links []string
		for _, j := range []int{2*i + 1, 2*i + 2, (i * 13) % n, i / 2} {
			if j < n {
				links = append(links, u(j))
			}
		}
		site[u(i)] = crawl.Page{
			Body:  strings.Repeat("word ", i%5) + "Node " + strconv.Itoa(i),
			Links: links,
		}
	}
	rep := lp6Crawl(t, u(0), 50, site)
	if len(rep.Pages) != n || len(rep.Errors) != 0 {
		t.Fatalf("crawling %d pages found %d and %d errors", n,
			len(rep.Pages), len(rep.Errors))
	}
	for i := range n {
		got := rep.Pages[u(i)]
		want := collections.WordCount(site[u(i)].Body)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Pages[%q] = %v, want %v", u(i), got, want)
		}
	}
}

func TestLP_6_CrawlWithTimeouts(t *testing.T) {
	lp6NoLeak(t, lp6LeakHint)
	slow := &lp6Counting{
		pages: lp6Site,
		delay: map[string]time.Duration{"/docs": 400 * time.Millisecond},
	}
	tf := crawl.TimeoutFetcher{Fetcher: slow, Timeout: 40 * time.Millisecond}
	start := time.Now()
	rep := lp6Crawl(t, "/", 3, tf)
	if took := time.Since(start); took > 350*time.Millisecond {
		t.Errorf("the crawl took %v; a 40ms timeout on the slow page "+
			"should keep it short", took)
	}
	want := []string{"/", "/blog", "/blog/2026"}
	if got := lp6Keys(rep.Pages); !slices.Equal(got, want) {
		t.Errorf("with /docs timing out, Crawl found %q, want %q", got,
			want)
	}
	if len(rep.Errors) != 1 {
		t.Fatalf("want one error (for /docs), got %v", rep.Errors)
	}
	var fe *shapes.FetchError
	if !errors.As(rep.Errors[0], &fe) || fe.URL != "/docs" ||
		!errors.Is(rep.Errors[0], crawl.ErrTimeout) {
		t.Errorf("the error should be a *shapes.FetchError for /docs "+
			"wrapping ErrTimeout, got %v", rep.Errors[0])
	}
}

func lp6Imports(t *testing.T) map[string]bool {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		af, err := parser.ParseFile(fset, f, src, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parsing %s: %v", f, err)
		}
		for _, im := range af.Imports {
			p, _ := strconv.Unquote(im.Path.Value)
			got[p] = true
		}
	}
	return got
}

func TestLP_6_UsesEarlierParts(t *testing.T) {
	imports := lp6Imports(t)
	for _, p := range []struct{ path, why string }{
		{"burrow/primer/collections", "collections.WordCount (P.3) " +
			"counts each page's words"},
		{"burrow/primer/shapes", "shapes.FetchError (P.4) wraps each " +
			"failure"},
		{"burrow/primer/generic", "a generic.Set[string] (P.5) " +
			"remembers which URLs are taken"},
	} {
		if !imports[p.path] {
			t.Errorf("the crawler should reuse your earlier parts: %s. "+
				"Import %q", p.why, p.path)
		}
	}
}
