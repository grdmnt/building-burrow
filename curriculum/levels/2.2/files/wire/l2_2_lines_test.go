package wire_test

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"burrow/internal/testkit"
	"burrow/store"
	"burrow/wire"
)

type lineConn struct {
	t *testing.T
	c net.Conn
	r *bufio.Reader
}

func startLines(t *testing.T) (*lineConn, *store.Memory, string) {
	t.Helper()
	st := store.NewMemory()
	ln := testkit.Listen(t)
	errc := make(chan error, 1)
	go func() { errc <- wire.ServeLines(ln, st, nil) }()
	t.Cleanup(func() {
		ln.Close()
		select {
		case err := <-errc:
			if err != nil {
				t.Errorf("ServeLines should return nil once its listener closes, got %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Errorf("ServeLines didn't return within 2s of its listener closing")
		}
	})
	return openLines(t, ln.Addr().String()), st, ln.Addr().String()
}

func openLines(t *testing.T, addr string) *lineConn {
	c := dial(t, addr)
	return &lineConn{t: t, c: c, r: bufio.NewReader(c)}
}

func (l *lineConn) send(s string) {
	l.t.Helper()
	if _, err := io.WriteString(l.c, s); err != nil {
		l.t.Fatalf("write %q: %v", s, err)
	}
}

func (l *lineConn) line() string {
	l.t.Helper()
	_ = l.c.SetReadDeadline(time.Now().Add(3 * time.Second))
	s, err := l.r.ReadString('\n')
	if err != nil {
		l.t.Fatalf("waiting for a reply line: %v (got %q so far)", err, s)
	}
	return strings.TrimSuffix(s, "\n")
}

func (l *lineConn) do(cmd, want string) {
	l.t.Helper()
	l.send(cmd + "\n")
	if got := l.line(); got != want {
		l.t.Errorf("%q → %q, want %q", cmd, got, want)
	}
}

func (l *lineConn) doPrefix(cmd, wantPrefix string) string {
	l.t.Helper()
	l.send(cmd + "\n")
	got := l.line()
	if !strings.HasPrefix(got, wantPrefix) {
		l.t.Errorf("%q → %q, want a reply starting with %q", cmd, got, wantPrefix)
	}
	return got
}

func (l *lineConn) expectClosed() {
	l.t.Helper()
	_ = l.c.SetReadDeadline(time.Now().Add(3 * time.Second))
	rest, err := io.ReadAll(l.r)
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			l.t.Fatalf("expected the server to close the connection, but it stayed open")
		}
	}
	if len(rest) > 0 {
		l.t.Errorf("unexpected bytes before close: %q", rest)
	}
}

func TestL2_2_SetGetDel(t *testing.T) {
	c, _, _ := startLines(t)
	c.do("SET name gerard", "OK")
	c.do("GET name", "VALUE gerard")
	c.do("DEL name", "OK")
	c.do("GET name", "NOT_FOUND")
	c.do("DEL name", "NOT_FOUND")
}

func TestL2_2_ValuesKeepSpaces(t *testing.T) {
	c, _, _ := startLines(t)
	c.do("SET greeting hello there,  world ", "OK")
	c.do("GET greeting", "VALUE hello there,  world ")
}

func TestL2_2_EmptyValue(t *testing.T) {
	c, st, _ := startLines(t)
	c.do("SET k ", "OK")
	v, err := st.Get(context.Background(), "k")
	if err != nil || len(v) != 0 {
		t.Errorf(`"SET k " (trailing space) sets an empty value; store has (%q, %v)`, v, err)
	}
	c.do("GET k", "VALUE ")
}

func TestL2_2_CommandsAreCaseInsensitive(t *testing.T) {
	c, _, _ := startLines(t)
	c.do("set Key v1", "OK")
	c.do("Get Key", "VALUE v1")
	c.do("gEt key", "NOT_FOUND") // keys stay case-sensitive
	c.do("ping", "PONG")
}

func TestL2_2_CRLF(t *testing.T) {
	c, _, _ := startLines(t)
	c.send("SET k v\r\n")
	if got := c.line(); got != "OK" {
		t.Errorf("CRLF line endings (from telnet or nc -C) should work, got %q", got)
	}
	c.do("GET k", "VALUE v")
}

func TestL2_2_Ping(t *testing.T) {
	c, _, _ := startLines(t)
	c.do("PING", "PONG")
	c.do("PING are you there?", "PONG are you there?")
}

func TestL2_2_Keys(t *testing.T) {
	c, _, _ := startLines(t)
	c.do("SET user:2 b", "OK")
	c.do("SET user:1 a", "OK")
	c.do("SET order:1 x", "OK")
	c.do("KEYS user:", "KEYS 2")
	if a, b := c.line(), c.line(); a != "user:1" || b != "user:2" {
		t.Errorf("KEYS user: should list user:1 then user:2, got %q, %q", a, b)
	}
	c.do("KEYS", "KEYS 3")
	c.line()
	c.line()
	c.line()
	c.do("KEYS nothing", "KEYS 0")
	c.do("PING", "PONG") // still in sync
}

func TestL2_2_Errors(t *testing.T) {
	c, _, _ := startLines(t)
	c.do("FROB x", `ERR unknown command "FROB"`)
	c.doPrefix("GET", "ERR wrong number of arguments")
	c.doPrefix("GET a b", "ERR wrong number of arguments")
	c.doPrefix("SET k", "ERR wrong number of arguments")
	c.doPrefix("DEL", "ERR wrong number of arguments")
	c.doPrefix("GET "+strings.Repeat("x", 300), "ERR invalid key")
	c.do("PING", "PONG") // errors don't close the connection
}

func TestL2_2_BlankLinesIgnored(t *testing.T) {
	c, _, _ := startLines(t)
	c.send("\n   \n\r\n")
	c.do("PING", "PONG")
}

func TestL2_2_Pipelining(t *testing.T) {
	c, _, _ := startLines(t)
	c.send("SET a 1\nSET b 2\nGET a\nGET b\nGET c\n")
	want := []string{"OK", "OK", "VALUE 1", "VALUE 2", "NOT_FOUND"}
	for i, w := range want {
		if got := c.line(); got != w {
			t.Errorf("pipelined reply %d = %q, want %q", i+1, got, w)
		}
	}
}

func TestL2_2_Quit(t *testing.T) {
	c, _, _ := startLines(t)
	c.do("QUIT", "BYE")
	c.expectClosed()
}

func TestL2_2_LineTooLong(t *testing.T) {
	c, _, _ := startLines(t)
	go c.send("SET k " + strings.Repeat("v", 70_000) + "\n")
	if got := c.line(); got != "ERR line too long" {
		t.Errorf("a line over 64 KiB should get %q, got %q", "ERR line too long", got)
	}
	c.expectClosed()
}

func TestL2_2_ClientsShareTheStore(t *testing.T) {
	a, _, addr := startLines(t)
	b := openLines(t, addr)
	a.do("SET shared yes", "OK")
	b.do("GET shared", "VALUE yes")
}

func TestL2_2_PartialWrites(t *testing.T) {
	c, _, _ := startLines(t)
	for _, chunk := range []string{"SE", "T k", " val", "ue\nGE", "T k\n"} {
		c.send(chunk)
		time.Sleep(5 * time.Millisecond)
	}
	if a, b := c.line(), c.line(); a != "OK" || b != "VALUE value" {
		t.Errorf("a command split across several TCP writes must still parse: got %q, %q", a, b)
	}
}
