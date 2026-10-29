package client_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"burrow/client"
	"burrow/internal/testkit"
	"burrow/wire"
)

// slowServer answers each request after delay(n), where n counts requests
// across all connections. GET replies with the key as the value.
type slowServer struct {
	addr  string
	mu    sync.Mutex
	n     int
	conns int
	delay func(n int) time.Duration
}

func newSlow(t *testing.T, delay func(n int) time.Duration) *slowServer {
	ln := testkit.Listen(t)
	s := &slowServer{addr: ln.Addr().String(), delay: delay}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.conns++
			s.mu.Unlock()
			t.Cleanup(func() { c.Close() })
			go func() {
				for {
					req, err := wire.ReadFrame(c)
					if err != nil {
						return
					}
					s.mu.Lock()
					n := s.n
					s.n++
					s.mu.Unlock()
					time.Sleep(s.delay(n))
					resp := wire.Frame{Op: wire.OpValue, Key: req.Key, Value: []byte(req.Key), Version: 1}
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
	return s
}

func (s *slowServer) stats() (requests, conns int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n, s.conns
}

func TestL3_4_Deadline(t *testing.T) {
	s := newSlow(t, func(int) time.Duration { return 5 * time.Second })
	c := connect(t, s.addr)
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

func TestL3_4_CancelMidCall(t *testing.T) {
	s := newSlow(t, func(int) time.Duration { return 5 * time.Second })
	c := connect(t, s.addr)
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

// After a timeout, the late reply is still on its way. It must not reach the next caller.
func TestL3_4_NoStaleReplies(t *testing.T) {
	s := newSlow(t, func(n int) time.Duration {
		if n == 0 {
			return 300 * time.Millisecond
		}
		return 0
	})
	c := connect(t, s.addr)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	_, err := c.Get(ctx, "first")
	cancel()
	if err == nil {
		t.Fatal("the first call should time out")
	}
	time.Sleep(400 * time.Millisecond) // the late reply to "first" arrives now
	got, err := c.Get(testkit.Ctx(t), "second")
	if err != nil {
		t.Fatalf("the call after a timeout should succeed (redial if needed): %v", err)
	}
	if string(got.Value) != "second" {
		t.Fatalf("Get(second) returned %q: that's the stale reply to the call that timed out. "+
			"After a timeout the connection is out of sync; close it and redial.", got.Value)
	}
	if _, conns := s.stats(); conns < 2 {
		t.Errorf("expected the client to redial after a timeout")
	}
}

func TestL3_4_RedialAfterServerHangsUp(t *testing.T) {
	ln := testkit.Listen(t)
	var once sync.Once
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			first := false
			once.Do(func() { first = true })
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
	c := connect(t, ln.Addr().String())
	if err := c.Ping(testkit.Ctx(t)); err == nil {
		t.Fatal("the server hung up mid-call; Ping should return an error")
	}
	if err := c.Ping(testkit.Ctx(t)); err != nil {
		t.Errorf("after the server dropped the connection, the next call should redial and work: %v", err)
	}
}

func TestL3_4_DialHonoursContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := newSlow(t, func(int) time.Duration { return 0 })
	if _, err := client.Dial(ctx, s.addr); !errors.Is(err, context.Canceled) {
		t.Errorf("Dial with an already-cancelled context should fail with context.Canceled, got %v", err)
	}
	if _, conns := s.stats(); conns != 0 {
		t.Errorf("Dial with a cancelled context must not connect")
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()
	start := time.Now()
	_, err := client.Dial(ctx2, "10.255.255.1:7070") // unroutable: connect hangs
	if err == nil {
		t.Skip("this network routed 10.255.255.1; can't test a hanging connect here")
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("Dial ignored its context deadline (took %v). Use a net.Dialer with DialContext.", time.Since(start))
	}
}

func TestL3_4_NoLeaksAcrossTimeouts(t *testing.T) {
	s := newSlow(t, func(int) time.Duration { return 30 * time.Millisecond })
	testkit.NoLeaks(t)
	c, err := client.Dial(testkit.Ctx(t), s.addr)
	if err != nil {
		t.Fatal(err)
	}
	for range 100 {
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		c.Get(ctx, "k")
		cancel()
	}
	c.Close()
}
