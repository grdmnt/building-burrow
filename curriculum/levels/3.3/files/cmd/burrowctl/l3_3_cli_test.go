package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"burrow/internal/testkit"
	"burrow/store"
	"burrow/wire"
)

func server(t *testing.T) string {
	t.Helper()
	st := store.NewMemory()
	t.Cleanup(func() { st.Close() })
	ln := testkit.Listen(t)
	go (&wire.Server{Store: st}).Serve(ln)
	return ln.Addr().String()
}

func ctl(args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestL3_3_CtlCommands(t *testing.T) {
	addr := server(t)
	steps := []struct {
		args     []string
		code     int
		stdout   string
		inStderr string
	}{
		{[]string{"set", "name", "hello world"}, 0, "OK 1\n", ""},
		{[]string{"get", "name"}, 0, "hello world\n", ""},
		{[]string{"set", "name", "again"}, 0, "OK 2\n", ""},
		{[]string{"cas", "name", "1", "stale"}, 1, "", "version mismatch"},
		{[]string{"cas", "name", "2", "fresh"}, 0, "OK 3\n", ""},
		{[]string{"cas", "-ttl", "1h", "lock", "0", "me"}, 0, "OK 1\n", ""},
		{[]string{"ttl", "session", "10m", "abc"}, 0, "OK 1\n", ""},
		{[]string{"touch", "name", "5m"}, 0, "OK 3\n", ""},
		{[]string{"keys"}, 0, "lock\nname\nsession\n", ""},
		{[]string{"keys", "se"}, 0, "session\n", ""},
		{[]string{"cad", "name", "1"}, 1, "", "version mismatch"},
		{[]string{"cad", "name", "3"}, 0, "OK\n", ""},
		{[]string{"del", "session"}, 0, "OK\n", ""},
		{[]string{"get", "session"}, 1, "", "not found"},
		{[]string{"del", "session"}, 1, "", "not found"},
		{[]string{"ping"}, 0, "PONG\n", ""},
		{[]string{"set", "bad key", "v"}, 1, "", "invalid key"},
	}
	for _, s := range steps {
		code, out, errOut := ctl(append([]string{"-addr", addr}, s.args...)...)
		cmd := "burrowctl " + strings.Join(s.args, " ")
		if code != s.code {
			t.Errorf("`%s` exited %d, want %d (stderr: %q)", cmd, code, s.code, errOut)
		}
		if out != s.stdout {
			t.Errorf("`%s` printed %q, want %q", cmd, out, s.stdout)
		}
		if s.inStderr != "" && !strings.Contains(errOut, s.inStderr) {
			t.Errorf("`%s` stderr should mention %q, got %q", cmd, s.inStderr, errOut)
		}
	}
}

func TestL3_3_CtlUsageErrors(t *testing.T) {
	addr := server(t)
	for _, args := range [][]string{
		{},
		{"-addr", addr},
		{"-addr", addr, "frob"},
		{"-addr", addr, "get"},
		{"-addr", addr, "get", "a", "b"},
		{"-addr", addr, "set", "k"},
		{"-addr", addr, "cas", "k", "notanumber", "v"},
		{"-addr", addr, "ttl", "k", "soon", "v"},
		{"-addr", addr, "keys", "a", "b"},
		{"-nope"},
	} {
		code, out, errOut := ctl(args...)
		if code != 2 {
			t.Errorf("`burrowctl %s` is a usage error and should exit 2, got %d", strings.Join(args, " "), code)
		}
		if out != "" {
			t.Errorf("`burrowctl %s` printed to stdout: %q", strings.Join(args, " "), out)
		}
		if errOut == "" {
			t.Errorf("`burrowctl %s` should explain the problem on stderr", strings.Join(args, " "))
		}
	}
}

func TestL3_3_CtlDeadServer(t *testing.T) {
	dead := testkit.DeadAddr(t)
	start := time.Now()
	code, _, errOut := ctl("-addr", dead, "ping")
	if code != 1 {
		t.Errorf("can't reach the server: exit 1, got %d", code)
	}
	if !strings.Contains(errOut, dead) {
		t.Errorf("the error should name the address it tried (%s), got %q", dead, errOut)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("failing to connect took %v", time.Since(start))
	}
}
