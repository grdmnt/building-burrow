package kvclient_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"burrow/grpc/kvclient"
	"burrow/grpc/kvserver"
	"burrow/internal/testkit"
	"burrow/store"
	"burrow/store/storetest"
	"burrow/watch"
)

var _ store.Store = (*kvclient.Client)(nil)

// countingBroker is a real broker that counts open subscriptions and
// records the after ID of every SubscribeFrom.
type countingBroker struct {
	*watch.Broker
	open   atomic.Int64
	mu     sync.Mutex
	afters []uint64
}

func (c *countingBroker) wrap(stop func()) func() {
	c.open.Add(1)
	var once sync.Once
	return func() {
		once.Do(func() { c.open.Add(-1) })
		stop()
	}
}

func (c *countingBroker) Subscribe(prefix string) (<-chan watch.Event, uint64, func()) {
	ch, last, stop := c.Broker.Subscribe(prefix)
	return ch, last, c.wrap(stop)
}

func (c *countingBroker) SubscribeFrom(prefix string, after uint64) (<-chan watch.Event, func(), error) {
	c.mu.Lock()
	c.afters = append(c.afters, after)
	c.mu.Unlock()
	ch, stop, err := c.Broker.SubscribeFrom(prefix, after)
	if err != nil {
		return ch, stop, err
	}
	return ch, c.wrap(stop), nil
}

// node is a KV server that can be stopped and started again on a new
// in-memory listener, with the same store and broker.
type node struct {
	t   testing.TB
	srv *kvserver.Server
	b   *countingBroker
	st  *watch.Store

	mu  sync.Mutex
	lis *bufconn.Listener
	gs  *grpc.Server
}

// newNode serves a memory store wrapped with watch.Wrap. history is the
// broker's ring size.
func newNode(t testing.TB, history int) *node {
	t.Helper()
	b := &countingBroker{Broker: watch.NewBroker(1024, history)}
	var ws *watch.Store
	m := store.NewMemory(store.WithOnEvict(func(e store.Entry) { ws.OnEvict(e) }))
	ws = watch.Wrap(m, b.Broker)
	n := &node{t: t, srv: &kvserver.Server{Store: ws, Broker: b}, b: b, st: ws}
	t.Cleanup(func() {
		n.stop()
		m.Close()
	})
	n.start()
	return n
}

// storeNode serves st with no broker.
func storeNode(t testing.TB, st store.Store) *node {
	t.Helper()
	n := &node{t: t, srv: &kvserver.Server{Store: st}}
	t.Cleanup(n.stop)
	n.start()
	return n
}

func (n *node) start() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.lis = bufconn.Listen(1 << 20)
	n.gs = n.srv.GRPCServer()
	go n.gs.Serve(n.lis)
}

func (n *node) stop() {
	n.mu.Lock()
	gs := n.gs
	n.gs, n.lis = nil, nil
	n.mu.Unlock()
	if gs != nil {
		gs.Stop()
	}
}

func (n *node) dialer(ctx context.Context, _ string) (net.Conn, error) {
	n.mu.Lock()
	lis := n.lis
	n.mu.Unlock()
	if lis == nil {
		return nil, errors.New("server down")
	}
	return lis.DialContext(ctx)
}

var quickBackoff = func(int) time.Duration { return 10 * time.Millisecond }

// dial returns a kvclient.Client for n that reconnects quickly.
func (n *node) dial(t testing.TB) *kvclient.Client {
	t.Helper()
	c, err := kvclient.Dial("passthrough:///bufnet", kvclient.Options{Backoff: quickBackoff},
		grpc.WithContextDialer(n.dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithConnectParams(grpc.ConnectParams{
			Backoff:           backoff.Config{BaseDelay: 10 * time.Millisecond, Multiplier: 1.5, MaxDelay: 50 * time.Millisecond},
			MinConnectTimeout: time.Second,
		}),
	)
	if err != nil {
		t.Fatalf("kvclient.Dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func ctxT(t testing.TB) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// The conformance suites from chapter 1, over gRPC.
func TestLG_6_Storetest(t *testing.T) {
	testkit.RequireRace(t)
	client := func(t *testing.T) *kvclient.Client { return newNode(t, 64).dial(t) }
	t.Run("Basic", func(t *testing.T) {
		storetest.Run(t, func(t *testing.T) storetest.Store { return client(t) })
	})
	t.Run("Concurrent", func(t *testing.T) {
		storetest.RunConcurrent(t, func(t *testing.T) storetest.Store { return client(t) })
	})
	t.Run("CAS", func(t *testing.T) {
		storetest.RunCAS(t, func(t *testing.T) storetest.CASStore { return client(t) })
	})
	t.Run("TTL", func(t *testing.T) {
		storetest.RunTTL(t, func(t *testing.T) storetest.TTLStore { return client(t) })
	})
}

// slowStore blocks every Get until its context ends.
type slowStore struct{ store.Store }

func (slowStore) Get(ctx context.Context, _ string) (store.Entry, error) {
	select {
	case <-ctx.Done():
		return store.Entry{}, ctx.Err()
	case <-time.After(3 * time.Second):
		return store.Entry{}, errors.New("slowStore: the context never ended")
	}
}

func TestLG_6_Errors(t *testing.T) {
	c := newNode(t, 64).dial(t)
	ctx := ctxT(t)
	c.Set(ctx, "k", []byte("1"))
	c.Set(ctx, "k", []byte("2"))
	_, getMissing := c.Get(ctx, "missing")
	_, stale := c.CompareAndSwap(ctx, "k", 1, []byte("x"))
	_, casMissing := c.CompareAndSwap(ctx, "missing", 7, []byte("x"))
	_, badKey := c.Set(ctx, "a b", nil)
	_, badTTL := c.SetWithTTL(ctx, "k", nil, 0)
	cases := []struct {
		name string
		err  error
		is   error
		code codes.Code // OK: don't check
	}{
		{"Get missing", getMissing, store.ErrNotFound, codes.NotFound},
		{"CAS stale", stale, store.ErrVersionMismatch, codes.Aborted},
		{"CAS on a missing key", casMissing, store.ErrNotFound, codes.Aborted},
		{"CompareAndDelete stale", c.CompareAndDelete(ctx, "k", 1), store.ErrVersionMismatch, codes.Aborted},
		{"CompareAndDelete missing", c.CompareAndDelete(ctx, "missing", 1), store.ErrNotFound, codes.Aborted},
		{"Delete missing", c.Delete(ctx, "missing"), store.ErrNotFound, codes.NotFound},
		{"invalid key", badKey, store.ErrInvalidKey, codes.InvalidArgument},
		{"invalid ttl", badTTL, store.ErrInvalidTTL, codes.OK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !errors.Is(tc.err, tc.is) {
				t.Errorf("errors.Is(%v, %v) is false", tc.err, tc.is)
			}
			if tc.code != codes.OK && status.Code(tc.err) != tc.code {
				t.Errorf("status.Code(%v) = %v, want %v: wrap the status error, don't replace it", tc.err, status.Code(tc.err), tc.code)
			}
		})
	}

	slow := storeNode(t, slowStore{store.NewMemory()}).dial(t)
	dctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := slow.Get(dctx, "k")
	if !errors.Is(err, context.DeadlineExceeded) || status.Code(err) != codes.DeadlineExceeded {
		t.Errorf("Get past its deadline: %v; want errors.Is context.DeadlineExceeded and code DeadlineExceeded", err)
	}
}

// flakyStore fails the first fails calls of Get and Set with
// store.ErrClosed, which the server sends as UNAVAILABLE.
type flakyStore struct {
	store.Store
	fails      int64
	gets, sets atomic.Int64
}

func (f *flakyStore) Get(ctx context.Context, k string) (store.Entry, error) {
	if f.gets.Add(1) <= f.fails {
		return store.Entry{}, store.ErrClosed
	}
	return f.Store.Get(ctx, k)
}

func (f *flakyStore) Set(ctx context.Context, k string, v []byte) (store.Entry, error) {
	if f.sets.Add(1) <= f.fails {
		return store.Entry{}, store.ErrClosed
	}
	return f.Store.Set(ctx, k, v)
}

func TestLG_6_RetryPolicy(t *testing.T) {
	if !json.Valid([]byte(kvclient.ServiceConfig)) {
		t.Fatalf("kvclient.ServiceConfig isn't valid JSON:\n%s", kvclient.ServiceConfig)
	}
	m := store.NewMemory()
	defer m.Close()
	m.Set(context.Background(), "k", []byte("v"))
	f := &flakyStore{Store: m, fails: 2}
	c := storeNode(t, f).dial(t)
	ctx := ctxT(t)
	e, err := c.Get(ctx, "k")
	if err != nil || string(e.Value) != "v" {
		t.Fatalf("Get against a server that answers UNAVAILABLE twice: (%q, %v). "+
			"The service config's retryPolicy should retry Get.", e.Value, err)
	}
	if n := f.gets.Load(); n != 3 {
		t.Errorf("the store saw %d Gets, want 3 (two failures, one retry that worked)", n)
	}
	_, err = c.Set(ctx, "k", []byte("w"))
	if status.Code(err) != codes.Unavailable {
		t.Errorf("Set against a failing server: %v, want Unavailable. Set must not be retried: "+
			"a retried write that had succeeded would bump the version twice.", err)
	}
	if n := f.sets.Load(); n != 1 {
		t.Errorf("the store saw %d Sets, want exactly 1", n)
	}
}

func TestLG_6_WaitForReady(t *testing.T) {
	n := storeNode(t, store.NewMemory())
	n.stop()
	c := n.dial(t)
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := c.Get(ctx, "k")
		done <- err
	}()
	// Longer than the retry policy's whole budget, so only
	// waitForReady can make this call succeed.
	time.Sleep(time.Second)
	n.start()
	select {
	case err := <-done:
		if !errors.Is(err, store.ErrNotFound) {
			t.Errorf("Get issued while the server was down: %v; want it to wait for the server "+
				"(waitForReady in the service config) and then get ErrNotFound", err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("Get never returned")
	}
}

// collect ranges over a Watch in a goroutine.
func collect(ctx context.Context, c *kvclient.Client, prefix string, after uint64) (<-chan watch.Event, <-chan error) {
	evs := make(chan watch.Event, 1024)
	errs := make(chan error, 2)
	go func() {
		defer close(evs)
		for ev, err := range c.Watch(ctx, prefix, after) {
			if err != nil {
				errs <- err
				continue
			}
			evs <- ev
		}
	}()
	return evs, errs
}

func next(t testing.TB, evs <-chan watch.Event, errs <-chan error) watch.Event {
	t.Helper()
	select {
	case ev, ok := <-evs:
		if !ok {
			t.Fatal("Watch ended early")
		}
		return ev
	case err := <-errs:
		t.Fatalf("Watch yielded %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("no event within 5s")
	}
	return watch.Event{}
}

func expectIDs(t testing.TB, evs <-chan watch.Event, errs <-chan error, from, to uint64) {
	t.Helper()
	for id := from; id <= to; id++ {
		if ev := next(t, evs, errs); ev.ID != id {
			t.Fatalf("got event %d (%s %s), want %d", ev.ID, ev.Type, ev.Entry.Key, id)
		}
	}
}

func (n *node) set(t testing.TB, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if _, err := n.st.Set(context.Background(), k, []byte("v-"+k)); err != nil {
			t.Fatal(err)
		}
	}
}

func (n *node) waitOpen(t testing.TB, want int64) {
	t.Helper()
	testkit.Eventually(t, 3*time.Second, func() bool { return n.b.open.Load() == want },
		"%d open subscriptions on the server, have %d", want, n.b.open.Load())
}

func TestLG_6_Watch(t *testing.T) {
	n := newNode(t, 64)
	c := n.dial(t)
	n.set(t, "k1", "k2", "cfg") // 1, 2, 3
	ctx, cancel := context.WithCancel(ctxT(t))
	defer cancel()
	evs, errs := collect(ctx, c, "k", 0)
	n.waitOpen(t, 1)
	ctx2 := ctxT(t)
	c.Set(ctx2, "k3", []byte("x")) // 4
	c.Delete(ctx2, "k1")           // 5
	ev := next(t, evs, errs)
	if ev.ID != 4 || ev.Type != "put" || ev.Entry.Key != "k3" || string(ev.Entry.Value) != "x" || ev.Entry.Version != 1 {
		t.Errorf("first event %+v, want 4 put k3 = x at version 1 (READY is never yielded)", ev)
	}
	if ev := next(t, evs, errs); ev.ID != 5 || ev.Type != "delete" || ev.Entry.Key != "k1" {
		t.Errorf("second event %+v, want 5 delete k1", ev)
	}

	// Resume from an ID: 3 is cfg, filtered; then 4 and 5 again.
	evs2, errs2 := collect(ctx, c, "k", 2)
	expectIDs(t, evs2, errs2, 4, 5)
	n.waitOpen(t, 2)

	// Breaking out of the loop ends the stream on the server.
	got := 0
	for ev, err := range c.Watch(ctx, "", 1) {
		if err != nil {
			t.Fatal(err)
		}
		if ev.ID != uint64(2+got) {
			t.Fatalf("Watch after 1 yielded %d, want %d", ev.ID, 2+got)
		}
		if got++; got == 2 {
			break
		}
	}
	n.waitOpen(t, 2)
	cancel()
	n.waitOpen(t, 0)
}

func TestLG_6_WatchErrors(t *testing.T) {
	n := newNode(t, 8)
	c := n.dial(t)
	for i := range 20 {
		n.set(t, fmt.Sprintf("k%02d", i))
	}
	var errs []error
	for _, err := range c.Watch(ctxT(t), "", 3) {
		errs = append(errs, err)
	}
	if len(errs) != 1 || !errors.Is(errs[0], watch.ErrCannotResume) || status.Code(errs[0]) != codes.OutOfRange {
		t.Errorf("Watch after 3 with history 8 at 20 yielded %v; want exactly one error matching "+
			"watch.ErrCannotResume (code OutOfRange), then the loop ends", errs)
	}

	ctx, cancel := context.WithCancel(context.Background())
	evs, errc := collect(ctx, c, "", 0)
	n.waitOpen(t, 1)
	cancel()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("after cancel Watch yields %v, want an error matching context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelling the context didn't end the Watch")
	}
	select {
	case _, ok := <-evs:
		if ok {
			t.Error("Watch yielded an event after its context was cancelled")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Watch kept running after yielding its context error")
	}
	n.waitOpen(t, 0)
}

// The server goes away and comes back. Events written while it was
// down arrive after the reconnect, with no gaps and no duplicates.
func TestLG_6_WatchResumes(t *testing.T) {
	n := newNode(t, 1024)
	c := n.dial(t)
	n.set(t, "a", "b", "c") // 1..3
	ctx, cancel := context.WithCancel(ctxT(t))
	defer cancel()
	evs, errs := collect(ctx, c, "", 0)
	n.waitOpen(t, 1)
	n.set(t, "d", "e") // 4, 5
	expectIDs(t, evs, errs, 4, 5)

	n.stop()
	n.set(t, "f", "g", "h", "i", "j") // 6..10 while down
	n.waitOpen(t, 0)
	n.start()
	expectIDs(t, evs, errs, 6, 10)
	n.set(t, "k", "l") // 11, 12
	expectIDs(t, evs, errs, 11, 12)

	n.b.mu.Lock()
	afters := n.b.afters
	n.b.mu.Unlock()
	if len(afters) == 0 || afters[0] != 5 {
		t.Errorf("the reconnect resumed with after = %v, want 5 (the last ID seen)", afters)
	}
}

// A READY with nothing after it still counts as seen: a client that
// saw READY 3 and nothing else resumes after 3, not with a fresh
// stream that would miss what was written while it was away.
func TestLG_6_WatchReadyCountsAsSeen(t *testing.T) {
	n := newNode(t, 1024)
	c := n.dial(t)
	n.set(t, "a", "b", "c")
	ctx, cancel := context.WithCancel(ctxT(t))
	defer cancel()
	evs, errs := collect(ctx, c, "", 0)
	n.waitOpen(t, 1)
	n.stop()
	n.set(t, "d", "e") // 4, 5 while down
	n.start()
	expectIDs(t, evs, errs, 4, 5)
}

func TestLG_6_NoLeaks(t *testing.T) {
	run := func(t *testing.T) {
		n := newNode(t, 64)
		c := n.dial(t)
		n.set(t, "a", "b")
		if _, err := c.Get(ctxT(t), "a"); err != nil {
			t.Fatal(err)
		}
		for _, err := range c.Watch(ctxT(t), "", 1) {
			if err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	t.Run("warmup", run)
	testkit.NoLeaks(t)
	t.Run("run", run)
}
