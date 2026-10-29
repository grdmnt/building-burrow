package httpraw_test

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"burrow/httpraw"
	"burrow/internal/testkit"
)

// serve starts srv on a fresh listener and returns its address.
func serve(t *testing.T, srv *httpraw.Server) string {
	t.Helper()
	ln := testkit.Listen(t)
	done := make(chan struct{})
	go func() {
		srv.Serve(ln)
		close(done)
	}()
	t.Cleanup(func() {
		ln.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Errorf("Serve didn't return within 2s of its listener closing")
		}
	})
	return ln.Addr().String()
}

// rawClient talks HTTP by hand and uses net/http's response parser as the judge.
type rawClient struct {
	t  *testing.T
	c  net.Conn
	br *bufio.Reader
}

func rawDial(t *testing.T, addr string) *rawClient {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	c.SetDeadline(time.Now().Add(10 * time.Second))
	return &rawClient{t: t, c: c, br: bufio.NewReader(c)}
}

func (r *rawClient) send(s string) {
	r.t.Helper()
	if _, err := io.WriteString(r.c, s); err != nil {
		r.t.Fatalf("write: %v", err)
	}
}

// response reads one response. method matters: HEAD responses have no body.
func (r *rawClient) response(method string) (*http.Response, string) {
	r.t.Helper()
	resp, err := http.ReadResponse(r.br, &http.Request{Method: method})
	if err != nil {
		r.t.Fatalf("net/http couldn't parse your response: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		r.t.Fatalf("reading response body: %v", err)
	}
	return resp, string(body)
}

// statusLine reads just the first line of the response.
func (r *rawClient) statusLine() string {
	r.t.Helper()
	line, err := r.br.ReadString('\n')
	if err != nil {
		r.t.Fatalf("reading status line: %v (got %q)", err, line)
	}
	return strings.TrimRight(line, "\r\n")
}

func (r *rawClient) expectClosed() {
	r.t.Helper()
	r.c.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, err := io.ReadAll(r.br)
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		r.t.Errorf("expected the server to close the connection")
	}
}

func TestL4_1_HelloOverRealHTTP(t *testing.T) {
	addr := serve(t, &httpraw.Server{})
	resp, err := http.Get("http://" + addr + "/anything")
	if err != nil {
		t.Fatalf("net/http client: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if string(body) != "Hello from Burrow\n" {
		t.Errorf("with no Handler set, reply %q, got %q", "Hello from Burrow\n", body)
	}
	if resp.ContentLength != int64(len("Hello from Burrow\n")) {
		t.Errorf("Content-Length = %d, want %d", resp.ContentLength, len("Hello from Burrow\n"))
	}
}

func TestL4_1_RawResponseShape(t *testing.T) {
	c := rawDial(t, serve(t, &httpraw.Server{}))
	c.send("GET / HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n")
	if line := c.statusLine(); line != "HTTP/1.1 200 OK" {
		t.Errorf("status line = %q, want %q", line, "HTTP/1.1 200 OK")
	}
}

func TestL4_1_ErrorStatuses(t *testing.T) {
	addr := serve(t, &httpraw.Server{})
	cases := []struct {
		raw, want string
	}{
		{"GARBAGE\r\n\r\n", "HTTP/1.1 400 Bad Request"},
		{"GET / HTTP/1.1\r\nNoColon\r\n\r\n", "HTTP/1.1 400 Bad Request"},
		{"GET / HTTP/2.0\r\n\r\n", "HTTP/1.1 505 HTTP Version Not Supported"},
		{"GET / HTTP/1.1\r\nX-Big: " + strings.Repeat("a", 9000) + "\r\n\r\n", "HTTP/1.1 431 Request Header Fields Too Large"},
	}
	for _, c := range cases {
		rc := rawDial(t, addr)
		rc.send(c.raw)
		if got := rc.statusLine(); got != c.want {
			t.Errorf("%.30q → %q, want %q", c.raw, got, c.want)
		}
		rc.expectClosed()
	}
}

func TestL4_1_ManyClients(t *testing.T) {
	addr := serve(t, &httpraw.Server{})
	var wg sync.WaitGroup
	for range 25 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Get("http://" + addr + "/")
			if err != nil {
				t.Error(err)
				return
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode != 200 {
				t.Errorf("status %d", resp.StatusCode)
			}
		}()
	}
	wg.Wait()
}

func TestL4_1_SlowClientDoesNotBlockOthers(t *testing.T) {
	addr := serve(t, &httpraw.Server{})
	slow := rawDial(t, addr)
	slow.send("GET / HTTP/1.1\r\nHo") // ...and stalls
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + addr + "/")
	if err != nil {
		t.Fatalf("a half-sent request from one client blocked another: %v", err)
	}
	resp.Body.Close()
}
