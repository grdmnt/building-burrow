package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// call runs your run() function and captures what it prints.
func call(args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestL0_1_VersionCommand(t *testing.T) {
	code, out, errOut := call("version")
	if code != 0 {
		t.Errorf("`burrowd version` should exit 0, got %d", code)
	}
	if out != "burrowd 0.1.0\n" {
		t.Errorf("`burrowd version` should print exactly %q to stdout, got %q", "burrowd 0.1.0\n", out)
	}
	if errOut != "" {
		t.Errorf("`burrowd version` should print nothing to stderr, got %q", errOut)
	}
}

func TestL0_1_VersionFlags(t *testing.T) {
	for _, arg := range []string{"-version", "--version"} {
		t.Run(arg, func(t *testing.T) {
			code, out, _ := call(arg)
			if code != 0 || out != "burrowd 0.1.0\n" {
				t.Errorf("`burrowd %s` should behave like `burrowd version`: got exit %d, stdout %q", arg, code, out)
			}
		})
	}
}

func TestL0_1_VersionRejectsExtraArgs(t *testing.T) {
	code, out, errOut := call("version", "now")
	if code != 2 {
		t.Errorf("`burrowd version now` has an unexpected argument and should exit 2, got %d", code)
	}
	if out != "" {
		t.Errorf("errors go to stderr, but stdout got %q", out)
	}
	if !strings.Contains(errOut, "now") {
		t.Errorf("the error should name the unexpected argument %q, got %q", "now", errOut)
	}
}

func TestL0_1_NoArgsPrintsUsage(t *testing.T) {
	code, out, errOut := call()
	if code != 2 {
		t.Errorf("`burrowd` with no command should exit 2 (usage error), got %d", code)
	}
	if out != "" {
		t.Errorf("usage errors go to stderr, but stdout got %q", out)
	}
	if !strings.HasPrefix(errOut, "usage: burrowd <command>") {
		t.Errorf("stderr should start with %q, got %q", "usage: burrowd <command>", errOut)
	}
	if !strings.Contains(errOut, "version") {
		t.Errorf("usage should list the `version` command, got %q", errOut)
	}
}

func TestL0_1_Help(t *testing.T) {
	for _, arg := range []string{"help", "-h", "--help"} {
		t.Run(arg, func(t *testing.T) {
			code, out, errOut := call(arg)
			if code != 0 {
				t.Errorf("asking for help isn't an error: `burrowd %s` should exit 0, got %d", arg, code)
			}
			if !strings.HasPrefix(out, "usage: burrowd <command>") {
				t.Errorf("help goes to stdout and starts with %q, got stdout %q", "usage: burrowd <command>", out)
			}
			if errOut != "" {
				t.Errorf("help shouldn't write to stderr, got %q", errOut)
			}
		})
	}
}

func TestL0_1_UnknownCommand(t *testing.T) {
	code, out, errOut := call("bogus")
	if code != 2 {
		t.Errorf("an unknown command should exit 2, got %d", code)
	}
	if out != "" {
		t.Errorf("errors go to stderr, but stdout got %q", out)
	}
	if !strings.Contains(errOut, `unknown command "bogus"`) {
		t.Errorf("stderr should contain %q, got %q", `unknown command "bogus"`, errOut)
	}
	if !strings.Contains(errOut, "usage: burrowd <command>") {
		t.Errorf("an unknown command should also print usage, got %q", errOut)
	}
}

func TestL0_1_UnknownFlag(t *testing.T) {
	code, _, errOut := call("--frobnicate")
	if code != 2 {
		t.Errorf("an unknown flag should exit 2, got %d", code)
	}
	if !strings.Contains(errOut, "frobnicate") {
		t.Errorf("the error should name the bad flag, got %q", errOut)
	}
}

// TestL0_1_BinaryExitCodes builds the real binary, so it checks that main()
// actually calls run() and passes its result to os.Exit.
func TestL0_1_BinaryExitCodes(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary")
	}
	bin := filepath.Join(t.TempDir(), "burrowd")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}

	out, err := exec.Command(bin, "version").Output()
	if err != nil {
		t.Fatalf("running `burrowd version`: %v", err)
	}
	if string(out) != "burrowd 0.1.0\n" {
		t.Errorf("binary printed %q for `version`", out)
	}

	err = exec.Command(bin, "bogus").Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 2 {
		t.Errorf("binary should exit with status 2 for an unknown command, got %v. "+
			"Does main() call os.Exit(run(...))?", err)
	}
}

func TestL0_1_Gofmt(t *testing.T) {
	gofmt, err := exec.LookPath("gofmt")
	if err != nil {
		t.Skip("gofmt not on PATH")
	}
	root, _ := filepath.Abs(filepath.Join("..", ".."))
	out, err := exec.Command(gofmt, "-l", root).CombinedOutput()
	if err != nil {
		t.Fatalf("gofmt: %v\n%s", err, out)
	}
	var bad []string
	for _, f := range strings.Fields(string(out)) {
		rel, _ := filepath.Rel(root, f)
		if strings.HasPrefix(rel, "curriculum"+string(os.PathSeparator)) {
			continue
		}
		bad = append(bad, rel)
	}
	if len(bad) > 0 {
		t.Errorf("these files aren't gofmt-formatted (run `gofmt -w .`):\n  %s", strings.Join(bad, "\n  "))
	}
}
