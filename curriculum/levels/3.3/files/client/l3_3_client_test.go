package client_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"burrow/client"
	"burrow/internal/testkit"
	"burrow/store"
	"burrow/store/storetest"
	"burrow/wire"
)

var _ store.Store = (*client.Client)(nil) // the remote store is a Store

func realServer(t *testing.T) string {
	t.Helper()
	st := store.NewMemory()
	t.Cleanup(func() { st.Close() })
	ln := testkit.Listen(t)
	go (&wire.Server{Store: st}).Serve(ln)
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

func TestL3_3_Conformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T) storetest.Store { return connect(t, realServer(t)) })
}

func TestL3_3_CASConformance(t *testing.T) {
	storetest.RunCAS(t, func(t *testing.T) storetest.CASStore { return connect(t, realServer(t)) })
}

func TestL3_3_TTLConformance(t *testing.T) {
	storetest.RunTTL(t, func(t *testing.T) storetest.TTLStore { return connect(t, realServer(t)) })
}

func TestL3_3_EntriesCarryVersionAndExpiry(t *testing.T) {
	c := connect(t, realServer(t))
	ctx := testkit.Ctx(t)
	e, err := c.Set(ctx, "k", []byte("v"))
	if err != nil || e.Version != 1 || e.Key != "k" || string(e.Value) != "v" {
		t.Fatalf("Set → (%+v, %v)", e, err)
	}
	e, _ = c.Set(ctx, "k", []byte("v2"))
	if e.Version != 2 {
		t.Errorf("second Set should report version 2, got %d", e.Version)
	}
	g, err := c.Get(ctx, "k")
	if err != nil || g.Version != 2 || !g.ExpiresAt.IsZero() {
		t.Errorf("Get → (%+v, %v): version 2 and no expiry", g, err)
	}
	before := time.Now()
	c.SetWithTTL(ctx, "t", []byte("v"), time.Minute)
	g, _ = c.Get(ctx, "t")
	if g.ExpiresAt.Before(before.Add(59*time.Second)) || g.ExpiresAt.After(time.Now().Add(61*time.Second)) {
		t.Errorf("ExpiresAt on the client should be about now + the TTL remaining, got %v", g.ExpiresAt)
	}
	tch, err := c.Touch(ctx, "k", time.Hour)
	if err != nil || string(tch.Value) != "v2" || tch.Version != 2 || tch.ExpiresAt.IsZero() {
		t.Errorf("Touch returns the full entry (value v2, version 2, an expiry): (%+v, %v)", tch, err)
	}
}

func TestL3_3_ErrorsMatchTheLocalStore(t *testing.T) {
	c := connect(t, realServer(t))
	ctx := testkit.Ctx(t)
	if _, err := c.Get(ctx, "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Get of a missing key should match store.ErrNotFound, got %v", err)
	}
	if err := c.Delete(ctx, "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Delete of a missing key should match store.ErrNotFound, got %v", err)
	}
	c.Set(ctx, "k", []byte("v"))
	if _, err := c.CompareAndSwap(ctx, "k", 9, []byte("x")); !errors.Is(err, store.ErrVersionMismatch) {
		t.Errorf("a stale CAS should match store.ErrVersionMismatch, got %v", err)
	}
	if err := c.CompareAndDelete(ctx, "k", 9); !errors.Is(err, store.ErrVersionMismatch) {
		t.Errorf("a stale CompareAndDelete should match store.ErrVersionMismatch, got %v", err)
	}
}

func TestL3_3_ServerErrorType(t *testing.T) {
	ln := testkit.Listen(t)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			req, err := wire.ReadFrame(conn)
			if err != nil {
				return
			}
			wire.WriteFrame(conn, wire.Frame{Op: wire.OpError, Key: req.Key, Value: []byte("disk full")})
		}
	}()
	c := connect(t, ln.Addr().String())
	_, err := c.Set(testkit.Ctx(t), "k", []byte("v"))
	var se *client.ServerError
	if !errors.As(err, &se) {
		t.Fatalf("an OpError reply should come back as a *client.ServerError, got %T %v", err, err)
	}
	if se.Msg != "disk full" {
		t.Errorf("ServerError.Msg should carry the server's text, got %q", se.Msg)
	}
}

// countingServer answers every request with OpOK and counts them.
func countingServer(t *testing.T) (string, *atomic.Int64) {
	var n atomic.Int64
	ln := testkit.Listen(t)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { conn.Close() })
			go func() {
				for {
					req, err := wire.ReadFrame(conn)
					if err != nil {
						return
					}
					n.Add(1)
					wire.WriteFrame(conn, wire.Frame{Op: wire.OpOK, Key: req.Key})
				}
			}()
		}
	}()
	return ln.Addr().String(), &n
}

func TestL3_3_ValidationIsLocal(t *testing.T) {
	addr, requests := countingServer(t)
	c := connect(t, addr)
	ctx := testkit.Ctx(t)
	if _, err := c.Set(ctx, "bad key", []byte("v")); !errors.Is(err, store.ErrInvalidKey) {
		t.Errorf("an invalid key fails locally with store.ErrInvalidKey, got %v", err)
	}
	if _, err := c.Get(ctx, ""); !errors.Is(err, store.ErrInvalidKey) {
		t.Errorf("Get(\"\") fails locally with store.ErrInvalidKey, got %v", err)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := c.Get(cctx, "k"); !errors.Is(err, context.Canceled) {
		t.Errorf("an already-cancelled context fails locally with context.Canceled, got %v", err)
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("nothing should be sent for an invalid key or a cancelled context; the server saw %d requests", n)
	}
	if _, err := c.Keys(ctx, "a b"); err != nil {
		t.Errorf("Keys takes a prefix, not a key: \"a b\" should be sent, got %v", err)
	}
	if requests.Load() != 1 {
		t.Errorf("Keys should have made exactly one request")
	}
}

func TestL3_3_TouchIsOneRequest(t *testing.T) {
	addr, requests := countingServer(t)
	c := connect(t, addr)
	c.Touch(testkit.Ctx(t), "k", time.Minute)
	if n := requests.Load(); n != 1 {
		t.Errorf("Touch should be one round trip (the OpOK reply carries the value), made %d", n)
	}
}

func TestL3_3_BinaryAndLarge(t *testing.T) {
	c := connect(t, realServer(t))
	ctx := testkit.Ctx(t)
	big := bytes.Repeat([]byte{0, 1, '\n', 0xff}, 1<<18) // 1 MiB
	if _, err := c.Set(ctx, "big", big); err != nil {
		t.Fatal(err)
	}
	got, err := c.Get(ctx, "big")
	if err != nil || !bytes.Equal(got.Value, big) {
		t.Errorf("1 MiB binary value round trip failed (%d bytes, %v)", len(got.Value), err)
	}
}

func TestL3_3_SharedClient(t *testing.T) {
	c := connect(t, realServer(t))
	ctx := testkit.Ctx(t)
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key := fmt.Sprintf("key-%d", i)
			for j := range 100 {
				val := fmt.Sprintf("%d-%d", i, j)
				if _, err := c.Set(ctx, key, []byte(val)); err != nil {
					t.Error(err)
					return
				}
				got, err := c.Get(ctx, key)
				if err != nil || string(got.Value) != val {
					t.Errorf("goroutine %d read %q (%v), want %q: replies got mixed up between callers", i, got.Value, err, val)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestL3_3_DialAndClose(t *testing.T) {
	start := time.Now()
	if _, err := client.Dial(testkit.Ctx(t), testkit.DeadAddr(t)); err == nil {
		t.Fatal("Dial to a closed port should fail")
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("Dial to a closed port took %v", time.Since(start))
	}
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
