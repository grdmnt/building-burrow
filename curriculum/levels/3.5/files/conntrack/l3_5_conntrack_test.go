package conntrack_test

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"burrow/conntrack"
)

// pipeConn returns the server side of an in-memory connection, plus the client
// side for checking whether the server side was closed.
func pipeConn(t *testing.T) (server, client net.Conn) {
	t.Helper()
	s, c := net.Pipe()
	t.Cleanup(func() { s.Close(); c.Close() })
	return s, c
}

func closed(c net.Conn) bool {
	c.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	_, err := c.Read(make([]byte, 1))
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe)
}

func TestL3_5_TrackerBasics(t *testing.T) {
	tr := conntrack.New()
	a, ac := pipeConn(t)
	b, bc := pipeConn(t)
	tr.Add(a)
	tr.Add(b)
	if tr.Len() != 2 {
		t.Fatalf("Len = %d, want 2", tr.Len())
	}
	tr.SetBusy(b, true)
	if n := tr.CloseIdle(); n != 1 {
		t.Errorf("CloseIdle should close only the idle connection: closed %d", n)
	}
	if !closed(ac) {
		t.Errorf("the idle connection should be closed")
	}
	if closed(bc) {
		t.Errorf("the busy connection must stay open")
	}
	if tr.Len() != 1 {
		t.Errorf("closed connections are removed: Len = %d, want 1", tr.Len())
	}
	tr.SetBusy(b, false)
	tr.Remove(b)
	if tr.Len() != 0 || tr.CloseIdle() != 0 {
		t.Errorf("after Remove the tracker forgets the connection")
	}
}

func TestL3_5_CloseAll(t *testing.T) {
	tr := conntrack.New()
	var clients []net.Conn
	for i := range 4 {
		s, c := pipeConn(t)
		tr.Add(s)
		if i%2 == 0 {
			tr.SetBusy(s, true)
		}
		clients = append(clients, c)
	}
	if n := tr.CloseAll(); n != 4 {
		t.Errorf("CloseAll closes busy and idle alike: closed %d of 4", n)
	}
	for i, c := range clients {
		if !closed(c) {
			t.Errorf("connection %d still open after CloseAll", i)
		}
	}
}

func TestL3_5_DrainWaitsForBusy(t *testing.T) {
	tr := conntrack.New()
	busy, bc := pipeConn(t)
	idle, ic := pipeConn(t)
	tr.Add(busy)
	tr.Add(idle)
	tr.SetBusy(busy, true)
	time.AfterFunc(100*time.Millisecond, func() {
		tr.SetBusy(busy, false)
	})
	start := time.Now()
	if err := tr.Drain(context.Background()); err != nil {
		t.Fatalf("Drain should return nil once every connection went idle and was closed, got %v", err)
	}
	if took := time.Since(start); took < 90*time.Millisecond || took > time.Second {
		t.Errorf("Drain returned after %v; it should wait for the busy connection (~100ms)", took)
	}
	if !closed(ic) || !closed(bc) {
		t.Errorf("both connections should be closed after Drain")
	}
}

func TestL3_5_DrainDeadline(t *testing.T) {
	tr := conntrack.New()
	busy, bc := pipeConn(t)
	tr.Add(busy)
	tr.SetBusy(busy, true)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := tr.Drain(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Drain with a stuck connection returns ctx.Err() at the deadline, got %v", err)
	}
	if !closed(bc) {
		t.Errorf("at the deadline, Drain force-closes everything")
	}
	if tr.Len() != 0 {
		t.Errorf("Len after a forced drain = %d", tr.Len())
	}
}

func TestL3_5_ConcurrentUse(t *testing.T) {
	tr := conntrack.New()
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, c := net.Pipe()
			defer c.Close()
			tr.Add(s)
			tr.SetBusy(s, true)
			_ = tr.Len()
			tr.SetBusy(s, false)
			tr.CloseIdle()
			tr.Remove(s)
		}()
	}
	wg.Wait()
	if tr.Len() != 0 {
		t.Errorf("Len = %d after every goroutine removed its connection", tr.Len())
	}
}
