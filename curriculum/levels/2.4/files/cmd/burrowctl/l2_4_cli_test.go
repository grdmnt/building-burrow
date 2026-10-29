package main

import (
	"bytes"
	"net"
	"strings"
	"testing"
	"time"

	"burrow/internal/testkit"
	"burrow/store"
	"burrow/wire"
)

func server(t *testing.T) string {
	t.Helper()
	ln := testkit.Listen(t)
	go (&wire.Server{Store: store.NewMemory()}).Serve(ln)
	return ln.Addr().String()
}

func ctl(args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestL2_4_CtlSetGetDel(t *testing.T) {
	addr := server(t)
	steps := []struct {
		args     []string
		code     int
		stdout   string
		inStderr string
	}{
		{[]string{"-addr", addr, "set", "name", "hello world"}, 0, "OK\n", ""},
		{[]string{"-addr", addr, "get", "name"}, 0, "hello world\n", ""},
		{[]string{"-addr", addr, "del", "name"}, 0, "OK\n", ""},
		{[]string{"-addr", addr, "get", "name"}, 1, "", "not found"},
		{[]string{"-addr", addr, "del", "name"}, 1, "", "not found"},
		{[]string{"-addr", addr, "ping"}, 0, "PONG\n", ""},
		{[]string{"-addr", addr, "set", "bad key", "v"}, 1, "", "invalid key"},
	}
	for _, s := range steps {
		code, out, errOut := ctl(s.args...)
		cmd := "burrowctl " + strings.Join(s.args[2:], " ")
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

func TestL2_4_CtlUsageErrors(t *testing.T) {
	addr := server(t)
	for _, args := range [][]string{
		{},
		{"-addr", addr},
		{"-addr", addr, "frob"},
		{"-addr", addr, "get"},
		{"-addr", addr, "get", "a", "b"},
		{"-addr", addr, "set", "k"},
		{"-addr", addr, "set", "k", "v", "extra"},
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

func TestL2_4_CtlDeadServer(t *testing.T) {
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

func TestL2_4_CtlTimeoutFlag(t *testing.T) {
	ln := testkit.Listen(t)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { c.Close() })
			go func(c net.Conn) { wire.ReadFrame(c) }(c) // read the request, never reply
		}
	}()
	start := time.Now()
	code, _, errOut := ctl("-addr", ln.Addr().String(), "-timeout", "200ms", "get", "k")
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("with -timeout 200ms against a server that never replies, burrowctl took %v", took)
	}
	if code != 1 {
		t.Errorf("a timed-out call should exit 1, got %d", code)
	}
	if !strings.Contains(errOut, "deadline") && !strings.Contains(errOut, "timeout") && !strings.Contains(errOut, "timed out") {
		t.Errorf("stderr should say it timed out, got %q", errOut)
	}
}
