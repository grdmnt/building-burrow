package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const staticcheck = "honnef.co/go/tools/cmd/staticcheck"

// goIn runs the go command at the module root with your environment, minus
// GOFLAGS and any go.work, so the results match a plain `go` invocation.
func goIn(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root, _ := filepath.Abs(filepath.Join("..", ".."))
	cmd := exec.Command("go", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOFLAGS=", "GOWORK=off")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestLM_1_ToolDirective(t *testing.T) {
	out, err := goIn(t, "mod", "edit", "-json")
	if err != nil {
		t.Fatalf("go mod edit -json: %v\n%s", err, out)
	}
	var mod struct {
		Tool []struct{ Path string }
	}
	if err := json.Unmarshal([]byte(out), &mod); err != nil {
		t.Fatal(err)
	}
	for _, tool := range mod.Tool {
		if tool.Path == staticcheck {
			return
		}
	}
	t.Errorf("go.mod has no `tool %s` line. Add it with `go get -tool %s@latest`.", staticcheck, staticcheck)
}

func TestLM_1_GoSumCoversTheTool(t *testing.T) {
	root, _ := filepath.Abs(filepath.Join("..", ".."))
	b, err := os.ReadFile(filepath.Join(root, "go.sum"))
	if err != nil {
		t.Fatalf("no go.sum next to go.mod: %v. `go get -tool` writes it; commit it with go.mod.", err)
	}
	if !bytes.Contains(b, []byte("honnef.co/go/tools ")) {
		t.Errorf("go.sum has no checksums for honnef.co/go/tools")
	}
}

func TestLM_1_Tidy(t *testing.T) {
	if out, err := goIn(t, "mod", "tidy", "-diff"); err != nil {
		t.Errorf("go.mod or go.sum isn't tidy. Run `go mod tidy`. The difference:\n%s", out)
	}
}

func TestLM_1_ToolRuns(t *testing.T) {
	out, err := goIn(t, "tool", "-n", "staticcheck")
	if err != nil {
		t.Fatalf("`go tool -n staticcheck` failed:\n%s", out)
	}
	if strings.TrimSpace(out) == "" {
		t.Errorf("`go tool -n staticcheck` printed no path")
	}
}

// A tool dependency is for building and checking the code. It must never end
// up inside burrowd: every package in the module still imports only burrow/...
// and the standard library.
func TestLM_1_BinaryStaysStdlibOnly(t *testing.T) {
	out, err := goIn(t, "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", "./...")
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, p := range strings.Fields(out) {
		if p != "burrow" && !strings.HasPrefix(p, "burrow/") {
			t.Errorf("%s is imported by the module's code. Only tools may come from outside the standard library.", p)
		}
	}
}
