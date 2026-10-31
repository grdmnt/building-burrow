// Command burrow runs the Building Burrow curriculum from the terminal.
package main

import (
	"burrowtool/tui"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"

	"strings"
	"syscall"
	"time"
)

const helpText = `burrow · learn Go by building a distributed key-value store

  burrow                  the full-screen browser: levels, pages and the machine
  burrow status           dashboard: progress and what to do next
  burrow machine          Burrow as a machine: every part you've built, lit up
  burrow map              the chapter tech tree
  burrow list [chapter]   every level and its state
  burrow parts            the parts you've built and where each one plugs in
  burrow show [level]     the level's spec (defaults to the current level)

  burrow start <level>    unlock a level: installs its tests into your code
  burrow next             start the next available level
  burrow watch            re-run the tests whenever you save  (h hint · r rerun · d done · q quit)
  burrow test [level]     run a level's tests once   (--all: every level you've started)
  burrow hint             reveal the next hint for the current level
  burrow done             verify the tests pass and complete the level

  burrow prompt <tests|hint|review>   copy a prompt for Claude (tests for unwritten levels,
                                      a nudge with your code and failures, or a code review)
  burrow restore [level]  put back a level's test files if you changed them

  Colours follow your terminal's background. To force them, set
  BURROW_THEME=light or BURROW_THEME=dark.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		showCursor()
		fmt.Fprintln(os.Stderr, red("✘ ")+err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	if useColor {
		tui.SetLight(tui.DetectLight())
	}
	cmd := ""
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	if cmd == "help" || cmd == "-h" || cmd == "--help" {
		fmt.Print(helpText)
		return nil
	}
	root, err := findRoot()
	if err != nil {
		return err
	}
	cur, err := loadCurriculum(root)
	if err != nil {
		return err
	}
	prog, err := loadProgress(root)
	if err != nil {
		return err
	}
	w := &world{cur: cur, prog: prog}
	switch cmd {
	case "":
		if isTTY && os.Getenv("TERM") != "dumb" {
			return cmdBrowse(w)
		}
		return cmdStatus(w)
	case "status":
		return cmdStatus(w)
	case "browse", "ui":
		return cmdBrowse(w)
	case "machine", "m":
		return cmdMachine(w)
	case "map", "tree":
		return cmdMap(w)
	case "list", "ls":
		return cmdList(w, args)
	case "parts", "inventory":
		return cmdParts(w, args)
	case "show":
		return cmdShow(w, args)
	case "start":
		return cmdStart(w, args)
	case "next":
		return cmdNext(w)
	case "test":
		return cmdTest(w, args)
	case "watch":
		return cmdWatch(w, args)
	case "hint":
		return cmdHint(w, args)
	case "done":
		return cmdDone(w, args)
	case "prompt":
		return cmdPrompt(w, args)
	case "restore":
		return cmdRestore(w, args)
	default:
		return fmt.Errorf("unknown command %q. Run `burrow help`", cmd)
	}
}

// ---------- status ----------

func logo() string {
	return bold(cyan("▞▚ ")) + bold("BUILDING BURROW") + gray("  ·  Go, from the standard library up")
}

func cmdStatus(w *world) error {
	fmt.Println()
	fmt.Println("  " + logo())
	fmt.Println()
	done, total := len(w.prog.Completed), len(w.cur.flat)
	chDone := 0
	for _, ch := range w.cur.Chapters {
		if w.chapterDone(ch) {
			chDone++
		}
	}
	width := min(36, termWidth-40)
	fmt.Printf("  %s  %s %s   %s\n\n", bar(done, total, width, cyan), bold(fmt.Sprintf("%d/%d", done, total)), gray("levels"),
		gray(fmt.Sprintf("%d/%d chapters", chDone, len(w.cur.Chapters))))

	if l, ok := w.cur.byID[w.prog.Current]; ok && !w.levelDone(l) {
		lines := []string{
			gray(chapterLabel(l.ch)+" · "+l.ch.Name) + ifs(l.ch.Optional, amber(" · optional")),
			"",
			inline(l.Brief),
			"",
			gray("builds ") + amber(l.Unlock) + "   " + pips(l.Difficulty),
			gray("uses   ") + ifs(len(l.Consumes) == 0, gray("nothing yet: this is a fresh start")) + usesLine(w, l),
			gray("Hints revealed: ") + fmt.Sprintf("%d/%d", w.prog.Hints[l.ID], len(l.Hints)) +
				ifs(!w.cur.hasTests(l), "   "+amber("tests not written yet")),
		}
		fmt.Print(box(bold("▶ "+l.ID+" "+l.Title), lines, min(termWidth-2, 78), cyan))
		fmt.Println()
		fmt.Println("  " + bold("burrow watch") + gray("   test on every save") + "     " + bold("burrow show") + gray("   the full spec"))
		fmt.Println("  " + bold("burrow hint") + gray("    a nudge") + "                " + bold("burrow done") + gray("   finish the level"))
	} else {
		av := w.available()
		if len(av) == 0 {
			fmt.Println("  " + green("★ Every level is complete. You built a distributed database."))
			return nil
		}
		fmt.Println("  " + bold("Ready to start:"))
		for _, l := range av {
			fmt.Println("    " + cyan(gOpen) + " " + bold(l.ID) + " " + l.Title + gray("  · "+l.ch.Name) + ifs(l.ch.Optional, amber(" (optional)")))
		}
		fmt.Println()
		fmt.Println("  " + bold("burrow next") + gray(" or ") + bold("burrow start "+av[0].ID))
	}
	fmt.Println()
	return nil
}

// ---------- list ----------

func stateGlyph(s state) string {
	switch s {
	case stDone:
		return green(gDone)
	case stCurrent:
		return cyan(gCurrent)
	case stOpen:
		return bold(gOpen)
	}
	return faint(gLocked)
}

func cmdList(w *world, args []string) error {
	fmt.Println()
	for _, ch := range w.cur.Chapters {
		if len(args) > 0 && args[0] != ch.Key && !strings.EqualFold(args[0], ch.Name) {
			continue
		}
		cs := w.chapterState(ch)
		n := 0
		for _, l := range ch.Levels {
			if w.levelDone(l) {
				n++
			}
		}
		name := bold(ch.Name)
		if cs == stLocked {
			name = gray(ch.Name)
		}
		head := "  " + stateGlyph(cs) + " " + gray(padRight(chapterLabel(ch), 7)) + name +
			ifs(ch.Optional, amber("  optional")) + "  " + bar(n, len(ch.Levels), len(ch.Levels), green)
		if cs == stLocked {
			var need []string
			for _, k := range ch.Needs {
				if !w.chapterDone(w.cur.byKey[k]) {
					need = append(need, w.cur.byKey[k].Name)
				}
			}
			head += gray("  needs " + strings.Join(need, " + "))
		}
		fmt.Println(head)
		for _, l := range ch.Levels {
			s := w.levelState(l)
			title := l.Title
			switch s {
			case stLocked:
				title = gray(title)
			case stCurrent:
				title = bold(cyan(title))
			}
			tag := ""
			if !w.cur.hasTests(l) {
				tag = faint("  tests pending")
			}
			if s == stDone {
				tag = faint("  → ") + amber(l.Unlock)
			}
			fmt.Println("      " + stateGlyph(s) + " " + gray(padRight(l.ID, 5)) + pips(l.Difficulty) + " " + title + tag)
		}
		fmt.Println()
	}
	fmt.Println("  " + gray(green(gDone)+" done  "+cyan(gCurrent)+" in progress  "+bold(gOpen)+" available  "+faint(gLocked)+" locked"))
	fmt.Println()
	return nil
}

// ---------- show ----------

func cmdShow(w *world, args []string) error {
	l, err := w.resolve(args)
	if err != nil {
		return err
	}
	src, err := os.ReadFile(w.cur.levelDir(l) + "/README.md")
	if err != nil {
		return err
	}
	s := w.levelState(l)
	var status string
	switch s {
	case stDone:
		status = green("complete")
	case stCurrent:
		status = cyan("in progress")
	case stOpen:
		status = "available"
	default:
		status = gray("locked: " + w.lockReason(l))
	}
	head := "\n  " + gray(chapterLabel(l.ch)+" · "+l.ch.Name) + gray("  ·  ") + status + gray("  ·  ") + pips(l.Difficulty) +
		"\n  " + gray("builds ") + amber(bold(l.Unlock))
	body := levelBody(w, l, string(src), termWidth-4)
	foot := "\n" + rule("") + "\n  " + gray("Hints: ") + fmt.Sprintf("%d/%d revealed", w.prog.Hints[l.ID], len(l.Hints)) + gray("  ·  burrow hint") + "\n"
	page(head + "\n\n" + body + "\n" + foot)
	return nil
}

// levelBody renders a level's README and draws the how-it-fits diagram under
// its "What you're using from before" heading.
func levelBody(w *world, l *Level, src string, width int) string {
	body := renderMarkdownW(src, width)
	diagram := fitDiagram(w, l, width)
	if len(diagram) == 0 {
		return body
	}
	lines := strings.Split(body, "\n")
	at := -1
	for i, ln := range lines {
		if strings.Contains(ansiRE.ReplaceAllString(ln, ""), "WHAT YOU'RE USING FROM BEFORE") {
			at = i + 1
			break
		}
	}
	block := []string{""}
	for _, d := range diagram {
		block = append(block, "  "+d)
	}
	if at < 0 {
		return strings.Join(block, "\n") + "\n\n" + body
	}
	out := append([]string{}, lines[:at]...)
	out = append(out, block...)
	out = append(out, lines[at:]...)
	return strings.Join(out, "\n")
}

// page shows long output through less when it won't fit the screen.
func page(s string) {
	if isTTY && strings.Count(s, "\n") > 45 {
		if lessPath, err := exec.LookPath("less"); err == nil {
			cmd := exec.Command(lessPath, "-R", "-F", "-X")
			cmd.Stdin = strings.NewReader(s)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if cmd.Run() == nil {
				return
			}
		}
	}
	fmt.Println(s)
}

// ---------- start / next ----------

func cmdStart(w *world, args []string) error {
	force := false
	var rest []string
	for _, a := range args {
		if a == "--force" || a == "-f" {
			force = true
		} else {
			rest = append(rest, a)
		}
	}
	if len(rest) == 0 {
		return errors.New("which level? e.g. `burrow start 1.1` (see `burrow list`)")
	}
	l, err := w.resolve(rest)
	if err != nil {
		return err
	}
	return startLevel(w, l, force)
}

func startLevel(w *world, l *Level, force bool) error {
	if s := w.levelState(l); s == stLocked && !force {
		return fmt.Errorf("%s %s is locked (%s). Use --force to start it anyway", l.ID, l.Title, w.lockReason(l))
	}
	fmt.Println()
	lv := startLive(func(f int) string { return "  " + cyan(spinFrames[f%len(spinFrames)]) + " Unlocking " + bold(l.ID) })
	if animate {
		time.Sleep(350 * time.Millisecond)
	}
	added, updated, err := w.cur.install(l)
	lv.Stop()
	if err != nil {
		return err
	}
	if _, ok := w.prog.Started[l.ID]; !ok {
		w.prog.Started[l.ID] = time.Now()
	}
	w.prog.Current = l.ID
	if err := w.prog.save(); err != nil {
		return err
	}
	typeOut("  " + cyan(gCurrent) + " " + bold(l.ID+" · "+l.Title) + gray("   "+chapterLabel(l.ch)+" · "+l.ch.Name))
	fmt.Println()
	for _, line := range wrapVisible(inline(l.Brief), termWidth-6) {
		fmt.Println("    " + line)
	}
	fmt.Println()
	fmt.Println("    " + gray("builds ") + amber(bold(l.Unlock)) + "   " + pips(l.Difficulty))
	if u := usesLine(w, l); u != "" {
		fmt.Println("    " + gray("uses   ") + u)
	}
	fmt.Println()
	for _, f := range added {
		fmt.Println("    " + green("+ ") + f)
		if animate {
			time.Sleep(40 * time.Millisecond)
		}
	}
	for _, f := range updated {
		fmt.Println("    " + amber("~ ") + f + gray("  (restored)"))
	}
	if !w.cur.hasTests(l) {
		fmt.Println("    " + amber("Tests for this level aren't written yet.") + " Run " + bold("burrow prompt tests") + " and paste it to Claude.")
	}
	fmt.Println()
	fmt.Println("  Next: " + bold("burrow show") + gray(" to read the spec, then ") + bold("burrow watch") + gray(" and start coding."))
	fmt.Println()
	return nil
}

func cmdNext(w *world) error {
	if l, ok := w.cur.byID[w.prog.Current]; ok && !w.levelDone(l) {
		fmt.Println("\n  You're on " + bold(l.ID+" "+l.Title) + ". Finish it with " + bold("burrow done") + gray(" (or start another with burrow start <level>).") + "\n")
		return nil
	}
	av := w.available()
	switch len(av) {
	case 0:
		fmt.Println("\n  " + green("★ Nothing left to start. Every level is complete.") + "\n")
		return nil
	case 1:
		return startLevel(w, av[0], false)
	}
	fmt.Println()
	fmt.Println("  " + bold("The path branches.") + gray(" Pick one:"))
	fmt.Println()
	for i, l := range av {
		fmt.Printf("    %s  %s %s%s%s\n", cyan(fmt.Sprintf("[%d]", i+1)), bold(l.ID), l.Title, gray("  · "+l.ch.Name), ifs(l.ch.Optional, amber(" (optional)")))
	}
	fmt.Println()
	fmt.Print("  Choice " + gray(fmt.Sprintf("(1-%d)", len(av))) + ": ")
	var n int
	if _, err := fmt.Scanln(&n); err != nil || n < 1 || n > len(av) {
		fmt.Println(gray("  Nothing started. Run `burrow start <level>` when you've decided."))
		return nil
	}
	return startLevel(w, av[n-1], false)
}

// ---------- test ----------

func cmdTest(w *world, args []string) error {
	all := false
	var rest []string
	for _, a := range args {
		if a == "--all" || a == "-a" {
			all = true
		} else {
			rest = append(rest, a)
		}
	}
	var levels []*Level
	title := ""
	if all {
		levels = w.installedLevels()
		if len(levels) == 0 {
			return errors.New("no levels started yet")
		}
		title = fmt.Sprintf("All %d started levels", len(levels))
	} else {
		l, err := w.resolve(rest)
		if err != nil {
			return err
		}
		levels = []*Level{l}
		title = l.ID + " · " + l.Title
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	hideCursor()
	defer showCursor()
	sum := testAndReport(ctx, w, levels, title)
	if !sum.ok() {
		return errors.New("not passing yet")
	}
	return nil
}

func header(title, sub string) {
	fmt.Println()
	fmt.Println("  " + bold(cyan("▞▚ ")) + bold(title) + ifs(sub != "", gray("   "+sub)))
	fmt.Println()
}

func testAndReport(ctx context.Context, w *world, levels []*Level, title string) *runSummary {
	sub := ""
	if len(levels) == 1 {
		sub = chapterLabel(levels[0].ch) + " · " + levels[0].ch.Name
	}
	header(title, sub)
	sum := runTests(ctx, w, runOpts{levels: levels})
	if sum.cancelled {
		return sum
	}
	renderFailures(sum, w.cur.root)
	renderSummary(sum)
	saveLastRun(w.cur.root, sum)
	if sum.ok() && len(levels) == 1 {
		l := levels[0]
		fmt.Println()
		sparkle(min(termWidth-2, 60), green)
		msg := []string{
			gray("produces ") + amber(l.Unlock),
		}
		if !w.levelDone(l) {
			msg = append(msg, gray("lock it in with ")+bold("burrow done"))
		}
		fmt.Print(box(green(bold("★ ALL GREEN"))+gray(" · "+l.ID+" "+l.Title), msg, min(termWidth-2, 60), green))
	} else if !sum.buildFailed && sum.fail > 0 {
		fmt.Println()
		fmt.Println("  " + gray("Stuck? ") + bold("burrow hint") + gray(" or ") + bold("burrow prompt hint"))
	}
	fmt.Println()
	return sum
}

// ---------- hint ----------

func cmdHint(w *world, args []string) error {
	l, err := w.resolve(args)
	if err != nil {
		return err
	}
	return revealHint(w, l)
}

func revealHint(w *world, l *Level) error {
	n := w.prog.Hints[l.ID]
	fmt.Println()
	if n >= len(l.Hints) {
		for i, h := range l.Hints {
			printHint(i, len(l.Hints), h, false)
		}
		fmt.Println()
		fmt.Println("  " + gray("That's every hint. For a nudge tailored to your code: ") + bold("burrow prompt hint"))
		fmt.Println()
		return nil
	}
	for i := 0; i < n; i++ {
		printHint(i, len(l.Hints), l.Hints[i], false)
	}
	printHint(n, len(l.Hints), l.Hints[n], true)
	w.prog.Hints[l.ID] = n + 1
	fmt.Println()
	return w.prog.save()
}

func printHint(i, total int, h string, fresh bool) {
	label := fmt.Sprintf("hint %d/%d", i+1, total)
	lines := wrapVisible(inline(h), termWidth-12)
	if fresh {
		fmt.Println("  " + amber("◆ "+label))
		for _, l := range lines {
			if animate {
				typeOut("    " + l)
			} else {
				fmt.Println("    " + l)
			}
		}
		return
	}
	fmt.Println("  " + gray("◇ "+label))
	for _, l := range lines {
		fmt.Println("    " + gray(ansiRE.ReplaceAllString(l, "")))
	}
}

// ---------- done ----------

func cmdDone(w *world, args []string) error {
	force := false
	var rest []string
	for _, a := range args {
		if a == "--force" {
			force = true
		} else {
			rest = append(rest, a)
		}
	}
	l, err := w.resolve(rest)
	if err != nil {
		return err
	}
	if w.levelDone(l) {
		fmt.Println("\n  " + green(gDone) + " " + l.ID + " " + l.Title + " is already complete.\n")
		return nil
	}
	if !w.cur.hasTests(l) {
		if !force {
			return fmt.Errorf("%s has no tests yet, so there's nothing to verify. Get them with `burrow prompt tests`, or mark it done anyway with `burrow done --force`", l.ID)
		}
		return complete(w, l)
	}
	if changed, missing := w.cur.tampered(l); len(changed)+len(missing) > 0 {
		fmt.Println()
		for _, f := range changed {
			fmt.Println("    " + amber("~ ") + f + gray("  differs from the curriculum's copy"))
		}
		for _, f := range missing {
			fmt.Println("    " + red("- ") + f + gray("  is missing"))
		}
		return fmt.Errorf("the level's provided files changed. Put them back with `burrow restore %s`", l.ID)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	hideCursor()
	sum := testAndReportQuietCelebration(ctx, w, l)
	showCursor()
	if !sum.ok() {
		return errors.New("not complete yet: every test must pass")
	}
	return complete(w, l)
}

func testAndReportQuietCelebration(ctx context.Context, w *world, l *Level) *runSummary {
	header("Verifying "+l.ID+" · "+l.Title, chapterLabel(l.ch)+" · "+l.ch.Name)
	sum := runTests(ctx, w, runOpts{levels: []*Level{l}})
	if !sum.cancelled {
		renderFailures(sum, w.cur.root)
		renderSummary(sum)
		saveLastRun(w.cur.root, sum)
	}
	return sum
}

func complete(w *world, l *Level) error {
	before := map[string]bool{}
	for _, x := range w.available() {
		before[x.ID] = true
	}
	chWasDone := w.chapterDone(l.ch)
	fmt.Println()
	switchOn(w, l)
	w.prog.Completed[l.ID] = time.Now()
	if w.prog.Current == l.ID {
		w.prog.Current = ""
	}
	if err := w.prog.save(); err != nil {
		return err
	}
	fmt.Println()
	sparkle(min(termWidth-2, 64), amber)
	lines := []string{gray("you built ") + amber(bold(l.Unlock))}
	if cons := w.cur.consumers(l.Unlock); len(cons) > 0 {
		var ids []string
		for _, c := range cons {
			ids = append(ids, c.ID+" "+c.Title)
		}
		lines = append(lines, gray("it plugs into: ")+strings.Join(ids, gray(", ")))
	}
	if !chWasDone && w.chapterDone(l.ch) {
		lines = append(lines, "", green(bold("Chapter complete: "+l.ch.Name)))
	}
	var unlocked []*Level
	for _, x := range w.available() {
		if !before[x.ID] {
			unlocked = append(unlocked, x)
		}
	}
	if len(unlocked) > 0 {
		lines = append(lines, "", gray("unlocked:"))
		for _, x := range unlocked {
			lines = append(lines, "  "+cyan(gOpen)+" "+bold(x.ID)+" "+x.Title+ifs(x.ch != l.ch, gray("  · "+x.ch.Name))+ifs(x.ch.Optional, amber(" (optional)")))
		}
	}
	fmt.Print(box(amber(bold("★ LEVEL COMPLETE"))+gray(" · "+l.ID+" "+l.Title), lines, min(termWidth-2, 64), amber))
	done, total := len(w.prog.Completed), len(w.cur.flat)
	fmt.Println("  " + bar(done, total, 30, cyan) + "  " + gray(fmt.Sprintf("%d/%d levels", done, total)))
	fmt.Println()
	fmt.Println("  " + gray("Next: ") + bold("burrow next"))
	fmt.Println()
	return nil
}

// ---------- restore ----------

func cmdRestore(w *world, args []string) error {
	l, err := w.resolve(args)
	if err != nil {
		return err
	}
	added, updated, err := w.cur.install(l)
	if err != nil {
		return err
	}
	if len(added)+len(updated) == 0 {
		fmt.Println("\n  " + green(gDone) + " " + l.ID + "'s files are already intact.\n")
		return nil
	}
	fmt.Println()
	for _, f := range append(added, updated...) {
		fmt.Println("    " + green("↺ ") + f)
	}
	fmt.Println()
	return nil
}
