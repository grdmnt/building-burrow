package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type fileStamp struct {
	mod  time.Time
	size int64
}

func snapshot(root string) map[string]fileStamp {
	out := map[string]fileStamp{}
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if p != root && (strings.HasPrefix(name, ".") || name == "curriculum" || name == "tools" || name == "node_modules" || name == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(name, ".go") || name == "go.mod" || name == "go.sum" {
			if info, err := d.Info(); err == nil {
				out[p] = fileStamp{info.ModTime(), info.Size()}
			}
		}
		return nil
	})
	return out
}

func changed(a, b map[string]fileStamp) string {
	for p, s := range b {
		if a[p] != s {
			return p
		}
	}
	for p := range a {
		if _, ok := b[p]; !ok {
			return p
		}
	}
	return ""
}

// rawTTY switches the terminal to single-key input. It returns a restore func.
func rawTTY() (func(), bool) {
	if !isTTY {
		return func() {}, false
	}
	tty, err := os.Open("/dev/tty")
	if err != nil {
		return func() {}, false
	}
	get := exec.Command("stty", "-g")
	get.Stdin = tty
	saved, err := get.Output()
	if err != nil {
		tty.Close()
		return func() {}, false
	}
	set := exec.Command("stty", "-icanon", "-echo", "min", "1")
	set.Stdin = tty
	if set.Run() != nil {
		tty.Close()
		return func() {}, false
	}
	return func() {
		r := exec.Command("stty", strings.TrimSpace(string(saved)))
		r.Stdin = tty
		r.Run()
		tty.Close()
	}, true
}

func cmdWatch(w *world, args []string) error {
	l, err := w.resolve(args)
	if err != nil {
		return err
	}
	restore, raw := rawTTY()
	defer restore()
	hideCursor()
	defer showCursor()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)

	keys := make(chan byte, 8)
	go func() {
		buf := make([]byte, 1)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil {
				return
			}
			if n == 1 && buf[0] != '\n' && buf[0] != '\r' {
				keys <- buf[0]
			}
		}
	}()

	var cancel context.CancelFunc = func() {}
	var prevDone chan struct{}
	results := make(chan *runSummary, 1)
	var last *runSummary
	pending := false // a branch choice is waiting for a digit
	var choices []*Level

	startRun := func(reason string) {
		cancel()
		if prevDone != nil {
			select {
			case <-prevDone:
			case <-time.After(3 * time.Second):
			}
		}
		var ctx context.Context
		ctx, cancel = context.WithCancel(context.Background())
		done := make(chan struct{})
		prevDone = done
		clearScreen()
		sub := chapterLabel(l.ch) + " · " + l.ch.Name
		if reason != "" {
			sub += "  ·  " + reason
		}
		go func(ctx context.Context, lvl *Level) {
			defer close(done)
			sum := watchRun(ctx, w, lvl, sub)
			if ctx.Err() == nil {
				results <- sum
			}
		}(ctx, l)
	}
	footer := func() {
		k := func(key, label string) string { return bold(cyan(key)) + gray(" "+label) }
		keysLine := k("h", "hint") + "  " + k("r", "rerun") + "  " + k("s", "spec") + "  " + k("d", "done") + "  " + k("q", "quit")
		if !raw {
			keysLine += gray("   (type a key, then Enter)")
		}
		fmt.Println(rule(""))
		fmt.Println("  " + cyan("◉") + gray(" watching for changes   ") + keysLine)
	}

	startRun("")
	before := snapshot(w.cur.root)
	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()
	defer cancel()
	for {
		select {
		case <-sigs:
			fmt.Println()
			return nil
		case sum := <-results:
			last = sum
			footer()
		case <-tick.C:
			now := snapshot(w.cur.root)
			if p := changed(before, now); p != "" {
				time.Sleep(120 * time.Millisecond) // let editors finish writing
				before = snapshot(w.cur.root)
				rel, _ := filepath.Rel(w.cur.root, p)
				startRun("changed " + rel)
			}
		case k := <-keys:
			if pending {
				pending = false
				if k >= '1' && int(k-'0') <= len(choices) {
					l = choices[k-'1']
					showCursor()
					startLevel(w, l, false)
					hideCursor()
					time.Sleep(900 * time.Millisecond)
					before = snapshot(w.cur.root)
					startRun("started")
				}
				continue
			}
			switch k {
			case 'q', 'Q', 3:
				fmt.Println()
				return nil
			case 'r', 'R':
				startRun("rerun")
			case 'h', 'H':
				revealHint(w, l)
			case 's', 'S':
				clearScreen()
				src, _ := os.ReadFile(w.cur.levelDir(l) + "/README.md")
				fmt.Println(renderMarkdown(string(src)))
				footer()
			case 'd', 'D':
				if w.levelDone(l) {
					fmt.Println("\n  " + green(gDone) + " " + l.ID + " is already complete. " + gray("Run `burrow next` for the next level.") + "\n")
					continue
				}
				if last == nil || !last.ok() {
					fmt.Println("\n  " + yellow("Not yet: ") + gray("every test has to pass first.") + "\n")
					continue
				}
				if changedFiles, missing := w.cur.tampered(l); len(changedFiles)+len(missing) > 0 {
					fmt.Println("\n  " + red("The level's provided files changed.") + gray(" Run `burrow restore` first.") + "\n")
					continue
				}
				if err := complete(w, l); err != nil {
					return err
				}
				av := w.available()
				switch len(av) {
				case 0:
					return nil
				case 1:
					l = av[0]
					startLevel(w, l, false)
					time.Sleep(1200 * time.Millisecond)
					before = snapshot(w.cur.root)
					startRun("started")
				default:
					choices, pending = av, true
					fmt.Println("  " + bold("The path branches.") + gray(" Press a number to pick:"))
					for i, x := range av {
						fmt.Printf("    %s  %s %s%s\n", cyan(fmt.Sprintf("[%d]", i+1)), bold(x.ID), x.Title, gray("  · "+x.ch.Name))
					}
				}
			}
		}
	}
}

func watchRun(ctx context.Context, w *world, l *Level, sub string) *runSummary {
	header(l.ID+" · "+l.Title, sub)
	sum := runTests(ctx, w, runOpts{levels: []*Level{l}})
	if sum.cancelled {
		return sum
	}
	renderFailures(sum, w.cur.root)
	renderSummary(sum)
	saveLastRun(w.cur.root, sum)
	if sum.ok() {
		fmt.Println()
		sparkle(min(termWidth-2, 60), green)
		msg := []string{gray("produces ") + amber(l.Unlock)}
		if !w.levelDone(l) {
			msg = append(msg, gray("press ")+bold("d")+gray(" to complete the level"))
		}
		fmt.Print(box(green(bold("★ ALL GREEN"))+gray(" · "+l.ID+" "+l.Title), msg, min(termWidth-2, 60), green))
	}
	fmt.Println()
	return sum
}
