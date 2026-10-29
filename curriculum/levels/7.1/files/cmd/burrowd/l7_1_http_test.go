package main

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"burrow/internal/testkit"
)

func TestL7_1_BothPorts(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary")
	}
	bin := buildBurrowd(t)
	cmd := exec.Command(bin, "serve", "-addr", "127.0.0.1:0", "-http", "127.0.0.1:0")
	stdout, _ := cmd.StdoutPipe()
	stderr := &lockedBuf{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	lines := make(chan string, 2)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()
	read := func(prefix string) string {
		select {
		case l := <-lines:
			addr, ok := strings.CutPrefix(l, prefix)
			if !ok {
				t.Fatalf("expected a %q line, got %q", prefix, l)
			}
			return addr
		case <-time.After(5 * time.Second):
			t.Fatalf("no %q line within 5s; stderr: %s", prefix, stderr)
		}
		return ""
	}
	frameAddr := read("listening on ")
	httpAddr := read("http on ")

	ctx := testkit.Ctx(t)
	c := dialProc(t, &proc{addr: frameAddr})
	c.Set(ctx, "from-frames", []byte("hello"))
	resp, err := http.Get("http://" + httpAddr + "/v1/kv/from-frames")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "hello" || resp.Header.Get("ETag") != `"1"` {
		t.Errorf("a value set over frames should be readable over HTTP: %q ETag %q", b, resp.Header.Get("ETag"))
	}
	req, _ := http.NewRequest("PUT", "http://"+httpAddr+"/v1/kv/from-http", strings.NewReader("hi"))
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 204 {
		t.Fatalf("PUT over HTTP: %v", err)
	}
	if e, err := c.Get(ctx, "from-http"); err != nil || string(e.Value) != "hi" {
		t.Errorf("a value set over HTTP should be readable over frames: (%q, %v)", e.Value, err)
	}
	http.DefaultClient.CloseIdleConnections()

	cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("SIGTERM should exit 0, got %v\n%s", err, stderr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("burrowd didn't exit after SIGTERM")
	}
	for _, a := range []string{frameAddr, httpAddr} {
		if c, err := net.DialTimeout("tcp", a, 500*time.Millisecond); err == nil {
			c.Close()
			t.Errorf("%s still accepts connections after shutdown", a)
		}
	}
}
