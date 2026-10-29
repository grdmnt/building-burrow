package httpraw_test

import (
	"bufio"
	"bytes"

	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"burrow/httpraw"
	"burrow/internal/testkit"
	"burrow/store"
)

// TestLH_3_DifferentialParsing feeds the same bytes to your parser and to
// net/http's. Where both accept a request, they must agree on what it says.
func TestLH_3_DifferentialParsing(t *testing.T) {
	inputs := []string{
		"GET / HTTP/1.1\r\nHost: a\r\n\r\n",
		"POST /submit?x=1&y=%20 HTTP/1.1\r\nHost: a\r\nContent-Length: 0\r\nX-Thing:  v  \r\n\r\n",
		"DELETE /a/b/c HTTP/1.0\r\n\r\n",
		"PUT /kv/%E2%9C%93 HTTP/1.1\r\nHost: b\r\nAccept: a\r\nAccept: b\r\n\r\n",
		"GET http://example.com/abs?q HTTP/1.1\r\nHost: example.com\r\n\r\n",
		"OPTIONS * HTTP/1.1\r\nHost: a\r\n\r\n",
		"GET /path;params HTTP/1.1\r\nHost: a\r\n\r\n",
		"PATCH /x HTTP/1.1\r\nHost: a\r\nX-Empty:\r\n\r\n",
		"GET / HTTP/1.1\nHost: lf-only\n\n",
	}
	for _, raw := range inputs {
		mine, err1 := httpraw.ReadRequest(bufio.NewReader(strings.NewReader(raw)))
		theirs, err2 := http.ReadRequest(bufio.NewReader(strings.NewReader(raw)))
		if err1 != nil || err2 != nil {
			if (err1 == nil) != (err2 == nil) {
				t.Errorf("%q: you said %v, net/http said %v", raw, err1, err2)
			}
			continue
		}
		if mine.Method != theirs.Method {
			t.Errorf("%q: Method %q vs net/http %q", raw, mine.Method, theirs.Method)
		}
		if mine.RawQuery != theirs.URL.RawQuery {
			t.Errorf("%q: RawQuery %q vs net/http %q", raw, mine.RawQuery, theirs.URL.RawQuery)
		}
		wantPath := theirs.URL.EscapedPath()
		if theirs.URL.Opaque == "*" {
			wantPath = "*"
		}
		if mine.Path != wantPath && mine.Target != "*" {
			t.Errorf("%q: Path %q vs net/http %q (keep the path escaped in Request.Path)", raw, mine.Path, wantPath)
		}
		for name, vals := range theirs.Header {
			if got := mine.Header.Values(name); strings.Join(got, ",") != strings.Join(vals, ",") {
				t.Errorf("%q: header %s = %q, net/http has %q", raw, name, got, vals)
			}
		}
	}
}

func FuzzLH_3_ReadRequest(f *testing.F) {
	for _, seed := range []string{
		"GET / HTTP/1.1\r\nHost: a\r\n\r\n",
		"POST /x HTTP/1.1\r\nContent-Length: 3\r\n\r\nabc",
		"GET / HTTP/1.1\r\nA: b\r\nA: c\r\n\r\n",
		"\r\n\r\n",
		"GET http://h/p?q HTTP/1.0\n\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		req, err := httpraw.ReadRequest(bufio.NewReader(strings.NewReader(raw)))
		if err != nil {
			return
		}
		if req.Method == "" || req.Proto == "" || (req.Path == "" && req.Target != "*") {
			t.Fatalf("accepted %q but left Method/Proto/Path empty: %+v", raw, req)
		}
		if strings.ContainsAny(req.Method+req.Target, " \r\n") {
			t.Fatalf("accepted a request line with stray whitespace: %q", raw)
		}
		for name, vals := range req.Header {
			if len(vals) > 0 && req.Header.Get(name) != vals[0] {
				t.Fatalf("Header.Get(%q) can't find its own key", name)
			}
			for _, v := range vals {
				if strings.ContainsAny(v, "\r\n") {
					t.Fatalf("header value with CR/LF accepted: %q", v)
				}
			}
		}
	})
}

func TestLH_3_ManyClientsKeepAlive(t *testing.T) {
	st := store.NewMemory()
	r := httpraw.NewRouter()
	httpraw.KVRoutes(r, st)
	base := "http://" + serve(t, &httpraw.Server{Handler: r})
	client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 50}, Timeout: 10 * time.Second}
	defer client.CloseIdleConnections()
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range 20 {
				url := fmt.Sprintf("%s/v1/kv/c%d", base, i)
				val := fmt.Sprintf("%d-%d", i, j)
				req, _ := http.NewRequest("PUT", url, strings.NewReader(val))
				resp, err := client.Do(req)
				if err != nil {
					t.Error(err)
					return
				}
				resp.Body.Close()
				resp, err = client.Get(url)
				if err != nil {
					t.Error(err)
					return
				}
				b, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if string(b) != val {
					t.Errorf("client %d read %q, want %q", i, b, val)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestLH_3_MatchesNetHTTPClientExpectations(t *testing.T) {
	addr := serve(t, &httpraw.Server{Handler: httpraw.HandlerFunc(func(w *httpraw.ResponseWriter, r *httpraw.Request) {
		switch r.Path {
		case "/redirect":
			w.Header().Set("Location", "/landed")
			w.WriteHeader(302)
		case "/landed":
			w.Write([]byte("made it"))
		case "/expect":
			b, _ := io.ReadAll(r.Body)
			w.Write(b)
		}
	})})
	resp, body := get(t, "http://"+addr+"/redirect")
	if resp.StatusCode != 200 || body != "made it" {
		t.Errorf("following a redirect through your server: %d %q", resp.StatusCode, body)
	}
	req, _ := http.NewRequest("POST", "http://"+addr+"/expect", strings.NewReader("continue please"))
	req.Header.Set("Expect", "100-continue")
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{ExpectContinueTimeout: 10 * time.Second}}
	r2, err := client.Do(req)
	if err != nil {
		t.Fatalf("Expect: 100-continue request: %v", err)
	}
	b, _ := io.ReadAll(r2.Body)
	r2.Body.Close()
	if string(b) != "continue please" {
		t.Errorf("with Expect: 100-continue, send \"HTTP/1.1 100 Continue\" before reading the body; got %q", b)
	}
}

func TestLH_3_Shutdown(t *testing.T) {
	testkit.NoLeaks(t)
	inHandler := make(chan struct{})
	srv := &httpraw.Server{Handler: httpraw.HandlerFunc(func(w *httpraw.ResponseWriter, r *httpraw.Request) {
		if r.Path == "/slow" {
			close(inHandler)
			time.Sleep(300 * time.Millisecond)
		}
		w.Write([]byte("done " + r.Path))
	})}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	addr := ln.Addr().String()

	idle := rawDial(t, addr)
	idle.send("GET /warm HTTP/1.1\r\n\r\n")
	idle.response("GET") // now idle in keep-alive

	busy := rawDial(t, addr)
	busy.send("GET /slow HTTP/1.1\r\n\r\n")
	<-inHandler

	shut := make(chan error, 1)
	go func() { shut <- srv.Shutdown(testkit.Ctx(t)) }()

	resp, body := busy.response("GET")
	if body != "done /slow" {
		t.Errorf("the in-flight request must finish during Shutdown, got %q", body)
	}
	if !resp.Close {
		t.Errorf("a response sent during Shutdown should carry Connection: close")
	}
	idle.expectClosed()
	select {
	case err := <-shut:
		if err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Shutdown didn't return")
	}
	select {
	case err := <-errc:
		if !errors.Is(err, httpraw.ErrServerClosed) {
			t.Errorf("Serve should return ErrServerClosed after Shutdown, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve didn't return after Shutdown")
	}
}

func TestLH_3_ShutdownDeadline(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	started := make(chan struct{})
	srv := &httpraw.Server{Handler: httpraw.HandlerFunc(func(w *httpraw.ResponseWriter, r *httpraw.Request) {
		close(started)
		<-block
	})}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	go srv.Serve(ln)
	c := rawDial(t, ln.Addr().String())
	c.send("GET / HTTP/1.1\r\n\r\n")
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := srv.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Shutdown with a stuck handler should give up at the context deadline, got %v", err)
	}
}

func TestLH_3_HeaderInjection(t *testing.T) {
	addr := handlerServer(t, func(w *httpraw.ResponseWriter, r *httpraw.Request) {
		w.Header().Set("X-Evil", "a\r\nSet-Cookie: session=pwned")
		w.Header().Set("Bad Name", "x")
		w.Write([]byte("ok"))
	})
	c := rawDial(t, addr)
	c.send("GET / HTTP/1.1\r\nConnection: close\r\n\r\n")
	c.c.SetReadDeadline(time.Now().Add(3 * time.Second))
	raw, _ := io.ReadAll(c.br)
	if bytes.Contains(bytes.ToLower(raw), []byte("\nset-cookie")) {
		t.Errorf("a CR/LF inside a header value let a handler inject a whole new header:\n%s", raw)
	}
	if bytes.Contains(raw, []byte("Bad Name")) {
		t.Errorf("a header name with a space isn't valid and must not be sent")
	}
}

func TestLH_3_ConflictingContentLength(t *testing.T) {
	addr := handlerServer(t, func(w *httpraw.ResponseWriter, r *httpraw.Request) {
		t.Errorf("the handler must not run for a request with two different Content-Length headers")
	})
	c := rawDial(t, addr)
	c.send("POST / HTTP/1.1\r\nContent-Length: 5\r\nContent-Length: 6\r\n\r\nhello!")
	if got := c.statusLine(); got != "HTTP/1.1 400 Bad Request" {
		t.Errorf("two different Content-Length headers is request smuggling bait: want 400, got %q", got)
	}
	c2 := rawDial(t, addr)
	c2.send("POST / HTTP/1.1\r\nContent-Length: 1, 2\r\n\r\nab")
	if got := c2.statusLine(); got != "HTTP/1.1 400 Bad Request" {
		t.Errorf("a Content-Length list with different values: want 400, got %q", got)
	}
}
