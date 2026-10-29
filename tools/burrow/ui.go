package main

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// ---------- terminal capabilities ----------

var (
	isTTY     = stdoutIsTTY()
	useColor  = isTTY && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
	animate   = useColor && os.Getenv("BURROW_NO_ANIM") == ""
	termWidth = detectWidth()
)

func stdoutIsTTY() bool {
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func detectWidth() int {
	if c, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && c > 20 {
		return min(c, 110)
	}
	if isTTY {
		cmd := exec.Command("stty", "size")
		if tty, err := os.Open("/dev/tty"); err == nil {
			cmd.Stdin = tty
			defer tty.Close()
		}
		if out, err := cmd.Output(); err == nil {
			parts := strings.Fields(string(out))
			if len(parts) == 2 {
				if w, err := strconv.Atoi(parts[1]); err == nil && w > 20 {
					return min(w, 110)
				}
			}
		}
	}
	return 80
}

// ---------- colour ----------

func paint(code, s string) string {
	if !useColor || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func bold(s string) string    { return paint("1", s) }
func dim(s string) string     { return paint("2", s) }
func italic(s string) string  { return paint("3", s) }
func red(s string) string     { return paint("38;5;203", s) }
func green(s string) string   { return paint("38;5;114", s) }
func yellow(s string) string  { return paint("38;5;221", s) }
func amber(s string) string   { return paint("38;5;215", s) }
func cyan(s string) string    { return paint("38;5;80", s) }
func blue(s string) string    { return paint("38;5;75", s) }
func magenta(s string) string { return paint("38;5;176", s) }
func gray(s string) string    { return paint("38;5;244", s) }
func faint(s string) string   { return paint("38;5;239", s) }

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func visibleLen(s string) int { return utf8.RuneCountInString(ansiRE.ReplaceAllString(s, "")) }

func padRight(s string, n int) string {
	if v := visibleLen(s); v < n {
		return s + strings.Repeat(" ", n-v)
	}
	return s
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:max(0, n-1)]) + "…"
}

// ---------- glyphs ----------

const (
	gDone    = "✔"
	gFail    = "✘"
	gSkip    = "◌"
	gCurrent = "▶"
	gOpen    = "○"
	gLocked  = "·"
)

var spinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// ---------- progress bar ----------

func bar(done, total, width int, fill func(string) string) string {
	if total <= 0 {
		return faint(strings.Repeat("░", width))
	}
	n := done * width / total
	if done > 0 && n == 0 {
		n = 1
	}
	return fill(strings.Repeat("█", n)) + faint(strings.Repeat("░", width-n))
}

// segmented bar: one cell per item, coloured by state.
func segBar(states []string) string {
	var b strings.Builder
	for _, s := range states {
		switch s {
		case "pass":
			b.WriteString(green("█"))
		case "fail":
			b.WriteString(red("█"))
		case "skip":
			b.WriteString(gray("▒"))
		default:
			b.WriteString(faint("░"))
		}
	}
	return b.String()
}

// ---------- boxes ----------

func box(title string, lines []string, width int, edge func(string) string) string {
	inner := width - 4
	var b strings.Builder
	t := ""
	if title != "" {
		t = " " + title + " "
	}
	top := "╭─" + t + strings.Repeat("─", max(0, width-3-visibleLen(t))) + "╮"
	b.WriteString(edgeTitle(top, t, edge) + "\n")
	for _, l := range lines {
		for _, wl := range wrapVisible(l, inner) {
			b.WriteString(edge("│") + " " + padRight(wl, inner) + " " + edge("│") + "\n")
		}
	}
	b.WriteString(edge("╰"+strings.Repeat("─", width-2)+"╯") + "\n")
	return b.String()
}

func edgeTitle(top, title string, edge func(string) string) string {
	if title == "" {
		return edge(top)
	}
	i := strings.Index(top, title)
	return edge(top[:i]) + title + edge(top[i+len(title):])
}

// wrapVisible wraps a string that may contain ANSI codes at word boundaries.
func wrapVisible(s string, width int) []string {
	if visibleLen(s) <= width || width < 10 {
		return []string{s}
	}
	words := strings.Split(s, " ")
	var lines []string
	cur := ""
	for _, w := range words {
		if cur == "" {
			cur = w
			continue
		}
		if visibleLen(cur)+1+visibleLen(w) > width {
			lines = append(lines, cur)
			cur = w
		} else {
			cur += " " + w
		}
	}
	return append(lines, cur)
}

// ---------- live status line ----------

// live keeps one animated status line at the bottom of the output while
// other lines are printed above it.
type live struct {
	mu     sync.Mutex
	render func(frame int) string
	frame  int
	stop   chan struct{}
	done   chan struct{}
	shown  bool
}

func startLive(render func(frame int) string) *live {
	l := &live{render: render, stop: make(chan struct{}), done: make(chan struct{})}
	if !animate {
		close(l.done)
		return l
	}
	go func() {
		defer close(l.done)
		t := time.NewTicker(80 * time.Millisecond)
		defer t.Stop()
		for {
			l.mu.Lock()
			l.draw()
			l.mu.Unlock()
			select {
			case <-l.stop:
				return
			case <-t.C:
			}
		}
	}()
	return l
}

func (l *live) draw() {
	l.frame++
	fmt.Print("\r\x1b[2K" + l.render(l.frame))
	l.shown = true
}

func (l *live) clear() {
	if l.shown {
		fmt.Print("\r\x1b[2K")
		l.shown = false
	}
}

// Println prints a line above the status line.
func (l *live) Println(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if animate {
		l.clear()
	}
	fmt.Println(s)
	if animate {
		l.draw()
	}
}

func (l *live) Stop() {
	if animate {
		select {
		case <-l.stop:
		default:
			close(l.stop)
		}
	}
	<-l.done
	l.mu.Lock()
	l.clear()
	l.mu.Unlock()
}

// ---------- celebration ----------

func sparkle(width int, colorize func(string) string) {
	if !animate {
		return
	}
	glyphs := []string{"✦", "✧", "⋆", "·", "✶", "✷"}
	const steps = 18
	for i := 0; i <= steps; i++ {
		var b strings.Builder
		head := i * width / steps
		for x := 0; x < width; x++ {
			d := head - x
			switch {
			case d < 0:
				b.WriteByte(' ')
			case d < 3:
				b.WriteString(bold(colorize(glyphs[(x+i)%len(glyphs)])))
			case d < 10:
				b.WriteString(colorize(glyphs[(x*7+i)%len(glyphs)]))
			case (x+i)%5 == 0:
				b.WriteString(faint("·"))
			default:
				b.WriteByte(' ')
			}
		}
		fmt.Print("\r\x1b[2K" + b.String())
		time.Sleep(28 * time.Millisecond)
	}
	fmt.Print("\r\x1b[2K")
}

// typeOut prints s with a quick typewriter effect.
func typeOut(s string) {
	if !animate {
		fmt.Println(s)
		return
	}
	plain := ansiRE.ReplaceAllString(s, "")
	if len(plain) > 80 {
		fmt.Println(s)
		return
	}
	for i := range plain {
		fmt.Print("\r" + plain[:i])
		time.Sleep(6 * time.Millisecond)
	}
	fmt.Print("\r\x1b[2K" + s + "\n")
}

func clearScreen() {
	if isTTY {
		fmt.Print("\x1b[H\x1b[2J\x1b[3J")
	}
}

func hideCursor() {
	if animate {
		fmt.Print("\x1b[?25l")
	}
}

func showCursor() {
	if animate {
		fmt.Print("\x1b[?25h")
	}
}

func rule(label string) string {
	if label == "" {
		return faint(strings.Repeat("─", termWidth))
	}
	l := " " + label + " "
	return faint("──") + l + faint(strings.Repeat("─", max(0, termWidth-2-visibleLen(l))))
}

func duration(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	default:
		return d.Round(time.Second).String()
	}
}
