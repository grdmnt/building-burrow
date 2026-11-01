package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"burrow/api"
	"burrow/internal/testkit"
	"burrow/store"
)

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

func TestLA_4_RunUsage(t *testing.T) {
	cases := []struct {
		name string
		args []string
		code int
		want string
	}{
		{"no command", nil, 2, "usage: accessd"},
		{"unknown command", []string{"bogus"}, 2, `"bogus"`},
		{"serve without -burrow", []string{"serve"}, 2, "-burrow"},
		{"unknown flag", []string{"serve", "-frobnicate"}, 2, "frobnicate"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			if code := run(c.args, &out, &errOut); code != c.code {
				t.Errorf("run(%q) = %d, want %d", c.args, code, c.code)
			}
			if !strings.Contains(errOut.String(), c.want) {
				t.Errorf("run(%q) stderr = %q, want it to mention %s", c.args, errOut.String(), c.want)
			}
		})
	}
}

func post(t *testing.T, url, body string, hdr ...string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest("POST", url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func TestLA_4_AccessdOverBurrow(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary")
	}
	bin := filepath.Join(t.TempDir(), "accessd")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	mem := store.NewMemory()
	t.Cleanup(func() { mem.Close() })
	burrowSrv := httptest.NewServer((&api.Server{Store: mem}).Handler())
	t.Cleanup(burrowSrv.Close)

	cmd := exec.Command(bin, "serve", "-burrow", burrowSrv.URL, "-http", "127.0.0.1:0")
	stdout, _ := cmd.StdoutPipe()
	stderr := &lockedBuf{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	lines := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()
	var addr string
	select {
	case l := <-lines:
		a, ok := strings.CutPrefix(l, "http on ")
		if !ok {
			t.Fatalf("first stdout line %q, want \"http on <addr>\"", l)
		}
		addr = a
	case <-time.After(5 * time.Second):
		t.Fatalf("no \"http on <addr>\" line within 5s; stderr:\n%s", stderr)
	}
	base := "http://" + addr

	resp, b := post(t, base+"/v1/teams", `{"name":"Platform"}`)
	var tm struct {
		ID      string `json:"id"`
		Version uint64 `json:"version"`
	}
	json.Unmarshal(b, &tm)
	if resp.StatusCode != 201 || tm.ID == "" {
		t.Fatalf("POST /v1/teams through accessd = %d %s", resp.StatusCode, b)
	}
	ctx := testkit.Ctx(t)
	e, err := mem.Get(ctx, "access/team/"+tm.ID)
	if err != nil || e.Version != tm.Version {
		t.Fatalf("Burrow should hold access/team/%s at version %d: (%+v, %v). "+
			"Wire burrow.New(-burrow URL) into kvrepo.New in main", tm.ID, tm.Version, e, err)
	}
	resp, b = post(t, base+"/v1/teams/"+tm.ID+"/keys", `{"name":"ci","role":"read"}`,
		"If-Match", fmt.Sprintf(`"%d"`, tm.Version))
	var k struct {
		Token string `json:"token"`
	}
	json.Unmarshal(b, &k)
	if resp.StatusCode != 201 || k.Token == "" {
		t.Fatalf("issuing a key through accessd = %d %s", resp.StatusCode, b)
	}
	if resp, b := post(t, base+"/v1/authenticate", `{"token":"`+k.Token+`"}`); resp.StatusCode != 200 {
		t.Errorf("authenticate through accessd = %d %s", resp.StatusCode, b)
	}
	if resp.Header.Get("X-Request-ID") == "" {
		t.Errorf("accessd responses carry no X-Request-ID; serve httpapi.New's handler as is")
	}
	testkit.Eventually(t, 2*time.Second, func() bool {
		s := stderr.String()
		return strings.Contains(s, `"msg":"event"`) && strings.Contains(s, `"type":"key.issued"`) &&
			strings.Contains(s, `"type":"team.created"`)
	}, "the event sink in main to log JSON lines with msg \"event\" and the event type; stderr:\n%s", stderr)
	if strings.Contains(stderr.String(), k.Token) {
		t.Errorf("accessd logged a plaintext token")
	}
	http.DefaultClient.CloseIdleConnections()

	cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("SIGTERM should shut down cleanly with exit 0, got %v\n%s", err, stderr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("accessd didn't exit within 10s of SIGTERM")
	}
}
