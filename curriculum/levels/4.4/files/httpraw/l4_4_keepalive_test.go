package httpraw_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptrace"
	"testing"
	"time"

	"burrow/httpraw"
)

func echoPath() httpraw.HandlerFunc {
	return func(w *httpraw.ResponseWriter, r *httpraw.Request) { w.Write([]byte(r.Path)) }
}

func TestL4_4_TwoRequestsOneConnection(t *testing.T) {
	c := rawDial(t, serve(t, &httpraw.Server{Handler: echoPath()}))
	c.send("GET /one HTTP/1.1\r\nHost: x\r\n\r\n")
	resp, body := c.response("GET")
	if body != "/one" || resp.Close {
		t.Fatalf("first response: %q, close=%v. HTTP/1.1 connections stay open by default", body, resp.Close)
	}
	c.send("GET /two HTTP/1.1\r\nHost: x\r\n\r\n")
	if _, body := c.response("GET"); body != "/two" {
		t.Errorf("second request on the same connection → %q", body)
	}
}

func TestL4_4_Pipelined(t *testing.T) {
	c := rawDial(t, serve(t, &httpraw.Server{Handler: echoPath()}))
	c.send("GET /a HTTP/1.1\r\n\r\nGET /b HTTP/1.1\r\n\r\nGET /c HTTP/1.1\r\n\r\n")
	for _, want := range []string{"/a", "/b", "/c"} {
		if _, body := c.response("GET"); body != want {
			t.Errorf("pipelined responses must come back in request order: got %q, want %q", body, want)
		}
	}
}

func TestL4_4_ConnectionClose(t *testing.T) {
	c := rawDial(t, serve(t, &httpraw.Server{Handler: echoPath()}))
	c.send("GET /bye HTTP/1.1\r\nConnection: close\r\n\r\n")
	resp, _ := c.response("GET")
	if !resp.Close {
		t.Errorf("when the client asks to close, the response should say Connection: close")
	}
	c.expectClosed()
}

func TestL4_4_ConnectionHeaderIsCaseInsensitive(t *testing.T) {
	c := rawDial(t, serve(t, &httpraw.Server{Handler: echoPath()}))
	c.send("GET / HTTP/1.1\r\nconnection: Keep-Alive, CLOSE\r\n\r\n")
	c.response("GET")
	c.expectClosed()
}

func TestL4_4_HTTP10(t *testing.T) {
	addr := serve(t, &httpraw.Server{Handler: echoPath()})
	c := rawDial(t, addr)
	c.send("GET /old HTTP/1.0\r\n\r\n")
	c.response("GET")
	c.expectClosed()

	c2 := rawDial(t, addr)
	c2.send("GET /old HTTP/1.0\r\nConnection: keep-alive\r\n\r\n")
	resp, _ := c2.response("GET")
	if resp.Close {
		t.Errorf("an HTTP/1.0 client that asks for keep-alive should get it")
	}
	c2.send("GET /again HTTP/1.0\r\nConnection: keep-alive\r\n\r\n")
	if _, body := c2.response("GET"); body != "/again" {
		t.Errorf("second HTTP/1.0 keep-alive request → %q", body)
	}
}

func TestL4_4_UnreadBodyIsDrained(t *testing.T) {
	c := rawDial(t, serve(t, &httpraw.Server{Handler: echoPath()}))
	c.send("POST /ignores-body HTTP/1.1\r\nContent-Length: 11\r\n\r\nhello worldGET /next HTTP/1.1\r\n\r\n")
	c.response("POST")
	if _, body := c.response("GET"); body != "/next" {
		t.Errorf("the handler ignored its body; the server must skip those 11 bytes before reading the next request, got %q", body)
	}
}

func TestL4_4_HalfReadBodyIsDrained(t *testing.T) {
	c := rawDial(t, serve(t, &httpraw.Server{Handler: httpraw.HandlerFunc(func(w *httpraw.ResponseWriter, r *httpraw.Request) {
		buf := make([]byte, 3)
		io.ReadFull(r.Body, buf)
		w.Write(buf)
	})}))
	c.send("POST /a HTTP/1.1\r\nContent-Length: 10\r\n\r\n0123456789POST /b HTTP/1.1\r\nContent-Length: 3\r\n\r\nabc")
	if _, body := c.response("POST"); body != "012" {
		t.Errorf("first body: %q", body)
	}
	if _, body := c.response("POST"); body != "abc" {
		t.Errorf("after a half-read body, the next request must still parse: got %q", body)
	}
}

func TestL4_4_IdleTimeout(t *testing.T) {
	c := rawDial(t, serve(t, &httpraw.Server{Handler: echoPath(), IdleTimeout: 100 * time.Millisecond}))
	c.send("GET / HTTP/1.1\r\n\r\n")
	c.response("GET")
	start := time.Now()
	c.expectClosed()
	if took := time.Since(start); took > time.Second {
		t.Errorf("IdleTimeout is 100ms but the idle connection stayed open %v", took)
	}
}

func TestL4_4_ReadHeaderTimeout(t *testing.T) {
	c := rawDial(t, serve(t, &httpraw.Server{Handler: echoPath(), ReadHeaderTimeout: 150 * time.Millisecond}))
	c.send("GET / HTTP/1.1\r\nX-Slow: ") // slowloris: start headers, never finish
	start := time.Now()
	c.expectClosed()
	if took := time.Since(start); took > time.Second {
		t.Errorf("a client that never finishes its headers should be dropped after ReadHeaderTimeout (150ms), took %v", took)
	}
}

func TestL4_4_TimeoutsResetPerRequest(t *testing.T) {
	c := rawDial(t, serve(t, &httpraw.Server{Handler: echoPath(), IdleTimeout: 200 * time.Millisecond}))
	for i := range 4 {
		c.send("GET /tick HTTP/1.1\r\n\r\n")
		if _, body := c.response("GET"); body != "/tick" {
			t.Fatalf("request %d on a busy connection failed", i)
		}
		time.Sleep(120 * time.Millisecond) // under the idle timeout each time, but 480ms in total
	}
}

func TestL4_4_MaxRequestsPerConn(t *testing.T) {
	c := rawDial(t, serve(t, &httpraw.Server{Handler: echoPath(), MaxRequestsPerConn: 3}))
	for i := range 3 {
		c.send("GET / HTTP/1.1\r\n\r\n")
		resp, _ := c.response("GET")
		if i < 2 && resp.Close {
			t.Errorf("response %d closed early", i+1)
		}
		if i == 2 && !resp.Close {
			t.Errorf("the last allowed response should carry Connection: close")
		}
	}
	c.expectClosed()
}

func TestL4_4_NetHTTPClientReusesConnection(t *testing.T) {
	addr := serve(t, &httpraw.Server{Handler: echoPath()})
	client := &http.Client{Transport: &http.Transport{}}
	defer client.CloseIdleConnections()
	var reused []bool
	for range 3 {
		trace := &httptrace.ClientTrace{GotConn: func(i httptrace.GotConnInfo) { reused = append(reused, i.Reused) }}
		req, _ := http.NewRequestWithContext(httptrace.WithClientTrace(context.Background(), trace), "GET", "http://"+addr+"/", nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	if len(reused) != 3 || !reused[1] || !reused[2] {
		t.Errorf("net/http should reuse one connection for three requests; reused = %v", reused)
	}
}
