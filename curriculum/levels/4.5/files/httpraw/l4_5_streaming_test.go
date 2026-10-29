package httpraw_test

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"burrow/httpraw"
	"burrow/store"
)

func TestL4_5_FlushStreamsChunks(t *testing.T) {
	release := make(chan struct{})
	addr := serve(t, &httpraw.Server{Handler: httpraw.HandlerFunc(func(w *httpraw.ResponseWriter, r *httpraw.Request) {
		w.Write([]byte("first part\n"))
		w.Flush()
		<-release
		w.Write([]byte("second part\n"))
	})})
	defer close(release)

	c := rawDial(t, addr)
	c.send("GET / HTTP/1.1\r\n\r\n")
	resp, err := http.ReadResponse(c.br, &http.Request{Method: "GET"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.TransferEncoding) == 0 || resp.TransferEncoding[0] != "chunked" {
		t.Fatalf("after Flush with no Content-Length known, the response must use Transfer-Encoding: chunked, got %v", resp.TransferEncoding)
	}
	br := bufio.NewReader(resp.Body)
	got := make(chan string, 1)
	go func() {
		line, _ := br.ReadString('\n')
		got <- line
	}()
	select {
	case line := <-got:
		if line != "first part\n" {
			t.Errorf("first chunk = %q", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the flushed data never reached the client while the handler was still running. Flush must write to the socket.")
	}
	release <- struct{}{}
	rest, err := io.ReadAll(br)
	if err != nil || string(rest) != "second part\n" {
		t.Errorf("rest of stream = (%q, %v)", rest, err)
	}
}

func TestL4_5_LargeBodySwitchesToChunked(t *testing.T) {
	addr := serve(t, &httpraw.Server{Handler: httpraw.HandlerFunc(func(w *httpraw.ResponseWriter, r *httpraw.Request) {
		chunk := bytes.Repeat([]byte("z"), 1024)
		for range 1024 {
			w.Write(chunk)
		}
	})})
	c := rawDial(t, addr)
	c.send("GET / HTTP/1.1\r\n\r\n")
	resp, body := c.response("GET")
	if len(body) != 1<<20 {
		t.Errorf("1 MiB response arrived as %d bytes", len(body))
	}
	if len(resp.TransferEncoding) == 0 {
		t.Errorf("buffer at most 64 KiB; past that, start streaming with chunked encoding instead of holding the whole body in memory")
	}
	c.send("GET / HTTP/1.1\r\nConnection: close\r\n\r\n")
	if _, body := c.response("GET"); len(body) != 1<<20 {
		t.Errorf("a chunked response must end cleanly so the connection can be reused")
	}
}

func TestL4_5_SmallBodyKeepsContentLength(t *testing.T) {
	addr := serve(t, &httpraw.Server{Handler: httpraw.HandlerFunc(func(w *httpraw.ResponseWriter, r *httpraw.Request) {
		w.Write(bytes.Repeat([]byte("a"), 60_000))
	})})
	resp, body := get(t, "http://"+addr+"/")
	if resp.ContentLength != 60_000 || len(body) != 60_000 {
		t.Errorf("a 60 KB body fits the buffer and should be sent with Content-Length: got %d", resp.ContentLength)
	}
}

func TestL4_5_HTTP10Streaming(t *testing.T) {
	addr := serve(t, &httpraw.Server{Handler: httpraw.HandlerFunc(func(w *httpraw.ResponseWriter, r *httpraw.Request) {
		w.Write([]byte("old "))
		w.Flush()
		w.Write([]byte("school"))
	})})
	c := rawDial(t, addr)
	c.send("GET / HTTP/1.0\r\n\r\n")
	c.c.SetReadDeadline(time.Now().Add(3 * time.Second))
	raw, _ := io.ReadAll(c.br)
	if bytes.Contains(bytes.ToLower(raw), []byte("transfer-encoding")) {
		t.Errorf("HTTP/1.0 has no chunked encoding: stream the body and close the connection to end it")
	}
	if !bytes.HasSuffix(raw, []byte("old school")) {
		t.Errorf("HTTP/1.0 streamed body: %q", raw)
	}
}

func chunkedEcho() httpraw.HandlerFunc {
	return func(w *httpraw.ResponseWriter, r *httpraw.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(400)
			return
		}
		w.Write(b)
	}
}

func TestL4_5_ChunkedRequestBody(t *testing.T) {
	c := rawDial(t, serve(t, &httpraw.Server{Handler: chunkedEcho()}))
	c.send("POST / HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n" +
		"5\r\nhello\r\n" +
		"1;ext=ignored\r\n \r\n" +
		"A\r\n0123456789\r\n" +
		"0\r\nX-Trailer: ignored\r\n\r\n" +
		"GET /after HTTP/1.1\r\n\r\n")
	if _, body := c.response("POST"); body != "hello 0123456789" {
		t.Errorf("chunked body (with an extension and a trailer) decoded to %q", body)
	}
	if _, body := c.response("GET"); body != "" {
		t.Errorf("the request after a chunked body must parse, got body %q", body)
	}
}

func TestL4_5_ChunkedFromNetHTTP(t *testing.T) {
	addr := serve(t, &httpraw.Server{Handler: chunkedEcho()})
	pr, pw := io.Pipe()
	go func() {
		for i := range 5 {
			fmt.Fprintf(pw, "piece %d;", i)
			time.Sleep(5 * time.Millisecond)
		}
		pw.Close()
	}()
	resp, err := http.Post("http://"+addr+"/", "text/plain", pr) // unknown length → chunked
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if string(b) != "piece 0;piece 1;piece 2;piece 3;piece 4;" {
		t.Errorf("echo of a chunked upload = %q", b)
	}
}

func TestL4_5_BadChunkedBodies(t *testing.T) {
	addr := serve(t, &httpraw.Server{Handler: chunkedEcho()})
	cases := map[string]string{
		"bad hex size":          "zz\r\nhello\r\n0\r\n\r\n",
		"size overflows int64":  "fffffffffffffffffffff\r\nx\r\n0\r\n\r\n",
		"missing CRLF after":    "5\r\nhelloXX0\r\n\r\n",
		"negative-looking size": "-5\r\nhello\r\n0\r\n\r\n",
	}
	for name, body := range cases {
		c := rawDial(t, addr)
		c.send("POST / HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n" + body)
		resp, _ := c.response("POST")
		if resp.StatusCode != 400 {
			t.Errorf("%s: want 400, got %d", name, resp.StatusCode)
		}
	}
}

func TestL4_5_ChunkedOverLimit(t *testing.T) {
	addr := serve(t, &httpraw.Server{MaxBodyBytes: 8, Handler: chunkedEcho()})
	c := rawDial(t, addr)
	c.send("POST / HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n5\r\nworld\r\n0\r\n\r\n")
	resp, _ := c.response("POST")
	if resp.StatusCode != 413 && resp.StatusCode != 400 {
		t.Errorf("a chunked body that grows past MaxBodyBytes must be cut off (413), got %d", resp.StatusCode)
	}
}

func TestL4_5_TransferEncodingRules(t *testing.T) {
	addr := serve(t, &httpraw.Server{Handler: chunkedEcho()})
	cases := []struct {
		name, head string
		want       int
	}{
		{"TE and CL together", "Transfer-Encoding: chunked\r\nContent-Length: 5\r\n", 400},
		{"unsupported coding", "Transfer-Encoding: gzip\r\n", 501},
		{"chunked not last", "Transfer-Encoding: chunked, gzip\r\n", 400},
	}
	for _, tc := range cases {
		c := rawDial(t, addr)
		c.send("POST / HTTP/1.1\r\n" + tc.head + "\r\n5\r\nhello\r\n0\r\n\r\n")
		if got := c.statusLine(); !strings.Contains(got, fmt.Sprint(tc.want)) {
			t.Errorf("%s → %q, want %d", tc.name, got, tc.want)
		}
		c.expectClosed()
	}
}

func TestL4_5_StreamKeys(t *testing.T) {
	st := store.NewMemory()
	for i := range 2500 {
		st.Set(t.Context(), fmt.Sprintf("user:%05d", i), []byte("v"))
	}
	st.Set(t.Context(), "other", []byte("v"))
	r := httpraw.NewRouter()
	httpraw.KVRoutes(r, st)
	addr := serve(t, &httpraw.Server{Handler: r})

	resp, err := http.Get("http://" + addr + "/v1/keys?prefix=user%3A")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if len(resp.TransferEncoding) == 0 {
		t.Errorf("GET /v1/keys should stream (chunked), flushing as it goes")
	}
	sc := bufio.NewScanner(resp.Body)
	n := 0
	for sc.Scan() {
		if want := fmt.Sprintf("user:%05d", n); sc.Text() != want {
			t.Fatalf("line %d = %q, want %q (one key per line, sorted)", n, sc.Text(), want)
		}
		n++
	}
	if n != 2500 {
		t.Errorf("streamed %d keys, want 2500", n)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}
}
