package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

type event struct {
	Action     string
	Package    string
	ImportPath string
	Test       string
	Output     string
	Elapsed    float64
}

type result struct {
	pkg     string
	name    string // full test name
	status  string // run, pass, fail, skip
	elapsed float64
	output  []string
	subs    []*result
}

type runSummary struct {
	pass, fail, skip int
	buildFailed      bool
	results          []*result // top-level, in finish order
	buildOutput      map[string][]string
	pkgOutput        map[string][]string
	pkgFailed        map[string]bool
	elapsed          time.Duration
	cancelled        bool
	raceOff          bool
}

func (s *runSummary) ok() bool {
	return !s.buildFailed && !s.cancelled && s.fail == 0 && s.pass > 0 && len(s.failedPackages()) == 0
}

func (s *runSummary) failedPackages() []string {
	var out []string
	for p := range s.pkgFailed {
		hasFailedTest := false
		for _, r := range s.results {
			if r.pkg == p && r.status == "fail" {
				hasFailedTest = true
			}
		}
		if !hasFailedTest {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

var cgoChecked, cgoOK bool

func raceAvailable(root string) bool {
	if cgoChecked {
		return cgoOK
	}
	cgoChecked = true
	cmd := exec.Command("go", "env", "CGO_ENABLED")
	cmd.Dir = root
	out, err := cmd.Output()
	cgoOK = err == nil && strings.TrimSpace(string(out)) == "1"
	return cgoOK
}

type runOpts struct {
	levels  []*Level
	quiet   bool // no per-test lines (used by done --all summaries)
	title   string
	verbose bool
}

func newSummary() *runSummary {
	return &runSummary{buildOutput: map[string][]string{}, pkgOutput: map[string][]string{}, pkgFailed: map[string]bool{}}
}

// runTests runs the tests for the given levels and renders them live. Levels
// in a nested module (chapter G's grpc/) run with that module as the working
// directory, one go test per module.
func runTests(ctx context.Context, w *world, o runOpts) *runSummary {
	var modules []string
	byModule := map[string][]*Level{}
	for _, l := range o.levels {
		if _, ok := byModule[l.Module]; !ok {
			modules = append(modules, l.Module)
		}
		byModule[l.Module] = append(byModule[l.Module], l)
	}
	if len(modules) == 1 {
		return runModuleTests(ctx, w, o, modules[0], byModule[modules[0]])
	}
	sum := newSummary()
	for _, m := range modules {
		s := runModuleTests(ctx, w, o, m, byModule[m])
		sum.merge(s)
		if s.cancelled {
			break
		}
	}
	return sum
}

func (s *runSummary) merge(o *runSummary) {
	s.pass += o.pass
	s.fail += o.fail
	s.skip += o.skip
	s.buildFailed = s.buildFailed || o.buildFailed
	s.cancelled = s.cancelled || o.cancelled
	s.raceOff = s.raceOff || o.raceOff
	s.elapsed += o.elapsed
	s.results = append(s.results, o.results...)
	for k, v := range o.buildOutput {
		s.buildOutput[k] = append(s.buildOutput[k], v...)
	}
	for k, v := range o.pkgOutput {
		s.pkgOutput[k] = append(s.pkgOutput[k], v...)
	}
	for k, v := range o.pkgFailed {
		s.pkgFailed[k] = s.pkgFailed[k] || v
	}
}

func runModuleTests(ctx context.Context, w *world, o runOpts, module string, levels []*Level) *runSummary {
	sum := newSummary()
	dir := filepath.Join(w.cur.root, filepath.FromSlash(module))
	var pkgs []string
	seen := map[string]bool{}
	var prefixes []string
	for _, l := range levels {
		prefixes = append(prefixes, testPrefix(l))
		for _, p := range w.cur.packages(l) {
			if module != "" {
				p = "./" + strings.TrimPrefix(strings.TrimPrefix(p, "./"), module+"/")
			}
			if !seen[p] {
				seen[p] = true
				pkgs = append(pkgs, p)
			}
		}
	}
	if len(pkgs) == 0 {
		fmt.Println(yellow("  No tests for this level yet.") + " Run " + bold("burrow prompt tests") + " and paste it to Claude.")
		return sum
	}
	if module != "" {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
			fmt.Println(yellow("  No "+module+"/go.mod yet.") + gray(" This level's code lives in its own module: create it as the level's page describes."))
			sum.buildFailed = true
			sum.buildOutput[module] = []string{"no go.mod in " + module + "/\n"}
			return sum
		}
	}
	pattern := "^(Test|Fuzz)(" + strings.Join(prefixes, "|") + ")"
	args := []string{"test", "-json", "-count=1", "-timeout=180s"}
	if raceAvailable(w.cur.root) {
		args = append(args, "-race")
	} else {
		sum.raceOff = true
	}
	args = append(args, "-run", pattern)
	args = append(args, pkgs...)

	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	stdout, _ := cmd.StdoutPipe()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	start := time.Now()
	rv := newRunView(o.quiet)
	testing := false

	if err := cmd.Start(); err != nil {
		rv.done(false)
		fmt.Println(red("  could not run go test: ") + err.Error())
		sum.buildFailed = true
		return sum
	}

	byKey := map[string]*result{}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1<<20), 8<<20)
	for sc.Scan() {
		var ev event
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			continue
		}
		switch {
		case ev.Action == "build-output":
			sum.buildOutput[ev.ImportPath] = append(sum.buildOutput[ev.ImportPath], ev.Output)
		case ev.Action == "build-fail":
			sum.buildFailed = true
		case ev.Test == "":
			if ev.Action == "output" {
				sum.pkgOutput[ev.Package] = append(sum.pkgOutput[ev.Package], ev.Output)
			}
			if ev.Action == "fail" {
				sum.pkgFailed[ev.Package] = true
				if strings.Contains(strings.Join(sum.pkgOutput[ev.Package], ""), "[setup failed]") ||
					strings.Contains(strings.Join(sum.pkgOutput[ev.Package], ""), "[build failed]") {
					sum.buildFailed = true
				}
			}
			if ev.Action == "start" && !testing {
				testing = true
				rv.phase("Testing")
			}
		default:
			if !testing {
				testing = true
				rv.phase("Testing")
			}
			key := ev.Package + "\x00" + ev.Test
			r := byKey[key]
			if r == nil {
				r = &result{pkg: ev.Package, name: ev.Test, status: "run"}
				byKey[key] = r
				if parent, _, ok := cutLast(ev.Test, "/"); ok {
					if p := byKey[ev.Package+"\x00"+parent]; p != nil {
						p.subs = append(p.subs, r)
					}
				}
			}
			switch ev.Action {
			case "run":
				if !strings.Contains(ev.Test, "/") {
					rv.start(key, ev.Test)
				}
			case "output":
				if keepOutput(ev.Output) {
					r.output = append(r.output, ev.Output)
				}
			case "pass", "fail", "skip":
				r.status, r.elapsed = ev.Action, ev.Elapsed
				if !strings.Contains(ev.Test, "/") {
					switch ev.Action {
					case "pass":
						sum.pass++
					case "fail":
						sum.fail++
					case "skip":
						sum.skip++
					}
					sum.results = append(sum.results, r)
					rv.finish(key, ev.Action, renderResultLine(r))
				}
			}
		}
	}
	err := cmd.Wait()
	rv.done(err == nil && !sum.buildFailed && sum.fail == 0 && ctx.Err() == nil)
	sum.elapsed = time.Since(start)
	if ctx.Err() != nil {
		sum.cancelled = true
		return sum
	}
	if err != nil && sum.pass+sum.fail+sum.skip == 0 && len(sum.buildOutput) == 0 && stderr.Len() > 0 {
		sum.buildFailed = true
		sum.buildOutput["go"] = strings.SplitAfter(stderr.String(), "\n")
	}
	return sum
}

func ifs(c bool, s string) string {
	if c {
		return s
	}
	return ""
}

func cutLast(s, sep string) (before, after string, ok bool) {
	i := strings.LastIndex(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
}

var noiseRE = regexp.MustCompile(`^\s*(=== (RUN|PAUSE|CONT|NAME)|--- (PASS|FAIL|SKIP)|PASS$|FAIL$|ok\s|coverage:)`)

func keepOutput(s string) bool { return !noiseRE.MatchString(s) }

// shortName turns TestL1_1_GetMissing into GetMissing.
var prefixRE = regexp.MustCompile(`^(Test|Fuzz)L[A-Z0-9]+_\d+_`)

func shortName(full string) string {
	parts := strings.Split(full, "/")
	return prefixRE.ReplaceAllString(parts[len(parts)-1], "")
}

// humanize turns "CleanEOFOnClose" into "Clean EOF on close".
func humanize(s string) string {
	if strings.ContainsAny(s, " _") {
		return strings.ReplaceAll(s, "_", " ")
	}
	rs := []rune(s)
	var words []string
	start := 0
	for i := 1; i < len(rs); i++ {
		prev, cur := rs[i-1], rs[i]
		next := rune(0)
		if i+1 < len(rs) {
			next = rs[i+1]
		}
		boundary := (unicode.IsLower(prev) && unicode.IsUpper(cur)) ||
			(unicode.IsUpper(prev) && unicode.IsUpper(cur) && unicode.IsLower(next)) ||
			(unicode.IsLetter(prev) && unicode.IsDigit(cur) && !(i >= 2 && unicode.IsUpper(rs[i-2]) && unicode.IsUpper(prev)))
		if boundary {
			words = append(words, string(rs[start:i]))
			start = i
		}
	}
	words = append(words, string(rs[start:]))
	for i, w := range words {
		if i > 0 && !isAcronym(w) {
			words[i] = strings.ToLower(w)
		}
	}
	return strings.Join(words, " ")
}

func isAcronym(w string) bool {
	up := 0
	for _, r := range w {
		if unicode.IsUpper(r) || unicode.IsDigit(r) {
			up++
		}
	}
	return up >= 2
}

func pkgLabel(p string) string {
	if i := strings.Index(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func renderResultLine(r *result) string {
	name := humanize(shortName(r.name))
	if strings.HasPrefix(shortName(r.name), "z") && r.status == "skip" {
		name = strings.TrimPrefix(name, "z") + " (helper)"
	}
	var glyph, label string
	switch r.status {
	case "pass":
		glyph, label = green(gDone), name
	case "fail":
		glyph, label = red(gFail), bold(name)
	default:
		glyph, label = gray(gSkip), gray(name)
	}
	extra := ""
	if n := len(r.subs); n > 0 {
		failed := 0
		for _, s := range r.subs {
			if s.status == "fail" {
				failed++
			}
		}
		if failed > 0 {
			extra = red(fmt.Sprintf(" %d/%d cases failed", failed, n))
		} else {
			extra = gray(fmt.Sprintf(" %d cases", n))
		}
	}
	pkg := faint(pkgLabel(r.pkg))
	el := gray(fmt.Sprintf("%.2fs", r.elapsed))
	left := "  " + glyph + " " + label + extra
	right := pkg + "  " + el
	gap := termWidth - visibleLen(left) - visibleLen(right) - 2
	if gap < 2 {
		return left + "  " + right
	}
	return left + strings.Repeat(" ", gap) + right
}

var locRE = regexp.MustCompile(`^\s*([\w./-]+_test\.go:\d+|[\w./-]+\.go:\d+): ?(.*)$`)

// renderFailures prints the details of every failed test.
func renderFailures(sum *runSummary, root string) {
	if sum.buildFailed {
		renderBuildFailure(sum, root)
		return
	}
	var failed []*result
	for _, r := range sum.results {
		if r.status == "fail" {
			failed = append(failed, r)
		}
	}
	for _, p := range sum.failedPackages() {
		fmt.Println()
		fmt.Println("  " + red(gFail+" "+pkgLabel(p)) + gray("  the package failed outside any single test"))
		printOutput(sum.pkgOutput[p], 30)
	}
	if len(failed) == 0 {
		return
	}
	fmt.Println()
	fmt.Println(rule(red("failures")))
	for _, r := range failed {
		fmt.Println()
		fmt.Println("  " + red(gFail) + " " + bold(humanize(shortName(r.name))) + gray("  "+pkgLabel(r.pkg)+" · "+r.name))
		printOutput(r.output, 24)
		for _, s := range r.subs {
			if s.status != "fail" {
				continue
			}
			fmt.Println("    " + red("↳ ") + bold(strings.TrimPrefix(s.name, r.name+"/")))
			printOutputIndent(s.output, 12, "      ")
		}
	}
}

func printOutput(lines []string, limit int) { printOutputIndent(lines, limit, "    ") }

func printOutputIndent(lines []string, limit int, indent string) {
	shown := 0
	width := termWidth - len(indent) - 2
	for _, raw := range lines {
		for _, l := range strings.Split(strings.TrimRight(raw, "\n"), "\n") {
			if strings.TrimSpace(l) == "" {
				continue
			}
			if shown >= limit {
				fmt.Println(indent + gray(fmt.Sprintf("… %d more lines (run with -v for everything)", countLines(lines)-limit)))
				return
			}
			shown++
			if m := locRE.FindStringSubmatch(l); m != nil {
				fmt.Println(indent + faint(m[1]))
				for _, wl := range wrapVisible(m[2], width) {
					fmt.Println(indent + wl)
				}
				continue
			}
			fmt.Println(indent + truncate(strings.TrimPrefix(strings.TrimPrefix(l, "    "), "    "), width+40))
		}
	}
}

func countLines(lines []string) int {
	n := 0
	for _, l := range lines {
		n += strings.Count(strings.TrimRight(l, "\n"), "\n") + 1
	}
	return n
}

var compileErrRE = regexp.MustCompile(`^(\S+\.go):(\d+):(\d+): (.*)$`)

func renderBuildFailure(sum *runSummary, root string) {
	fmt.Println()
	fmt.Println(rule(red("doesn't compile yet")))
	var all []string
	for _, lines := range sum.buildOutput {
		all = append(all, lines...)
	}
	for p, lines := range sum.pkgOutput {
		if sum.pkgFailed[p] {
			all = append(all, lines...)
		}
	}
	text := strings.Join(all, "")
	shown := 0
	undefined := false
	noFiles := ""
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") || strings.HasPrefix(l, "FAIL") {
			continue
		}
		if strings.Contains(l, "no non-test Go files") {
			noFiles = l
			continue
		}
		if shown >= 14 {
			continue
		}
		shown++
		if m := compileErrRE.FindStringSubmatch(l); m != nil {
			rel := m[1]
			if r, err := filepath.Rel(root, m[1]); err == nil && !strings.HasPrefix(r, "..") {
				rel = r
			}
			if strings.Contains(m[4], "undefined") || strings.Contains(m[4], "has no field or method") {
				undefined = true
			}
			fmt.Println("    " + faint(rel+":"+m[2]))
			fmt.Println("    " + m[4])
			continue
		}
		fmt.Println("    " + l)
	}
	if noFiles != "" {
		fmt.Println("    " + yellow("A package has tests but no code yet: ") + gray(noFiles))
		fmt.Println("    Create a .go file in that folder to get started.")
	}
	if undefined {
		fmt.Println()
		fmt.Println("  " + cyan("→") + " The tests call API you haven't written yet. " + bold("burrow show") + " lists the exact signatures.")
	}
}

// renderSummary prints the bar and counts after a run.
func renderSummary(sum *runSummary) {
	total := sum.pass + sum.fail + sum.skip
	var states []string
	for _, r := range sum.results {
		states = append(states, r.status)
	}
	fmt.Println()
	if sum.buildFailed {
		fmt.Println("  " + red("■ build failed") + gray("  · "+duration(sum.elapsed)))
		return
	}
	width := min(40, max(10, termWidth-50))
	var b string
	if total <= width && total > 0 {
		b = segBar(states)
	} else {
		b = bar(sum.pass, total, width, green)
	}
	line := "  " + b + "  " + bold(fmt.Sprintf("%d/%d", sum.pass, total-sum.skip)) + gray(" passing")
	if sum.fail > 0 {
		line += gray(" · ") + red(fmt.Sprintf("%d failing", sum.fail))
	}
	if sum.skip > 0 {
		line += gray(fmt.Sprintf(" · %d skipped", sum.skip))
	}
	line += gray(" · " + duration(sum.elapsed))
	fmt.Println(line)
	if sum.raceOff {
		fmt.Println("  " + yellow("!") + gray(" race detector off: cgo isn't available. On macOS run `xcode-select --install`."))
	}
}

// saveLastRun keeps a plain-text record for `burrow prompt hint`.
func saveLastRun(root string, sum *runSummary) {
	var b strings.Builder
	for _, r := range sum.results {
		if r.status != "fail" {
			continue
		}
		fmt.Fprintf(&b, "--- FAIL: %s\n", r.name)
		for _, o := range r.output {
			b.WriteString(o)
		}
		for _, s := range r.subs {
			if s.status == "fail" {
				fmt.Fprintf(&b, "  --- FAIL: %s\n", s.name)
				for _, o := range s.output {
					b.WriteString(o)
				}
			}
		}
	}
	if sum.buildFailed {
		for _, lines := range sum.buildOutput {
			b.WriteString(strings.Join(lines, ""))
		}
	}
	os.MkdirAll(filepath.Join(root, ".burrow"), 0o755)
	os.WriteFile(filepath.Join(root, ".burrow", "last-run.txt"), []byte(b.String()), 0o644)
}
