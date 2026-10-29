package main

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"burrow/client"
)

type proc struct {
	cmd    *exec.Cmd
	addr   string
	stderr *lockedBuf
}

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuf) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

func buildBurrowd(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "burrowd")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

func startBurrowd(t *testing.T, bin string, args ...string) *proc {
	t.Helper()
	cmd := exec.Command(bin, append([]string{"serve", "-addr", "127.0.0.1:0"}, args...)...)
	stdout, _ := cmd.StdoutPipe()
	p := &proc{cmd: cmd, stderr: &lockedBuf{}}
	cmd.Stderr = p.stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	lines := make(chan string, 1)
	go func() {
		s := bufio.NewScanner(stdout)
		if s.Scan() {
			lines <- s.Text()
		}
		io.Copy(io.Discard, stdout)
	}()
	select {
	case line := <-lines:
		addr, ok := strings.CutPrefix(line, "listening on ")
		if !ok {
			t.Fatalf("the first stdout line should be \"listening on <addr>\", got %q", line)
		}
		p.addr = addr
	case <-time.After(5 * time.Second):
		t.Fatalf("burrowd serve didn't print \"listening on <addr>\" within 5s; stderr: %s", p.stderr)
	}
	return p
}

func TestL3_5_ServeAndSignal(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary")
	}
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			p := startBurrowd(t, buildBurrowd(t))
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c, err := client.Dial(ctx, p.addr)
			if err != nil {
				t.Fatalf("dialing the printed address %s: %v", p.addr, err)
			}
			defer c.Close()
			if _, err := c.Set(ctx, "k", []byte("v")); err != nil {
				t.Fatalf("Set against burrowd: %v", err)
			}
			if v, err := c.Get(ctx, "k"); err != nil || string(v.Value) != "v" {
				t.Fatalf("Get against burrowd: (%q, %v)", v.Value, err)
			}

			p.cmd.Process.Signal(sig)
			done := make(chan error, 1)
			go func() { done <- p.cmd.Wait() }()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("after %v burrowd should exit 0, got %v\nstderr:\n%s", sig, err, p.stderr)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("burrowd didn't exit within 5s of %v", sig)
			}
			if !strings.Contains(p.stderr.String(), "shutting down") {
				t.Errorf("burrowd should log \"shutting down\" to stderr when it gets a signal; stderr:\n%s", p.stderr)
			}
		})
	}
}

func TestL3_5_ServeBadAddress(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"serve", "-addr", "not-an-address"}, &out, &errOut)
	if code != 1 {
		t.Errorf("serve with an unusable address should exit 1, got %d", code)
	}
	if errOut.Len() == 0 {
		t.Errorf("explain the listen error on stderr")
	}
}

func TestL3_5_ServeRejectsArgs(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"serve", "-addr", "127.0.0.1:0", "extra"}, &out, &errOut); code != 2 {
		t.Errorf("`burrowd serve extra` should exit 2, got %d", code)
	}
	if code := run([]string{"serve", "-bogus"}, &out, &errOut); code != 2 {
		t.Errorf("`burrowd serve -bogus` should exit 2, got %d", code)
	}
}
