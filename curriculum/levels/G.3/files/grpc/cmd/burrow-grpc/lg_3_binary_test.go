package main_test

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	reflectionpb "google.golang.org/grpc/reflection/grpc_reflection_v1"

	"burrow/grpc/burrowpb"
)

func build(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "burrow-grpc")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

// The one test on a real TCP port: the binary, reflection and health,
// the way grpcurl and a load balancer would see them.
func TestLG_3_Binary(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary")
	}
	if runtime.GOOS == "windows" {
		t.Skip("sends SIGINT")
	}
	bin := build(t)
	cmd := exec.Command(bin, "-mem", "-listen", "127.0.0.1:0")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() {
		cmd.Process.Kill()
	})

	lines := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		if sc.Scan() {
			lines <- sc.Text()
		}
		for sc.Scan() {
		}
	}()
	var addr string
	select {
	case line := <-lines:
		var ok bool
		addr, ok = strings.CutPrefix(line, "grpc on ")
		if !ok {
			t.Fatalf("first stdout line is %q, want \"grpc on <addr>\"", line)
		}
	case err := <-exited:
		t.Fatalf("burrow-grpc -mem exited early: %v\n%s", err, stderr.String())
	case <-time.After(15 * time.Second):
		t.Fatal("burrow-grpc printed no \"grpc on <addr>\" line within 15s")
	}

	conn, err := grpc.NewClient("passthrough:///"+addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	kv := burrowpb.NewKVClient(conn)
	if _, err := kv.Put(ctx, &burrowpb.PutRequest{Key: "greeting", Value: []byte("hello")}); err != nil {
		t.Fatalf("Put over TCP: %v", err)
	}
	g, err := kv.Get(ctx, &burrowpb.GetRequest{Key: "greeting"})
	if err != nil || string(g.GetEntry().GetValue()) != "hello" {
		t.Fatalf("Get over TCP: %v, %v", g.GetEntry(), err)
	}

	h, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{Service: "burrow.v1.KV"})
	if err != nil || h.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		t.Errorf("health check: %v, %v", h.GetStatus(), err)
	}

	st, err := reflectionpb.NewServerReflectionClient(conn).ServerReflectionInfo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	st.Send(&reflectionpb.ServerReflectionRequest{MessageRequest: &reflectionpb.ServerReflectionRequest_ListServices{}})
	resp, err := st.Recv()
	if err != nil {
		t.Fatalf("reflection: %v", err)
	}
	var names []string
	for _, s := range resp.GetListServicesResponse().GetService() {
		names = append(names, s.GetName())
	}
	if !slices.Contains(names, "burrow.v1.KV") {
		t.Errorf("reflection lists %q, missing burrow.v1.KV", names)
	}
	st.CloseSend()

	cmd.Process.Signal(os.Interrupt)
	select {
	case err := <-exited:
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			t.Errorf("after SIGINT burrow-grpc exited with %d, want 0\n%s", ee.ExitCode(), stderr.String())
		} else if err != nil {
			t.Errorf("after SIGINT: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Errorf("burrow-grpc didn't exit within 15s of SIGINT")
	}
}

func TestLG_3_BinaryUsage(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := exec.CommandContext(ctx, build(t), "-mem", "-listen", "127.0.0.1:0", "bogus").Run()
	if ctx.Err() != nil {
		t.Fatal("burrow-grpc -mem bogus started serving; an unexpected argument is exit status 2")
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 2 {
		t.Errorf("burrow-grpc -mem bogus: %v, want exit status 2", err)
	}
}
