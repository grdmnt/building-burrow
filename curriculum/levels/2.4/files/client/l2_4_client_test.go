package client_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"burrow/client"
	"burrow/internal/testkit"
	"burrow/store"
	"burrow/wire"
)

func realServer(t *testing.T) string {
	t.Helper()
	ln := testkit.Listen(t)
	go (&wire.Server{Store: store.NewMemory()}).Serve(ln)
	return ln.Addr().String()
}

func connect(t *testing.T, addr string) *client.Client {
	t.Helper()
	c, err := client.Dial(testkit.Ctx(t), addr)
	if err != nil {
		t.Fatalf("Dial(%s): %v", addr, err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// fakeServer answers each request after delay(n) where n counts requests
// across all connections. GET replies with the key as the value.
type fakeServer struct {
	addr  string
	mu    sync.Mutex
	n     int
	delay func(n int) time.Duration
	conns int
}

func newFake(t *testing.T, delay func(n int) time.Duration) *fakeServer {
	ln := testkit.Listen(t)
	f := &fakeServer{addr: ln.Addr().String(), delay: delay}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			f.mu.Lock()
			f.conns++
			f.mu.Unlock()
			t.Cleanup(func() { c.Close() })
			go func() {
				for {
					req, err := wire.ReadFrame(c)
					if err != nil {
						return
					}
					f.mu.Lock()
					n := f.n
					f.n++
					f.mu.Unlock()
					time.Sleep(f.delay(n))
					resp := wire.Frame{Op: wire.OpValue, Key: req.Key, Value: []byte(req.Key)}
					if req.Op == wire.OpPing {
						resp = wire.Frame{Op: wire.OpPong}
					}
					if wire.WriteFrame(c, resp) != nil {
						return
					}
				}
			}()
		}
	}()
	return f
}

func (f *fakeServer) connections() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.conns
}

func TestL2_4_ClientRoundTrip(t *testing.T) {
	c := connect(t, realServer(t))
	ctx := testkit.Ctx(t)
	if err := c.Set(ctx, "k", []byte("v")); err != nil {
		t.Fatal(err)
	}
	v, err := c.Get(ctx, "k")
	if err != nil || string(v) != "v" {
		t.Fatalf("Get = (%q, %v)", v, err)
	}
	if err := c.Delete(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	if err := c.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestL2_4_ClientNotFound(t *testing.T) {
	c := connect(t, realServer(t))
	_, err := c.Get(testkit.Ctx(t), "missing")
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Get of a missing key should match store.ErrNotFound so callers don't care whether the store is local or remote, got %v", err)
	}
	if err := c.Delete(testkit.Ctx(t), "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Delete of a missing key should match store.ErrNotFound, got %v", err)
	}
}

func TestL2_4_ClientServerError(t *testing.T) {
	c := connect(t, realServer(t))
	err := c.Set(testkit.Ctx(t), "bad key", []byte("v"))
	var se *client.ServerError
	if !errors.As(err, &se) {
		t.Fatalf("an OpError reply should come back as a *client.ServerError, got %T %v", err, err)
	}
	if !strings.Contains(se.Msg, "invalid key") {
		t.Errorf("ServerError.Msg should carry the server's message, got %q", se.Msg)
	}
	if err := c.Ping(testkit.Ctx(t)); err != nil {
		t.Errorf("a server error doesn't break the connection; Ping got %v", err)
	}
}

func TestL2_4_ClientBinaryAndLarge(t *testing.T) {
	c := connect(t, realServer(t))
	ctx := testkit.Ctx(t)
	big := bytes.Repeat([]byte{0, 1, '\n', 0xff}, 1<<20) // 4 MiB
	if err := c.Set(ctx, "big", big); err != nil {
		t.Fatal(err)
	}
	got, err := c.Get(ctx, "big")
	if err != nil || !bytes.Equal(got, big) {
		t.Errorf("4 MiB binary value round trip failed (%d bytes, %v)", len(got), err)
	}
}

func TestL2_4_ClientConcurrentUse(t *testing.T) {
	c := connect(t, realServer(t))
	ctx := testkit.Ctx(t)
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key := fmt.Sprintf("key-%d", i)
			for j := range 20 {
				val := fmt.Sprintf("%d-%d", i, j)
				if err := c.Set(ctx, key, []byte(val)); err != nil {
					t.Error(err)
					return
				}
				got, err := c.Get(ctx, key)
				if err != nil || string(got) != val {
					t.Errorf("goroutine %d read %q (%v), want %q: replies got mixed up between callers", i, got, err, val)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestL2_4_ClientDeadline(t *testing.T) {
	f := newFake(t, func(int) time.Duration { return 5 * time.Second })
	c := connect(t, f.addr)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c.Get(ctx, "slow")
	if took := time.Since(start); took > time.Second {
		t.Fatalf("Get with a 50ms deadline took %v; set a deadline on the connection from ctx", took)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a call that runs past its context deadline should return an error matching "+
			"context.DeadlineExceeded (not just a net timeout), got %v", err)
	}
}

func TestL2_4_ClientCancelMidCall(t *testing.T) {
	f := newFake(t, func(int) time.Duration { return 5 * time.Second })
	c := connect(t, f.addr)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	start := time.Now()
	_, err := c.Get(ctx, "slow")
	if took := time.Since(start); took > time.Second {
		t.Fatalf("cancelling the context mid-call took %v to take effect. A deadline alone can't see "+
			"a cancel: look at context.AfterFunc.", took)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("want context.Canceled, got %v", err)
	}
}

func TestL2_4_ClientAlreadyCancelled(t *testing.T) {
	f := newFake(t, func(int) time.Duration { return 0 })
	c := connect(t, f.addr)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Get(ctx, "k"); !errors.Is(err, context.Canceled) {
		t.Errorf("an already-cancelled context should fail immediately with context.Canceled, got %v", err)
	}
	f.mu.Lock()
	n := f.n
	f.mu.Unlock()
	if n != 0 {
		t.Errorf("an already-cancelled call shouldn't send anything; the server saw %d requests", n)
	}
}

// After a timeout, the late reply is still on its way. The client must not
// hand it to the next caller.
func TestL2_4_ClientNoStaleReplies(t *testing.T) {
	f := newFake(t, func(n int) time.Duration {
		if n == 0 {
			return 300 * time.Millisecond
		}
		return 0
	})
	c := connect(t, f.addr)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	_, err := c.Get(ctx, "first")
	cancel()
	if err == nil {
		t.Fatal("first call should time out")
	}
	time.Sleep(400 * time.Millisecond) // the late reply to "first" arrives now
	got, err := c.Get(testkit.Ctx(t), "second")
	if err != nil {
		t.Fatalf("the call after a timeout should succeed (redial if needed): %v", err)
	}
	if string(got) != "second" {
		t.Fatalf("Get(second) returned %q: that's the stale reply to the call that timed out. "+
			"After a timeout the connection is out of sync; throw it away and redial.", got)
	}
	if f.connections() < 2 {
		t.Errorf("expected the client to redial after a timeout")
	}
}

func TestL2_4_ClientRedialsAfterServerDrop(t *testing.T) {
	ln := testkit.Listen(t)
	addr := ln.Addr().String()
	var drops sync.Once
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			first := false
			drops.Do(func() { first = true })
			if first {
				wire.ReadFrame(conn)
				conn.Close() // hang up without replying
				continue
			}
			go func() {
				for {
					req, err := wire.ReadFrame(conn)
					if err != nil {
						return
					}
					wire.WriteFrame(conn, wire.Frame{Op: wire.OpPong, Key: req.Key})
				}
			}()
		}
	}()
	c := connect(t, addr)
	if err := c.Ping(testkit.Ctx(t)); err == nil {
		t.Fatal("the server hung up mid-call; Ping should return an error")
	}
	if err := c.Ping(testkit.Ctx(t)); err != nil {
		t.Errorf("after the server dropped the connection, the next call should redial and work: %v", err)
	}
}

func TestL2_4_DialDeadAddress(t *testing.T) {
	start := time.Now()
	_, err := client.Dial(testkit.Ctx(t), testkit.DeadAddr(t))
	if err == nil {
		t.Fatal("Dial to a closed port should fail")
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("Dial to a closed port took %v", time.Since(start))
	}
}

func TestL2_4_DialRespectsContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := client.Dial(ctx, "10.255.255.1:7070") // unroutable: connect hangs
	if err == nil {
		t.Skip("this network routed 10.255.255.1; can't test a hanging connect here")
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("Dial ignored its context deadline (took %v). Use a net.Dialer with DialContext.", time.Since(start))
	}
}

func TestL2_4_ClosedClient(t *testing.T) {
	c := connect(t, realServer(t))
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Ping(testkit.Ctx(t)); !errors.Is(err, client.ErrClosed) {
		t.Errorf("calls after Close should return client.ErrClosed, got %v", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("a second Close should return nil, got %v", err)
	}
}
