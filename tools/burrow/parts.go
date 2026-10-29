package main

import (
	"fmt"
	"strings"
)

func pips(n int) string {
	if n <= 0 {
		return ""
	}
	return amber(strings.Repeat("●", n)) + faint(strings.Repeat("○", max(0, 5-n)))
}

// cmdParts shows the component inventory: every part, who builds it, and
// which later levels plug it in.
func cmdParts(w *world, args []string) error {
	fmt.Println()
	fmt.Println("  " + logo())
	fmt.Println()
	fmt.Println("  " + bold("Parts") + gray("   every level builds one part; later levels plug it in"))
	fmt.Println()
	built := 0
	for _, ch := range w.cur.Chapters {
		if len(args) > 0 && args[0] != ch.Key && !strings.EqualFold(args[0], ch.Name) {
			continue
		}
		fmt.Println("  " + gray(chapterLabel(ch)+" · ") + bold(ch.Name) + ifs(ch.Optional, amber("  optional")))
		for _, l := range ch.Levels {
			done := w.levelDone(l)
			if done {
				built++
			}
			glyph, name := faint("▢"), gray(l.Unlock)
			if done {
				glyph, name = green("▣"), amber(bold(l.Unlock))
			} else if s := w.levelState(l); s == stOpen || s == stCurrent {
				glyph, name = cyan("▢"), l.Unlock
			}
			var feeds []string
			for _, c := range w.cur.consumers(l.Unlock) {
				id := c.ID
				if w.levelDone(c) {
					id = green(id)
				}
				feeds = append(feeds, id)
			}
			line := "    " + glyph + " " + padRight(name, 26) + gray(padRight(l.ID, 6))
			if len(feeds) > 0 {
				line += faint("→ ") + gray(strings.Join(feeds, " "))
			} else {
				line += faint("→ ") + amber("the finished machine")
			}
			fmt.Println(line)
		}
		fmt.Println()
	}
	fmt.Println("  " + gray(fmt.Sprintf("%d/%d parts built", built, len(w.cur.flat))) + gray("   ") + green("▣") + gray(" built  ") + cyan("▢") + gray(" buildable now  ") + faint("▢") + gray(" locked"))
	fmt.Println()
	return nil
}

// usesLine lists the parts a level plugs in, marking ones not built yet.
func usesLine(w *world, l *Level) string {
	if len(l.Consumes) == 0 {
		return ""
	}
	var parts []string
	for _, p := range l.Consumes {
		pr := w.cur.producer(p)
		label := p
		if pr != nil {
			label += gray(" (" + pr.ID + ")")
			if w.levelDone(pr) {
				label = green("▣ ") + label
			} else {
				label = faint("▢ ") + label
			}
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, gray(" · "))
}
