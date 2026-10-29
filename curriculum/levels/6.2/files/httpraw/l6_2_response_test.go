package httpraw_test

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"burrow/httpraw"
)

func handlerServer(t *testing.T, h httpraw.HandlerFunc) string {
	t.Helper()
	return serve(t, &httpraw.Server{Handler: h})
}

func get(t *testing.T, url string) (*http.Response, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	return resp, string(b)
}

func TestL6_2_DefaultStatusAndHeaders(t *testing.T) {
	addr := handlerServer(t, func(w *httpraw.ResponseWriter, r *httpraw.Request) {
		w.Write([]byte("hi"))
	})
	resp, body := get(t, "http://"+addr+"/")
	if resp.StatusCode != 200 || body != "hi" {
		t.Errorf("Write without WriteHeader means 200: got %d %q", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("default Content-Type for a body = %q, want %q", ct, "text/plain; charset=utf-8")
	}
	if resp.ContentLength != 2 {
		t.Errorf("Content-Length = %d, want 2", resp.ContentLength)
	}
	date, err := http.ParseTime(resp.Header.Get("Date"))
	if err != nil {
		t.Errorf("every response needs a Date header in HTTP date format (%q): %v", resp.Header.Get("Date"), err)
	} else if d := time.Since(date); d > time.Minute || d < -time.Minute {
		t.Errorf("Date header is %v off", d)
	}
	if !strings.HasSuffix(resp.Header.Get("Date"), " GMT") {
		t.Errorf("HTTP dates are always in GMT: %q", resp.Header.Get("Date"))
	}
}

func TestL6_2_CustomStatusAndHeaders(t *testing.T) {
	addr := handlerServer(t, func(w *httpraw.ResponseWriter, r *httpraw.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Add("X-Multi", "a")
		w.Header().Add("X-Multi", "b")
		w.Header().Set("X-Gone", "x")
		w.Header().Del("x-gone")
		w.WriteHeader(201)
		w.Write([]byte(`{"ok":true}`))
	})
	resp, body := get(t, "http://"+addr+"/")
	if resp.StatusCode != 201 || body != `{"ok":true}` {
		t.Errorf("got %d %q", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("a Content-Type the handler set must be kept, got %q", ct)
	}
	if v := resp.Header.Values("X-Multi"); len(v) != 2 {
		t.Errorf("Header().Add twice should send two values, got %q", v)
	}
	if resp.Header.Get("X-Gone") != "" {
		t.Errorf("a deleted header was sent")
	}
}

func TestL6_2_ReasonPhrases(t *testing.T) {
	for code, line := range map[int]string{
		200: "HTTP/1.1 200 OK", 201: "HTTP/1.1 201 Created", 400: "HTTP/1.1 400 Bad Request",
		404: "HTTP/1.1 404 Not Found", 405: "HTTP/1.1 405 Method Not Allowed",
		500: "HTTP/1.1 500 Internal Server Error", 503: "HTTP/1.1 503 Service Unavailable",
	} {
		addr := handlerServer(t, func(w *httpraw.ResponseWriter, r *httpraw.Request) { w.WriteHeader(code) })
		c := rawDial(t, addr)
		c.send("GET / HTTP/1.1\r\nConnection: close\r\n\r\n")
		if got := c.statusLine(); got != line {
			t.Errorf("WriteHeader(%d) → %q, want %q", code, got, line)
		}
	}
	addr := handlerServer(t, func(w *httpraw.ResponseWriter, r *httpraw.Request) { w.WriteHeader(299) })
	resp, _ := get(t, "http://"+addr+"/")
	if resp.StatusCode != 299 {
		t.Errorf("an unusual status like 299 must still produce a parseable status line, got %d", resp.StatusCode)
	}
}

func TestL6_2_WriteHeaderTwice(t *testing.T) {
	addr := handlerServer(t, func(w *httpraw.ResponseWriter, r *httpraw.Request) {
		w.WriteHeader(202)
		w.WriteHeader(500)
		w.Header().Set("X-Late", "too late")
		w.Write([]byte("body"))
	})
	resp, body := get(t, "http://"+addr+"/")
	if resp.StatusCode != 202 {
		t.Errorf("the first WriteHeader wins; got %d", resp.StatusCode)
	}
	if body != "body" {
		t.Errorf("body = %q", body)
	}
	if resp.Header.Get("X-Late") != "" {
		t.Errorf("headers changed after WriteHeader must not be sent")
	}
}

func TestL6_2_EchoBody(t *testing.T) {
	addr := handlerServer(t, func(w *httpraw.ResponseWriter, r *httpraw.Request) {
		if r.ContentLength != 11 {
			t.Errorf("Request.ContentLength = %d, want 11", r.ContentLength)
		}
		io.Copy(w, r.Body)
	})
	resp, err := http.Post("http://"+addr+"/echo", "text/plain", strings.NewReader("hello there"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if string(b) != "hello there" {
		t.Errorf("echo = %q", b)
	}
}

func TestL6_2_NoBodyMeansEmptyReader(t *testing.T) {
	addr := handlerServer(t, func(w *httpraw.ResponseWriter, r *httpraw.Request) {
		if r.Body == nil {
			t.Errorf("Request.Body must never be nil; use an empty reader when there's no body")
			return
		}
		b, err := io.ReadAll(r.Body)
		if err != nil || len(b) != 0 {
			t.Errorf("a GET with no body should read as empty, got (%q, %v)", b, err)
		}
	})
	get(t, "http://"+addr+"/")
}

func TestL6_2_BadContentLength(t *testing.T) {
	addr := handlerServer(t, func(w *httpraw.ResponseWriter, r *httpraw.Request) {
		t.Errorf("the handler must not run for a request with a bad Content-Length")
	})
	for _, cl := range []string{"abc", "-1", "1e3", "+5", "5 5", "99999999999999999999999"} {
		c := rawDial(t, addr)
		c.send("POST / HTTP/1.1\r\nContent-Length: " + cl + "\r\n\r\nhello")
		if got := c.statusLine(); got != "HTTP/1.1 400 Bad Request" {
			t.Errorf("Content-Length %q → %q, want 400", cl, got)
		}
		c.expectClosed()
	}
}

func TestL6_2_BodyTooLarge(t *testing.T) {
	var called atomic.Bool
	addr := serve(t, &httpraw.Server{
		MaxBodyBytes: 10,
		Handler:      httpraw.HandlerFunc(func(w *httpraw.ResponseWriter, r *httpraw.Request) { called.Store(true) }),
	})
	c := rawDial(t, addr)
	c.send("POST / HTTP/1.1\r\nContent-Length: 11\r\n\r\nhello world")
	if got := c.statusLine(); got != "HTTP/1.1 413 Request Entity Too Large" && got != "HTTP/1.1 413 Content Too Large" {
		t.Errorf("a body over MaxBodyBytes → %q, want 413", got)
	}
	if called.Load() {
		t.Errorf("reject an oversized body before calling the handler")
	}
	c2 := rawDial(t, addr)
	c2.send("POST / HTTP/1.1\r\nContent-Length: 10\r\nConnection: close\r\n\r\n0123456789")
	if got := c2.statusLine(); got != "HTTP/1.1 200 OK" {
		t.Errorf("a body exactly at the limit is fine, got %q", got)
	}
}

func TestL6_2_DefaultBodyLimitIsOneMiB(t *testing.T) {
	addr := handlerServer(t, func(w *httpraw.ResponseWriter, r *httpraw.Request) {})
	c := rawDial(t, addr)
	c.send("POST / HTTP/1.1\r\nContent-Length: 1048577\r\n\r\n")
	if got := c.statusLine(); !strings.HasPrefix(got, "HTTP/1.1 413") {
		t.Errorf("with MaxBodyBytes unset the limit is 1 MiB; 1 MiB + 1 → %q", got)
	}
}

func TestL6_2_TruncatedBody(t *testing.T) {
	got := make(chan error, 1)
	addr := handlerServer(t, func(w *httpraw.ResponseWriter, r *httpraw.Request) {
		_, err := io.ReadAll(r.Body)
		got <- err
	})
	c := rawDial(t, addr)
	c.send("POST / HTTP/1.1\r\nContent-Length: 100\r\n\r\nonly a few bytes")
	c.c.(interface{ CloseWrite() error }).CloseWrite()
	select {
	case err := <-got:
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Errorf("a body shorter than its Content-Length should read as io.ErrUnexpectedEOF, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("handler never finished reading the truncated body")
	}
}

func TestL6_2_NoBodyStatuses(t *testing.T) {
	for _, code := range []int{204, 304} {
		writeErr := make(chan error, 1)
		addr := handlerServer(t, func(w *httpraw.ResponseWriter, r *httpraw.Request) {
			w.WriteHeader(code)
			_, err := w.Write([]byte("should not be sent"))
			writeErr <- err
		})
		c := rawDial(t, addr)
		c.send("GET / HTTP/1.1\r\nConnection: close\r\n\r\n")
		c.c.SetReadDeadline(time.Now().Add(3 * time.Second))
		raw, _ := io.ReadAll(c.br)
		head, body, _ := strings.Cut(string(raw), "\r\n\r\n")
		if !strings.HasPrefix(head, "HTTP/1.1 "+strconv.Itoa(code)) {
			t.Errorf("status line for %d: %q", code, head)
		}
		if body != "" {
			t.Errorf("a %d response must have no body, got %q", code, body)
		}
		if strings.Contains(strings.ToLower(head), "content-length") {
			t.Errorf("a %d response must not carry Content-Length", code)
		}
		if err := <-writeErr; !errors.Is(err, httpraw.ErrBodyNotAllowed) {
			t.Errorf("Write after WriteHeader(%d) should return ErrBodyNotAllowed, got %v", code, err)
		}
	}
}

func TestL6_2_HEAD(t *testing.T) {
	addr := handlerServer(t, func(w *httpraw.ResponseWriter, r *httpraw.Request) {
		w.Write([]byte("twelve bytes"))
	})
	c := rawDial(t, addr)
	c.send("HEAD / HTTP/1.1\r\nConnection: close\r\n\r\n")
	c.c.SetReadDeadline(time.Now().Add(3 * time.Second))
	raw, _ := io.ReadAll(c.br)
	head, body, _ := strings.Cut(string(raw), "\r\n\r\n")
	if body != "" {
		t.Errorf("a HEAD response has no body, got %q", body)
	}
	if !strings.Contains(strings.ToLower(head), "content-length: 12") {
		t.Errorf("a HEAD response reports the Content-Length the GET would have (12). Head:\n%s", head)
	}
}

func TestL6_2_HandlerPanic(t *testing.T) {
	addr := handlerServer(t, func(w *httpraw.ResponseWriter, r *httpraw.Request) {
		if r.Path == "/boom" {
			panic("handler exploded")
		}
		w.Write([]byte("fine"))
	})
	resp, _ := get(t, "http://"+addr+"/boom")
	if resp.StatusCode != 500 {
		t.Errorf("a panicking handler should produce a 500, got %d", resp.StatusCode)
	}
	if _, body := get(t, "http://"+addr+"/ok"); body != "fine" {
		t.Errorf("the server must keep running after a handler panics")
	}
}

func TestL6_2_BodySizes(t *testing.T) {
	addr := handlerServer(t, func(w *httpraw.ResponseWriter, r *httpraw.Request) { io.Copy(w, r.Body) })
	for _, n := range []int{5, 0, 512 << 10} {
		body := bytes.Repeat([]byte("b"), n)
		resp, err := http.Post("http://"+addr+"/", "application/octet-stream", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("%d-byte body: %v", n, err)
		}
		got, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if !bytes.Equal(got, body) {
			t.Errorf("a %d-byte body came back as %d bytes", n, len(got))
		}
	}
}

func TestL6_2_UnknownStatusLine(t *testing.T) {
	addr := handlerServer(t, func(w *httpraw.ResponseWriter, r *httpraw.Request) { w.WriteHeader(299) })
	c := rawDial(t, addr)
	c.send("GET / HTTP/1.1\r\nConnection: close\r\n\r\n")
	if got := c.statusLine(); got != "HTTP/1.1 299 " {
		t.Errorf("an unknown status has an empty reason phrase: got %q, want %q", got, "HTTP/1.1 299 ")
	}
}
