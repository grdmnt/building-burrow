package testkit

// Helpers for grading the tests, fuzz targets, examples and benchmarks you write
// yourself. Your own tests are graded the way mutation testing works: the
// provided test copies the module to a temporary directory, swaps one of your
// functions for a deliberately broken version (a "mutant"), runs only your
// tests, and expects at least one of them to fail.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// providedName matches the names of tests, fuzz targets, benchmarks and
// examples that ship with the curriculum. Yours must not match it.
var providedName = regexp.MustCompile(`^(Test|Fuzz|Benchmark|Example)L[0-9A-Z][0-9A-Za-z]*_`)

// skipProvided is passed to `go test -skip` so only your own tests run.
const skipProvided = `^(Test|Fuzz|Benchmark|Example)L[0-9A-Z][0-9A-Za-z]*_`

// providedFile matches test files that ship with the curriculum, such as
// l4_3_crc_test.go or lh_1_keepalive_test.go.
var providedFile = regexp.MustCompile(`^l([0-9]+|[a-z])_[0-9]+_.*_test\.go$`)

// Mutant is one deliberately broken version of a function in your package.
type Mutant struct {
	// Bug says what is wrong with this version. It's shown when your tests
	// don't notice it.
	Bug string
	// Target names the function to replace: "ValidateKey" for a function,
	// "Log.Truncate" for a method (pointer or value receiver).
	Target string
	// Source is a complete Go file (without the package clause) defining the
	// replacement. Your original is still there, renamed to Orig plus its
	// name (OrigValidateKey, or the method OrigTruncate), so most mutants wrap it.
	Source string
}

// MutationCheck describes how your tests in one package are graded.
type MutationCheck struct {
	Dir             string // package directory relative to the module root, such as "store"
	RequireSubtests bool   // at least one of your tests must use t.Run
	RequireInTests  string // text that must appear in your test files, such as "t.TempDir()"
	Mutants         []Mutant
}

// CheckYourTests runs your tests against your code (they must pass) and
// against every mutant (each must make at least one of them fail).
func CheckYourTests(t *testing.T, mc MutationCheck) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipped in -short mode")
	}
	root := ModuleRoot(t)
	pkgDir := filepath.Join(root, mc.Dir)

	files := yourTestFiles(t, pkgDir)
	if len(files) == 0 {
		t.Fatalf("no test files of your own in %s/. Write one, such as %s/%s_test.go, with tests whose names don't start with TestL.",
			mc.Dir, mc.Dir, filepath.Base(mc.Dir))
	}
	if mc.RequireInTests != "" {
		found := false
		for _, f := range files {
			b, _ := os.ReadFile(f)
			if bytes.Contains(b, []byte(mc.RequireInTests)) {
				found = true
			}
		}
		if !found {
			t.Errorf("your tests in %s/ should use %s", mc.Dir, mc.RequireInTests)
		}
	}

	base := copyModule(t, root)
	res := goTestJSON(t, base, "./"+mc.Dir)
	if res.buildFailed {
		t.Fatalf("your tests in %s/ don't compile:\n%s", mc.Dir, res.tail())
	}
	if len(res.ran) == 0 {
		t.Fatalf("none of your own tests ran in %s/ (names starting with TestL are reserved for the provided ones)", mc.Dir)
	}
	if !res.ok {
		t.Fatalf("your own tests fail against your code. Fix them (or the code) first:\n%s", res.tail())
	}
	if mc.RequireSubtests && !slices.ContainsFunc(res.ran, func(n string) bool { return strings.Contains(n, "/") }) {
		t.Errorf("your tests in %s/ ran without subtests; make at least one table-driven with t.Run. Ran: %s",
			mc.Dir, strings.Join(res.ran, ", "))
	}

	// Each mutant gets its own copy of the module, and up to four run at once.
	problems := make([]string, len(mc.Mutants))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, m := range mc.Mutants {
		dir := copyModule(t, root)
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if err := applyMutant(filepath.Join(dir, mc.Dir), m); err != nil {
				problems[i] = fmt.Sprintf("mutant %q: %v", m.Bug, err)
				return
			}
			r := goTestJSON(t, dir, "./"+mc.Dir)
			switch {
			case r.buildFailed:
				problems[i] = fmt.Sprintf("mutant %q didn't compile against your package. Check that %s matches the spec's signature:\n%s",
					m.Bug, m.Target, r.tail())
			case r.ok:
				problems[i] = "your tests don't catch this bug: " + m.Bug
			}
		}()
	}
	wg.Wait()
	for _, p := range problems {
		if p != "" {
			t.Error(p)
		}
	}
}

// RequireExample runs Example<name> in dir and requires it to pass. An example
// only runs when it has an "// Output:" comment.
func RequireExample(t *testing.T, dir, name string) {
	t.Helper()
	root := ModuleRoot(t)
	out, err := runGo(root, 2*time.Minute, "test", "-count=1", "-v", "-run", "^"+name+"$", "./"+dir)
	if strings.Contains(out, "--- PASS: "+name) {
		return
	}
	if strings.Contains(out, "--- FAIL: "+name) {
		t.Fatalf("%s fails:\n%s", name, lastLines(out, 30))
	}
	if err != nil && strings.Contains(out, "build failed") {
		t.Fatalf("%s/ doesn't compile:\n%s", dir, lastLines(out, 30))
	}
	t.Fatalf("no runnable %s in %s/. Write func %s() in a _test.go file and end it with an // Output: comment.", name, dir, name)
}

// YourFuzzTargets lists the fuzz targets in dir that aren't provided ones.
func YourFuzzTargets(t *testing.T, dir string) []string { return yourList(t, dir, "^Fuzz") }

// YourBenchmarks lists the benchmarks in dir that aren't provided ones.
func YourBenchmarks(t *testing.T, dir string) []string { return yourList(t, dir, "^Benchmark") }

func yourList(t *testing.T, dir, pattern string) []string {
	t.Helper()
	out, err := runGo(ModuleRoot(t), 2*time.Minute, "test", "-list", pattern, "./"+dir)
	if err != nil {
		t.Fatalf("go test -list in %s/: %v\n%s", dir, err, lastLines(out, 30))
	}
	var names []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if regexp.MustCompile(pattern).MatchString(line) && !providedName.MatchString(line) {
			names = append(names, line)
		}
	}
	return names
}

// RunFuzz fuzzes target in a copy of the module for d and fails the test if
// the target fails, on its seed corpus or on a generated input.
func RunFuzz(t *testing.T, dir, target string, d time.Duration) {
	t.Helper()
	cp := copyModule(t, ModuleRoot(t))
	out, err := runGo(cp, d+2*time.Minute, "test", "-count=1", "-run", "^$", "-fuzz", "^"+target+"$",
		"-fuzztime", d.String(), "./"+dir)
	if err == nil {
		return
	}
	// The fuzzer saved the failing input in the copy. Bring it back so
	// `go test -run <target>` reproduces the failure in your own package.
	note := ""
	corpus := filepath.Join(dir, "testdata", "fuzz", target)
	if entries, _ := os.ReadDir(filepath.Join(cp, corpus)); len(entries) > 0 {
		real := filepath.Join(ModuleRoot(t), corpus)
		os.MkdirAll(real, 0o755)
		for _, e := range entries {
			if b, err := os.ReadFile(filepath.Join(cp, corpus, e.Name())); err == nil {
				os.WriteFile(filepath.Join(real, e.Name()), b, 0o644)
			}
		}
		note = fmt.Sprintf("\nThe failing input is now in %s/; `go test -run %s ./%s` replays it.", corpus, target, dir)
	}
	t.Fatalf("go test -fuzz=%s failed. Either your code has a bug the fuzzer found, or the target's checks are wrong:\n%s%s",
		target, lastLines(out, 40), note)
}

// RunBenchmark runs one benchmark n times and fails the test if it fails.
func RunBenchmark(t *testing.T, dir, name string, n int) string {
	t.Helper()
	out, err := runGo(ModuleRoot(t), 3*time.Minute, "test", "-count=1", "-run", "^$", "-bench", "^"+name+"$",
		"-benchtime", fmt.Sprintf("%dx", n), "-benchmem", "./"+dir)
	if err != nil {
		t.Fatalf("%s failed:\n%s", name, lastLines(out, 30))
	}
	return out
}

// ModuleRoot returns the directory holding the go.mod of module burrow.
func ModuleRoot(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if b, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil &&
			regexp.MustCompile(`(?m)^module\s+burrow\s*$`).Match(b) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("couldn't find the go.mod for module burrow above the test's directory")
		}
		dir = parent
	}
}

func yourTestFiles(t *testing.T, pkgDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() && strings.HasSuffix(n, "_test.go") && !providedFile.MatchString(n) {
			files = append(files, filepath.Join(pkgDir, n))
		}
	}
	return files
}

// copyModule copies the module (without nested modules, dot directories and
// version control) to a fresh temporary directory.
func copyModule(t *testing.T, root string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			if _, err := os.Stat(filepath.Join(p, "go.mod")); err == nil {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	if err != nil {
		t.Fatalf("copying the module: %v", err)
	}
	return dst
}

// applyMutant renames the target in your package to Orig<Name> and adds the
// mutant's file next to it.
func applyMutant(pkgDir string, m Mutant) error {
	recv, name, isMethod := strings.Cut(m.Target, ".")
	if !isMethod {
		name, recv = recv, ""
	}
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		return err
	}
	var pkgName string
	found := 0
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		path := filepath.Join(pkgDir, n)
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		pkgName = f.Name.Name
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Name.Name != name || (fd.Recv != nil) != (recv != "") {
				continue
			}
			if recv != "" && receiverName(fd) != recv {
				continue
			}
			off := fset.Position(fd.Name.Pos()).Offset
			src = append(src[:off:off], append([]byte("Orig"+name), src[off+len(name):]...)...)
			found++
			if err := os.WriteFile(path, src, 0o644); err != nil {
				return err
			}
			break
		}
	}
	if found != 1 {
		return fmt.Errorf("couldn't find %s in %s", m.Target, filepath.Base(pkgDir))
	}
	return os.WriteFile(filepath.Join(pkgDir, "zz_mutant.go"),
		[]byte("package "+pkgName+"\n\n"+m.Source), 0o644)
}

func receiverName(fd *ast.FuncDecl) string {
	if len(fd.Recv.List) == 0 {
		return ""
	}
	e := fd.Recv.List[0].Type
	if s, ok := e.(*ast.StarExpr); ok {
		e = s.X
	}
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.IndexExpr:
		if id, ok := x.X.(*ast.Ident); ok {
			return id.Name
		}
	}
	return ""
}

type testRun struct {
	ok          bool
	buildFailed bool
	ran         []string
	output      []string
}

func (r testRun) tail() string { return lastLines(strings.Join(r.output, ""), 40) }

// goTestJSON runs your tests (and only yours) in pkg under dir.
func goTestJSON(t *testing.T, dir, pkg string) testRun {
	t.Helper()
	out, err := runGo(dir, 3*time.Minute, "test", "-json", "-count=1", "-timeout=90s",
		"-skip", skipProvided, pkg)
	r := testRun{ok: err == nil}
	sawEvent := false
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var ev struct {
			Action, Test, Output, FailedBuild string
		}
		line := sc.Bytes()
		if json.Unmarshal(line, &ev) != nil || ev.Action == "" {
			r.output = append(r.output, string(line)+"\n")
			continue
		}
		sawEvent = true
		switch ev.Action {
		case "run":
			r.ran = append(r.ran, ev.Test)
		case "output", "build-output":
			r.output = append(r.output, ev.Output)
			if strings.Contains(ev.Output, "[build failed]") || strings.Contains(ev.Output, "[setup failed]") {
				r.buildFailed = true
			}
		case "build-fail":
			r.buildFailed = true
		case "fail":
			if ev.FailedBuild != "" {
				r.buildFailed = true
			}
		}
	}
	if !r.ok && !sawEvent {
		r.buildFailed = true
	}
	return r
}

func runGo(dir string, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=", "GOWORK=off")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = append([]string{"..."}, lines[len(lines)-n:]...)
	}
	return strings.Join(lines, "\n")
}
