package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

func cmdPrompt(w *world, args []string) error {
	if len(args) == 0 {
		return errors.New("which prompt? `burrow prompt tests`, `burrow prompt hint` or `burrow prompt review`")
	}
	kind := args[0]
	l, err := w.resolve(args[1:])
	if err != nil {
		return err
	}
	readme, _ := os.ReadFile(filepath.Join(w.cur.levelDir(l), "README.md"))
	var b strings.Builder
	head := fmt.Sprintf("Building Burrow · Level %s %s (%s: %s)", l.ID, l.Title, chapterLabel(l.ch), l.ch.Name)
	switch kind {
	case "tests":
		fmt.Fprintf(&b, "%s\n\n", head)
		b.WriteString("Write the hidden test suite for this level of my Building Burrow repo. I code the implementation by hand; you write tests only, never implementation.\n\n")
		b.WriteString("Conventions (match the existing suites in curriculum/levels/0.1 to 7.2, H.1 to H.3, M.1 and G.1 to G.6):\n")
		fmt.Fprintf(&b, "- Put test files in curriculum/levels/%s/files/<package dir>/l%s_<topic>_test.go, mirroring where they'll be installed in the repo.\n", l.ID, strings.ToLower(strings.ReplaceAll(l.ID, ".", "_")))
		fmt.Fprintf(&b, "- Every test function is named Test%s<Name> (or Fuzz%s<Name>) so `burrow test` can select it.\n", testPrefix(l), testPrefix(l))
		b.WriteString("- Standard library only. Use burrow/internal/testkit (Eventually, Within, NoLeaks, FakeClock, Listen, RequireRace).\n")
		b.WriteString("- If the README asks me to write tests, fuzz targets or benchmarks of my own, grade them with testkit.CheckYourTests (mutants that wrap my functions, as in 0.2, 4.3 and 7.1), YourFuzzTargets/RunFuzz or YourBenchmarks/RunBenchmark.\n")
		b.WriteString("- Make them hard: boundaries, concurrency under -race, timeouts, leaks, malformed input, crash/restart. Failure messages explain the expectation and hint at the concept.\n")
		b.WriteString("- Pin down the exact API (package, types, signatures) in the level's README.md under '## Build', and list what the tests check under '## The tests check'.\n")
		b.WriteString("- Earlier levels' tests must keep passing; don't change APIs they rely on.\n")
		b.WriteString("- Respect the part graph: the level may only rely on parts listed under '## Uses' in its README (curriculum.json `consumes`).\n")
		b.WriteString("- Validate the suite against a private reference solution and a couple of broken ones before handing it over, and add 3 progressive hints to curriculum.json.\n\n")
		b.WriteString("Current README:\n\n")
		b.Write(readme)
	case "hint":
		fmt.Fprintf(&b, "%s\n\n", head)
		b.WriteString("I'm stuck. Give me one nudge toward the next step, not the solution. Point at the concept or the line that's wrong; don't rewrite my code.\n\n")
		b.WriteString("The level:\n\n")
		b.Write(readme)
		if hints := w.prog.Hints[l.ID]; hints > 0 {
			fmt.Fprintf(&b, "\nHints I've already seen:\n")
			for _, h := range l.Hints[:min(hints, len(l.Hints))] {
				fmt.Fprintf(&b, "- %s\n", h)
			}
		}
		if last, err := os.ReadFile(filepath.Join(w.cur.root, ".burrow", "last-run.txt")); err == nil && len(last) > 0 {
			fmt.Fprintf(&b, "\nMy latest failing tests:\n\n```\n%s```\n", truncateBytes(string(last), 6000))
		}
		writeCode(&b, w, l)
	case "review":
		fmt.Fprintf(&b, "%s\n\n", head)
		b.WriteString("My tests pass. Review my code for idiomatic Go, hidden bugs, and anything that will hurt me in later levels. Don't rewrite it; point at specific lines and explain why.\n\n")
		b.WriteString("The level:\n\n")
		b.Write(readme)
		writeCode(&b, w, l)
	default:
		return fmt.Errorf("unknown prompt %q: use tests, hint or review", kind)
	}
	text := b.String()
	if copyToClipboard(text) {
		fmt.Println("\n  " + green(gDone) + " Copied the " + bold(kind) + " prompt for " + l.ID + gray(fmt.Sprintf(" (%d lines)", strings.Count(text, "\n"))) + ". Paste it into your chat with Claude.\n")
		return nil
	}
	fmt.Println(text)
	return nil
}

func truncateBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n… (truncated)\n"
}

func writeCode(b *strings.Builder, w *world, l *Level) {
	var files []string
	for _, pkg := range w.cur.packages(l) {
		dir := filepath.Join(w.cur.root, strings.TrimPrefix(pkg, "./"))
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			n := e.Name()
			if strings.HasSuffix(n, ".go") && !strings.HasSuffix(n, "_test.go") {
				files = append(files, filepath.Join(dir, n))
			}
		}
	}
	sort.Strings(files)
	if len(files) == 0 {
		b.WriteString("\nMy code: (paste the relevant files here)\n")
		return
	}
	b.WriteString("\nMy code:\n")
	total := 0
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		rel, _ := filepath.Rel(w.cur.root, f)
		if total > 40_000 {
			fmt.Fprintf(b, "\n(%s omitted for length)\n", rel)
			continue
		}
		total += len(src)
		fmt.Fprintf(b, "\n%s:\n```go\n%s```\n", rel, src)
	}
}

func copyToClipboard(s string) bool {
	if !isTTY {
		return false
	}
	var cands [][]string
	switch runtime.GOOS {
	case "darwin":
		cands = [][]string{{"pbcopy"}}
	default:
		cands = [][]string{{"wl-copy"}, {"xclip", "-selection", "clipboard"}, {"xsel", "--clipboard", "--input"}}
	}
	for _, c := range cands {
		if _, err := exec.LookPath(c[0]); err != nil {
			continue
		}
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Stdin = strings.NewReader(s)
		if cmd.Run() == nil {
			return true
		}
	}
	return false
}
