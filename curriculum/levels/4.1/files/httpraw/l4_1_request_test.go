package httpraw_test

import (
	"bufio"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"burrow/httpraw"
)

func parse(t *testing.T, raw string) *httpraw.Request {
	t.Helper()
	req, err := httpraw.ReadRequest(bufio.NewReader(strings.NewReader(raw)))
	if err != nil {
		t.Fatalf("ReadRequest(%q): %v", raw, err)
	}
	return req
}

func TestL4_1_ParseRequestLine(t *testing.T) {
	req := parse(t, "GET /hello?name=gerard&x=1 HTTP/1.1\r\nHost: localhost:8080\r\n\r\n")
	checks := []struct{ field, got, want string }{
		{"Method", req.Method, "GET"},
		{"Target", req.Target, "/hello?name=gerard&x=1"},
		{"Path", req.Path, "/hello"},
		{"RawQuery", req.RawQuery, "name=gerard&x=1"},
		{"Proto", req.Proto, "HTTP/1.1"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.field, c.got, c.want)
		}
	}
}

func TestL4_1_Headers(t *testing.T) {
	req := parse(t, "GET / HTTP/1.1\r\n"+
		"Host: example.com\r\n"+
		"User-Agent: curl/8.7.1\r\n"+
		"X-Spaced:    padded value   \r\n"+
		"X-Tabbed:\tvalue\t\r\n"+
		"X-Empty:\r\n"+
		"Accept: text/html\r\n"+
		"accept: application/json\r\n"+
		"\r\n")
	cases := map[string]string{
		"Host": "example.com", "host": "example.com", "HOST": "example.com",
		"user-agent": "curl/8.7.1",
		"X-Spaced":   "padded value",
		"x-tabbed":   "value",
		"X-Empty":    "",
		"Accept":     "text/html",
		"X-Missing":  "",
	}
	for name, want := range cases {
		if got := req.Header.Get(name); got != want {
			t.Errorf("Header.Get(%q) = %q, want %q", name, got, want)
		}
	}
	if got := req.Header.Values("ACCEPT"); !slices.Equal(got, []string{"text/html", "application/json"}) {
		t.Errorf("a header sent twice keeps both values in order: Values(Accept) = %q", got)
	}
	if got := req.Header.Values("x-empty"); len(got) != 1 {
		t.Errorf("an empty header is still present: Values(X-Empty) = %q, want one empty value", got)
	}
	if got := req.Header.Values("x-missing"); len(got) != 0 {
		t.Errorf("Values of an absent header = %q, want none", got)
	}
}

func TestL4_1_InternalWhitespaceKept(t *testing.T) {
	req := parse(t, "GET / HTTP/1.1\r\nX-Words: one  two\tthree\r\n\r\n")
	if got := req.Header.Get("x-words"); got != "one  two\tthree" {
		t.Errorf("trim only the ends of a header value: got %q", got)
	}
}

func TestL4_1_HTTP10(t *testing.T) {
	req := parse(t, "GET / HTTP/1.0\r\n\r\n")
	if req.Proto != "HTTP/1.0" {
		t.Errorf("Proto = %q", req.Proto)
	}
}

func TestL4_1_BareLF(t *testing.T) {
	req := parse(t, "GET /lf HTTP/1.1\nHost: x\n\n")
	if req.Path != "/lf" || req.Header.Get("host") != "x" {
		t.Errorf("lines ending in a bare \\n should be accepted (be lenient in what you accept), got %q %q", req.Path, req.Header.Get("host"))
	}
}

func TestL4_1_LeadingBlankLine(t *testing.T) {
	req := parse(t, "\r\nGET /after-blank HTTP/1.1\r\n\r\n")
	if req.Path != "/after-blank" {
		t.Errorf("RFC 9112 says to ignore an empty line before the request line, got path %q", req.Path)
	}
}

func TestL4_1_AbsoluteFormAndOptions(t *testing.T) {
	req := parse(t, "GET http://example.com/a/b?c=d HTTP/1.1\r\n\r\n")
	if req.Path != "/a/b" || req.RawQuery != "c=d" {
		t.Errorf("absolute-form target: Path=%q RawQuery=%q, want /a/b and c=d", req.Path, req.RawQuery)
	}
	req = parse(t, "OPTIONS * HTTP/1.1\r\n\r\n")
	if req.Target != "*" {
		t.Errorf("OPTIONS * is valid; Target = %q", req.Target)
	}
}

func TestL4_1_StopsAtEndOfHeaders(t *testing.T) {
	br := bufio.NewReader(strings.NewReader(
		"POST /a HTTP/1.1\r\nContent-Length: 5\r\n\r\nhelloGET /b HTTP/1.1\r\n\r\n"))
	req, err := httpraw.ReadRequest(br)
	if err != nil || req.Path != "/a" {
		t.Fatalf("first request: %v", err)
	}
	body := make([]byte, 5)
	if _, err := io.ReadFull(br, body); err != nil || string(body) != "hello" {
		t.Fatalf("ReadRequest must stop right after the blank line; the next bytes should be the body, got %q", body)
	}
	req, err = httpraw.ReadRequest(br)
	if err != nil || req.Path != "/b" {
		t.Errorf("second request on the same reader: (%v, %v)", req, err)
	}
}

func TestL4_1_Malformed(t *testing.T) {
	cases := map[string]string{
		"two parts":             "GET /\r\n\r\n",
		"four parts":            "GET / HTTP/1.1 extra\r\n\r\n",
		"double space":          "GET  / HTTP/1.1\r\n\r\n",
		"no method":             " / HTTP/1.1\r\n\r\n",
		"bad method char":       "G@T / HTTP/1.1\r\n\r\n",
		"relative target":       "GET hello HTTP/1.1\r\n\r\n",
		"lowercase version":     "GET / http/1.1\r\n\r\n",
		"not HTTP":              "GET / FOO/1.1\r\n\r\n",
		"version missing minor": "GET / HTTP/1\r\n\r\n",
		"header without colon":  "GET / HTTP/1.1\r\nBadHeaderLine\r\n\r\n",
		"space before colon":    "GET / HTTP/1.1\r\nHost : x\r\n\r\n",
		"empty header name":     "GET / HTTP/1.1\r\n: value\r\n\r\n",
		"bad header name char":  "GET / HTTP/1.1\r\nX(Y): v\r\n\r\n",
		"obsolete line folding": "GET / HTTP/1.1\r\nX-A: one\r\n  two\r\n\r\n",
		"NUL in header value":   "GET / HTTP/1.1\r\nX-A: a\x00b\r\n\r\n",
		"control char in path":  "GET /a\x01b HTTP/1.1\r\n\r\n",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := httpraw.ReadRequest(bufio.NewReader(strings.NewReader(raw)))
			if !errors.Is(err, httpraw.ErrMalformed) {
				t.Errorf("%q should be rejected with ErrMalformed, got %v", raw, err)
			}
		})
	}
}

func TestL4_1_UnsupportedVersion(t *testing.T) {
	for _, v := range []string{"HTTP/2.0", "HTTP/3.0", "HTTP/0.9", "HTTP/1.2"} {
		_, err := httpraw.ReadRequest(bufio.NewReader(strings.NewReader("GET / " + v + "\r\n\r\n")))
		if !errors.Is(err, httpraw.ErrUnsupportedVersion) {
			t.Errorf("%s is well-formed but unsupported: want ErrUnsupportedVersion, got %v", v, err)
		}
	}
}

func TestL4_1_HeadersTooLarge(t *testing.T) {
	huge := "GET / HTTP/1.1\r\nX-Big: " + strings.Repeat("a", 9000) + "\r\n\r\n"
	if _, err := httpraw.ReadRequest(bufio.NewReader(strings.NewReader(huge))); !errors.Is(err, httpraw.ErrHeaderTooLarge) {
		t.Errorf("a 9 KB header (limit is MaxHeaderBytes = 8 KiB for the whole head) should be ErrHeaderTooLarge, got %v", err)
	}
	longTarget := "GET /" + strings.Repeat("a", 9000) + " HTTP/1.1\r\n\r\n"
	if _, err := httpraw.ReadRequest(bufio.NewReader(strings.NewReader(longTarget))); !errors.Is(err, httpraw.ErrHeaderTooLarge) {
		t.Errorf("a 9 KB request line counts toward the limit too, got %v", err)
	}
	var many strings.Builder
	many.WriteString("GET / HTTP/1.1\r\n")
	for i := range 101 {
		many.WriteString("X-H" + strings.Repeat("x", i%5) + ": v\r\n")
	}
	many.WriteString("\r\n")
	if _, err := httpraw.ReadRequest(bufio.NewReader(strings.NewReader(many.String()))); !errors.Is(err, httpraw.ErrHeaderTooLarge) {
		t.Errorf("more than MaxHeaders (100) header lines should be ErrHeaderTooLarge, got %v", err)
	}
	if httpraw.MaxHeaderBytes != 8<<10 || httpraw.MaxHeaders != 100 {
		t.Errorf("MaxHeaderBytes should be 8 KiB and MaxHeaders 100")
	}
}

func TestL4_1_EOF(t *testing.T) {
	if _, err := httpraw.ReadRequest(bufio.NewReader(strings.NewReader(""))); err != io.EOF {
		t.Errorf("no bytes at all is a clean close: want io.EOF itself, got %v", err)
	}
	for _, raw := range []string{"GET / HT", "GET / HTTP/1.1\r\nHost: x\r\n", "GET / HTTP/1.1\r\n"} {
		_, err := httpraw.ReadRequest(bufio.NewReader(strings.NewReader(raw)))
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Errorf("%q ends mid-request: want io.ErrUnexpectedEOF, got %v", raw, err)
		}
	}
}
