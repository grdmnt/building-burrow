// Package testkit holds helpers shared by the Building Burrow test suites.
//
// It's provided by the curriculum. You don't need to edit it, and `burrow done`
// checks that it hasn't changed.
package testkit

import (
	"context"
	"net"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// RequireRace fails the test unless it runs under the race detector.
func RequireRace(t testing.TB) {
	t.Helper()
	if !RaceEnabled {
		t.Fatal("this level needs the race detector: run `burrow test` or `go test -race`")
	}
}

// Eventually polls cond until it returns true or timeout passes.
func Eventually(t testing.TB, timeout time.Duration, cond func() bool, format string, args ...any) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %v waiting for: "+format, append([]any{timeout}, args...)...)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// Within runs fn and fails the test if it doesn't return within d.
// It returns once fn has returned or the deadline passed.
func Within(t testing.TB, d time.Duration, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s did not finish within %v (is something blocked or leaking?)", what, d)
	}
}

// Ctx returns a context that is cancelled when the test ends, with a safety
// timeout so a hung call fails the test instead of hanging the run.
func Ctx(t testing.TB) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// NoLeaks records the goroutine count now and, when the test ends, waits up to
// two seconds for the count to come back down. Call it first in a test.
func NoLeaks(t testing.TB) {
	t.Helper()
	before := runtime.NumGoroutine()
	t.Cleanup(func() {
		if t.Failed() {
			return
		}
		deadline := time.Now().Add(2 * time.Second)
		for {
			now := runtime.NumGoroutine()
			if now <= before {
				return
			}
			if time.Now().After(deadline) {
				buf := make([]byte, 1<<16)
				n := runtime.Stack(buf, true)
				t.Errorf("goroutine leak: %d goroutines before the test, %d after.\n"+
					"Every goroutine you start needs a way to stop.\n\n%s", before, now, trimStacks(string(buf[:n])))
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
}

func trimStacks(s string) string {
	var keep []string
	for _, g := range strings.Split(s, "\n\n") {
		if strings.Contains(g, "testing.") && !strings.Contains(g, "burrow/") {
			continue
		}
		keep = append(keep, g)
	}
	out := strings.Join(keep, "\n\n")
	if len(out) > 4000 {
		out = out[:4000] + "\n..."
	}
	return out
}

// FakeClock is a manually advanced clock for deterministic time tests.
type FakeClock struct {
	mu  sync.Mutex
	now time.Time
}

// NewFakeClock starts a clock at a fixed instant.
func NewFakeClock() *FakeClock {
	return &FakeClock{now: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
}

// Now returns the clock's current time. Pass c.Now wherever a func() time.Time is wanted.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward by d.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// Listen opens a TCP listener on a free loopback port and closes it when the test ends.
func Listen(t testing.TB) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	return ln
}

// DeadAddr returns a loopback address that nothing is listening on.
func DeadAddr(t testing.TB) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}
