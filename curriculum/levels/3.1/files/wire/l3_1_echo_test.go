package wire_test

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"burrow/internal/testkit"
	"burrow/wire"
)

// startEcho runs ServeEcho on a fresh listener and returns its address.
// When the test ends it closes the listener and checks ServeEcho returned nil.
func startEcho(t *testing.T, logger *slog.Logger) string {
	t.Helper()
	ln := testkit.Listen(t)
	errc := make(chan error, 1)
	go func() { errc <- wire.ServeEcho(ln, logger) }()
	t.Cleanup(func() {
		ln.Close()
		select {
		case err := <-errc:
			if err != nil {
				t.Errorf("ServeEcho should return nil once its listener is closed, got %v. "+
					"Check errors.Is(err, net.ErrClosed) in your accept loop.", err)
			}
		case <-time.After(2 * time.Second):
			t.Errorf("ServeEcho didn't return within 2s of its listener closing")
		}
	})
	return ln.Addr().String()
}

func dial(t *testing.T, addr string) *net.TCPConn {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	t.Cleanup(func() { c.Close() })
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	return c.(*net.TCPConn)
}

func TestL3_1_Echo(t *testing.T) {
	addr := startEcho(t, nil)
	c := dial(t, addr)
	if _, err := c.Write([]byte("hello burrow\n")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len("hello burrow\n"))
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatalf("reading the echo: %v", err)
	}
	if string(buf) != "hello burrow\n" {
		t.Errorf("echo = %q", buf)
	}
}

func TestL3_1_IdleClientDoesNotBlockOthers(t *testing.T) {
	addr := startEcho(t, nil)
	idle := dial(t, addr) // connects, never sends anything
	_ = idle
	c := dial(t, addr)
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	c.Write([]byte("ping"))
	buf := make([]byte, 4)
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatalf("a second client got no echo while the first sat idle (%v). "+
			"Handle each connection in its own goroutine.", err)
	}
}

func TestL3_1_HalfClose(t *testing.T) {
	addr := startEcho(t, nil)
	c := dial(t, addr)
	c.Write([]byte("last words"))
	if err := c.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	all, err := io.ReadAll(c)
	if err != nil {
		t.Fatalf("after the client half-closes, the server should echo what it got and then close: %v", err)
	}
	if string(all) != "last words" {
		t.Errorf("got %q after half-close, want %q", all, "last words")
	}
}

func TestL3_1_LargePayload(t *testing.T) {
	addr := startEcho(t, nil)
	c := dial(t, addr)
	payload := make([]byte, 4<<20)
	rand.Read(payload)
	go func() {
		c.Write(payload)
		c.CloseWrite()
	}()
	got, err := io.ReadAll(c)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("4 MiB echo came back as %d bytes (or corrupted)", len(got))
	}
}

func TestL3_1_ManyClients(t *testing.T) {
	addr := startEcho(t, nil)
	var wg sync.WaitGroup
	errs := make(chan error, 50)
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := net.DialTimeout("tcp", addr, 2*time.Second)
			if err != nil {
				errs <- err
				return
			}
			defer c.Close()
			c.SetDeadline(time.Now().Add(10 * time.Second))
			msg := make([]byte, 64<<10)
			rand.Read(msg)
			go c.Write(msg)
			got := make([]byte, len(msg))
			if _, err := io.ReadFull(c, got); err != nil {
				errs <- err
				return
			}
			if !bytes.Equal(got, msg) {
				errs <- errors.New("echo mismatch")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("one of 50 concurrent clients failed: %v", err)
	}
}

func TestL3_1_StructuredLogs(t *testing.T) {
	var buf safeBuffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	addr := startEcho(t, logger)
	c := dial(t, addr)
	c.Write([]byte("hello"))
	io.ReadFull(c, make([]byte, 5))
	c.Close()

	testkit.Eventually(t, 2*time.Second, func() bool {
		return strings.Contains(buf.String(), "client disconnected")
	}, "a %q log line", "client disconnected")
	out := buf.String()
	for _, want := range []string{"client connected", "remote=127.0.0.1:", "client disconnected", "bytes=5"} {
		if !strings.Contains(out, want) {
			t.Errorf("logs should contain %q. Use slog attributes, not formatted strings. Logs:\n%s", want, out)
		}
	}
}

func TestL3_1_NilLoggerIsFine(t *testing.T) {
	addr := startEcho(t, nil)
	c := dial(t, addr)
	c.Write([]byte("x"))
	if _, err := io.ReadFull(c, make([]byte, 1)); err != nil {
		t.Fatalf("ServeEcho(ln, nil) must work with logging turned off: %v", err)
	}
}

type safeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestL3_1_ServeConnsCallsHandler(t *testing.T) {
	testkit.NoLeaks(t)
	ln := testkit.Listen(t)
	got := make(chan string, 3)
	errc := make(chan error, 1)
	go func() {
		errc <- wire.ServeConns(ln, func(c net.Conn) {
			defer c.Close()
			buf := make([]byte, 5)
			io.ReadFull(c, buf)
			got <- string(buf)
		}, nil)
	}()
	for _, msg := range []string{"one..", "two..", "three"} {
		c := dial(t, ln.Addr().String())
		c.Write([]byte(msg))
	}
	seen := map[string]bool{}
	for range 3 {
		select {
		case s := <-got:
			seen[s] = true
		case <-time.After(2 * time.Second):
			t.Fatal("ServeConns didn't call the handler for every connection")
		}
	}
	if len(seen) != 3 {
		t.Errorf("each connection gets its own handler call: saw %v", seen)
	}
	ln.Close()
	select {
	case err := <-errc:
		if err != nil {
			t.Errorf("ServeConns returns nil when its listener closes, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ServeConns didn't return after the listener closed")
	}
}

// brokenListener fails Accept with an error that isn't net.ErrClosed.
type brokenListener struct{ net.Listener }

var errBroken = errors.New("too many open files")

func (b brokenListener) Accept() (net.Conn, error) { return nil, errBroken }

func TestL3_1_OtherAcceptErrorsAreReturned(t *testing.T) {
	ln := testkit.Listen(t)
	done := make(chan error, 1)
	go func() { done <- wire.ServeConns(brokenListener{ln}, func(c net.Conn) { c.Close() }, nil) }()
	select {
	case err := <-done:
		if !errors.Is(err, errBroken) {
			t.Errorf("an Accept error other than a closed listener should be returned as-is, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ServeConns should return when Accept fails with a real error")
	}
}
