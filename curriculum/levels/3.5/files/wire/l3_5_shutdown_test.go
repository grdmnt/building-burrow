package wire_test

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"burrow/internal/testkit"
	"burrow/store"
	"burrow/wire"
)

// slowStore delays every Set so a request can be caught mid-flight.
type slowStore struct {
	store.Store
	delay   time.Duration
	started chan struct{}
}

func (s *slowStore) Set(ctx context.Context, k string, v []byte) (store.Entry, error) {
	select {
	case s.started <- struct{}{}:
	default:
	}
	time.Sleep(s.delay)
	return s.Store.Set(ctx, k, v)
}

type running struct {
	srv  *wire.Server
	addr string
	errc chan error
}

func startShutdownable(t *testing.T, st store.Store) *running {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	r := &running{srv: &wire.Server{Store: st}, addr: ln.Addr().String(), errc: make(chan error, 1)}
	go func() { r.errc <- r.srv.Serve(ln) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		r.srv.Shutdown(ctx)
	})
	return r
}

func (r *running) serveResult(t *testing.T) error {
	t.Helper()
	select {
	case err := <-r.errc:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("Serve didn't return within 2s of Shutdown")
		return nil
	}
}

func TestL3_5_ShutdownIdleServer(t *testing.T) {
	testkit.NoLeaks(t)
	r := startShutdownable(t, memStore(t))
	testkit.Within(t, time.Second, "Shutdown with no connections", func() {
		if err := r.srv.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
	if err := r.serveResult(t); !errors.Is(err, wire.ErrServerClosed) {
		t.Errorf("after Shutdown, Serve should return wire.ErrServerClosed, got %v", err)
	}
}

func TestL3_5_ShutdownClosesIdleConnections(t *testing.T) {
	testkit.NoLeaks(t)
	r := startShutdownable(t, memStore(t))
	conns := make([]net.Conn, 5)
	for i := range conns {
		conns[i] = dial(t, r.addr)
		if f := roundTrip(t, conns[i], wire.Frame{Op: wire.OpPing}); f.Op != wire.OpPong {
			t.Fatal("ping failed")
		}
	}
	start := time.Now()
	if err := r.srv.Shutdown(testkit.Ctx(t)); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("Shutdown took %v with only idle connections; close idle connections right away", took)
	}
	for i, c := range conns {
		c.SetReadDeadline(time.Now().Add(time.Second))
		if _, err := c.Read(make([]byte, 1)); err != io.EOF {
			t.Errorf("idle connection %d should be closed by Shutdown (read EOF), got %v", i, err)
		}
	}
	r.serveResult(t)
}

func TestL3_5_ShutdownFinishesInFlight(t *testing.T) {
	testkit.NoLeaks(t)
	slow := &slowStore{Store: memStore(t), delay: 300 * time.Millisecond, started: make(chan struct{}, 1)}
	r := startShutdownable(t, slow)
	c := dial(t, r.addr)
	wire.WriteFrame(c, wire.Frame{Op: wire.OpSet, Key: "k", Value: []byte("v")})
	<-slow.started

	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- r.srv.Shutdown(testkit.Ctx(t)) }()

	resp, err := wire.ReadFrame(c)
	if err != nil || resp.Op != wire.OpOK {
		t.Fatalf("a request already being handled when Shutdown starts must get its reply; got (%v, %v)", resp.Op, err)
	}
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Errorf("Shutdown should return nil once in-flight work finished, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown didn't return after the in-flight request finished")
	}
	c.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := c.Read(make([]byte, 1)); err != io.EOF {
		t.Errorf("after replying, the connection should be closed; got %v", err)
	}
	if v, _ := slow.Store.Get(context.Background(), "k"); string(v.Value) != "v" {
		t.Errorf("the in-flight write must have been applied")
	}
	r.serveResult(t)
}

func TestL3_5_ShutdownRefusesNewConnections(t *testing.T) {
	r := startShutdownable(t, memStore(t))
	r.srv.Shutdown(testkit.Ctx(t))
	c, err := net.DialTimeout("tcp", r.addr, time.Second)
	if err == nil {
		c.SetReadDeadline(time.Now().Add(time.Second))
		wire.WriteFrame(c, wire.Frame{Op: wire.OpPing})
		if _, err := wire.ReadFrame(c); err == nil {
			t.Errorf("a connection made after Shutdown got served")
		}
		c.Close()
	}
}

func TestL3_5_ShutdownDeadline(t *testing.T) {
	slow := &slowStore{Store: memStore(t), delay: 3 * time.Second, started: make(chan struct{}, 1)}
	r := startShutdownable(t, slow)
	c := dial(t, r.addr)
	wire.WriteFrame(c, wire.Frame{Op: wire.OpSet, Key: "k", Value: []byte("v")})
	<-slow.started

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := r.srv.Shutdown(ctx)
	if took := time.Since(start); took > time.Second {
		t.Errorf("Shutdown with a 100ms context took %v", took)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("when the context runs out first, Shutdown returns ctx.Err(), got %v", err)
	}
	c.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := wire.ReadFrame(c); err == nil {
		t.Errorf("once the deadline passed, remaining connections should be force-closed")
	}
}

func TestL3_5_ShutdownTwiceAndConcurrently(t *testing.T) {
	r := startShutdownable(t, memStore(t))
	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := r.srv.Shutdown(testkit.Ctx(t)); err != nil {
				t.Errorf("concurrent Shutdown calls should all return nil, got %v", err)
			}
		}()
	}
	wg.Wait()
	if err := r.srv.Shutdown(testkit.Ctx(t)); err != nil {
		t.Errorf("Shutdown after Shutdown should return nil, got %v", err)
	}
}

func TestL3_5_ServeAfterShutdown(t *testing.T) {
	srv := &wire.Server{Store: memStore(t)}
	srv.Shutdown(context.Background())
	ln := testkit.Listen(t)
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		if !errors.Is(err, wire.ErrServerClosed) {
			t.Errorf("Serve on a shut-down server should return ErrServerClosed, got %v", err)
		}
	case <-time.After(time.Second):
		t.Error("Serve on a shut-down server should return immediately")
	}
}

// memStore returns a Memory that's closed when the test ends, so its
// background goroutines don't count as leaks.
func memStore(t *testing.T) *store.Memory {
	m := store.NewMemory()
	t.Cleanup(func() { m.Close() })
	return m
}
