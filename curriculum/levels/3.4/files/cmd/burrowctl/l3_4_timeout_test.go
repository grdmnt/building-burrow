package main

import (
	"net"
	"strings"
	"testing"
	"time"

	"burrow/internal/testkit"
	"burrow/wire"
)

func TestL3_4_CtlTimeout(t *testing.T) {
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
	code, _, errOut := ctl("-addr", ln.Addr().String(), "-timeout", "50ms", "get", "k")
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("with -timeout 50ms against a server that never replies, burrowctl took %v", took)
	}
	if code != 1 {
		t.Errorf("a timed-out call should exit 1, got %d", code)
	}
	if !strings.Contains(errOut, "timeout") {
		t.Errorf("stderr should say `timeout`, got %q", errOut)
	}
}
