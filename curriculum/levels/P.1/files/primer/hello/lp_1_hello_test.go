package main

import (
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// TestLP_1_HelloPrints builds your program the way `go build` would and
// runs it, so it checks the real thing, not just a function.
func TestLP_1_HelloPrints(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary")
	}
	bin := filepath.Join(t.TempDir(), "hello")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Env = append(os.Environ(), "GOFLAGS=", "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ./primer/hello failed. Does "+
			"primer/hello/main.go start with `package main` and have "+
			"a func main()?\n%v\n%s", err, out)
	}
	out, err := exec.Command(bin).Output()
	if err != nil {
		t.Fatalf("running the hello program: %v", err)
	}
	if string(out) != "hello, burrow\n" {
		t.Errorf("the hello program should print exactly %q (Println adds "+
			"the newline), got %q", "hello, burrow\n", out)
	}
}

// TestLP_1_HelloUsesBasics checks that main.go imports your own package.
func TestLP_1_HelloUsesBasics(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	found, files := false, 0
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") ||
			strings.HasSuffix(n, "_test.go") {
			continue
		}
		files++
		f, err := parser.ParseFile(token.NewFileSet(), n, nil,
			parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parsing %s: %v", n, err)
		}
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			found = found || p == "burrow/primer/basics"
		}
	}
	if files == 0 {
		t.Fatal("no .go files in primer/hello yet: create primer/hello/main.go")
	}
	if !found {
		t.Errorf("main.go should import %q and print basics.Greet(%q): "+
			"a program uses your package through its import path, which is "+
			"the module name (burrow) plus the directory",
			"burrow/primer/basics", "burrow")
	}
}
