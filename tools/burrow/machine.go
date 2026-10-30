package main

import (
	"fmt"
	"strings"

	"burrowtool/tui"
)

// The machine view draws Burrow as the system it becomes: clients talk to the
// front doors, which drive the core store and its disk log, which the cluster
// replicates. Every level is one lamp, lit when you've built that part.

type machineRow struct {
	key   string // chapter key
	label string
}

type machineBlock struct {
	title string
	rows  []machineRow
}

var (
	frontDoors = machineBlock{"FRONT DOORS", []machineRow{
		{"3", "frame protocol"}, {"6", "HTTP by hand"}, {"7", "HTTP API"}, {"8", "leases & watch"},
	}}
	core = machineBlock{"CORE", []machineRow{
		{"0", "burrowd"}, {"1", "store"}, {"2", "codec"}, {"4", "disk log"}, {"5", "daemon"},
	}}
	cluster = machineBlock{"CLUSTER", []machineRow{
		{"9", "cluster"}, {"10", "raft"}, {"11", "chaos"},
	}}
	addOns = []machineRow{
		{"M", "toolchain"}, {"H", "deep cuts"}, {"T", "traffic"}, {"S", "tls"}, {"D", "dynamo"}, {"Q", "queues"},
	}
)

// machineAnim switches on one part with a flash.
type machineAnim struct {
	part  string // level ID whose lamp flashes
	frame int
}

const machineWidth = 71

func (w *world) machineLamp(l *Level, a machineAnim) tui.Lamp {
	if a.part == l.ID {
		if a.frame < 14 {
			return tui.LampNew
		}
		return tui.LampPass
	}
	return lampFor(w, l)
}

// lampsText draws a chapter's lamps onto the canvas and returns how many
// cells they took.
func (w *world) drawLamps(cv *tui.Canvas, x, y int, ch *Chapter, a machineAnim) int {
	for i, l := range ch.Levels {
		r, ink := w.machineLamp(l, a).Glyph(a.frame)
		cv.Set(x+i, y, r, ink)
	}
	return len(ch.Levels)
}

func (w *world) blockInk(rows []machineRow, a machineAnim) tui.Ink {
	all, any, cur := true, false, false
	for _, r := range rows {
		ch := w.cur.byKey[r.key]
		for _, l := range ch.Levels {
			done := w.levelDone(l) || l.ID == a.part
			all = all && done
			any = any || done
			cur = cur || w.prog.Current == l.ID || l.ID == a.part
		}
	}
	switch {
	case a.part != "" && cur && a.frame < 14 && a.frame%4 < 2:
		return tui.Ink{Color: tui.Yellow, Bold: true}
	case all:
		return tui.InkGreen
	case cur:
		return tui.InkCyan
	case any:
		return tui.Ink{Color: tui.White}
	}
	return tui.InkFaint
}

func (w *world) anyBuilt(rows []machineRow, a machineAnim) bool {
	for _, r := range rows {
		for _, l := range w.cur.byKey[r.key].Levels {
			if w.levelDone(l) || l.ID == a.part {
				return true
			}
		}
	}
	return false
}

func (w *world) drawBlock(cv *tui.Canvas, x, y, width int, b machineBlock, a machineAnim) int {
	h := len(b.rows) + 2
	cv.Box(x, y, width, h, tui.Rounded, w.blockInk(b.rows, a), b.title, tui.InkGray.WithBold())
	for i, r := range b.rows {
		ch := w.cur.byKey[r.key]
		cv.TextMax(x+2, y+1+i, r.label, 14, tui.Plain)
		n := w.drawLamps(cv, x+17, y+1+i, ch, a)
		cv.Text(x+17+n+1, y+1+i, ch.Key, tui.InkFaint)
	}
	return h
}

// machineLines renders the machine. It returns nil when width is too narrow.
func machineLines(w *world, width int, a machineAnim) []string {
	if width < machineWidth {
		return nil
	}
	const (
		clientX, clientW   = 0, 11
		frontX, blockW     = 14, 27
		coreX              = 44
		frontY, coreY      = 0, 0
		clientY, arrowY    = 2, 3
		diskW, diskH       = 10, 3
		clusterGap, addGap = 2, 1
	)
	frontH := len(frontDoors.rows) + 2
	coreH := len(core.rows) + 2
	diskY := coreY + coreH + 2
	clusterY := max(frontY+frontH, clientY+3) + 2
	clusterH := 3
	addY := max(clusterY+clusterH, diskY+diskH) + addGap
	addRows := 2
	cv := tui.NewCanvas(machineWidth, addY+addRows+2)

	lit := func(on bool) tui.Ink {
		if on {
			return tui.InkGreen
		}
		return tui.InkFaint
	}
	frontOn := w.anyBuilt(frontDoors.rows, a)
	coreOn := w.anyBuilt(core.rows, a)
	clusterOn := w.anyBuilt(cluster.rows, a)
	diskOn := w.anyBuilt([]machineRow{{"4", ""}}, a)

	// Wires go down first; blocks draw over their ends.
	cv.Route([]tui.Point{{X: clientX + clientW, Y: arrowY}, {X: frontX - 1, Y: arrowY}}, lit(frontOn), true)
	cv.Route([]tui.Point{{X: frontX + blockW, Y: arrowY}, {X: coreX - 1, Y: arrowY}}, lit(frontOn && coreOn), true)
	diskWireX := coreX + blockW - 6
	cv.Route([]tui.Point{{X: diskWireX, Y: coreY + coreH}, {X: diskWireX, Y: diskY - 1}}, lit(diskOn), true)
	upX := coreX + 4
	clusterRight := frontX + blockW
	cv.Route([]tui.Point{{X: clusterRight, Y: clusterY + 1}, {X: upX, Y: clusterY + 1}, {X: upX, Y: coreY + coreH}}, lit(clusterOn), true)

	// Clients and disk are the ends of the line.
	cv.Box(clientX, clientY, clientW, 3, tui.Rounded, tui.InkGray, "", tui.Plain)
	cv.Text(clientX+2, clientY+1, "clients", tui.InkGray)
	cv.Box(diskWireX-diskW/2, diskY, diskW, diskH, tui.Rounded, lit(diskOn), "", tui.Plain)
	cv.Text(diskWireX-diskW/2+3, diskY+1, "disk", tui.InkGray)

	w.drawBlock(cv, frontX, frontY, blockW, frontDoors, a)
	w.drawBlock(cv, coreX, coreY, blockW, core, a)

	// The cluster is one wide row of three chapters.
	cv.Box(clientX, clusterY, clusterRight-clientX, clusterH, tui.Rounded, w.blockInk(cluster.rows, a), cluster.title, tui.InkGray.WithBold())
	x := clientX + 2
	for _, r := range cluster.rows {
		ch := w.cur.byKey[r.key]
		cv.Text(x, clusterY+1, r.label, tui.Plain)
		x += len(r.label) + 1
		x += w.drawLamps(cv, x, clusterY+1, ch, a) + 2
	}

	// Optional add-ons, two rows of three.
	cv.Box(clientX, addY, machineWidth, addRows+2, tui.Rounded, w.blockInk(addOns, a), "ADD-ONS · optional", tui.InkGray)
	for i, r := range addOns {
		ch := w.cur.byKey[r.key]
		px := clientX + 2 + (i%3)*22
		py := addY + 1 + i/3
		cv.Text(px, py, r.label, tui.InkAmber)
		n := w.drawLamps(cv, px+11, py, ch, a)
		cv.Text(px+11+n+1, py, ch.Key, tui.InkFaint)
	}
	return cv.Lines()
}

// powerLine is the header above the machine: how much of it is built.
func powerLine(w *world, a machineAnim) string {
	var built, total, addBuilt, addTotal int
	for _, l := range w.cur.flat {
		done := w.levelDone(l) || l.ID == a.part
		if l.ch.Optional {
			addTotal++
			if done {
				addBuilt++
			}
			continue
		}
		total++
		if done {
			built++
		}
	}
	const width = 30
	n := built * width / max(total, 1)
	meter := tui.InkGreen.Render(strings.Repeat("█", n)) + tui.InkFaint.Render(strings.Repeat("░", width-n))
	return tui.InkGray.WithBold().Render("POWER ") + tui.InkFaint.Render("▕") + meter + tui.InkFaint.Render("▏") +
		fmt.Sprintf("  %s %s", tui.Ink{Color: tui.White, Bold: true}.Render(fmt.Sprintf("%d/%d", built, total)), tui.InkGray.Render("parts online")) +
		tui.InkGray.Render(fmt.Sprintf("  · add-ons %d/%d", addBuilt, addTotal))
}

// chapterParts lists one chapter's parts with their lamps, for under the machine.
func chapterParts(w *world, ch *Chapter, width int, a machineAnim) []string {
	var out []string
	out = append(out, tui.InkGray.Render(strings.ToUpper(chapterLabel(ch))+" · ")+tui.InkBold.Render(ch.Name))
	cell := 26
	perLine := max(1, width/cell)
	line := ""
	for i, l := range ch.Levels {
		item := w.machineLamp(l, a).Render(a.frame) + " " + tui.InkGray.Render(l.ID) + " " + tui.Truncate(l.Unlock, cell-len(l.ID)-4)
		line += padRight(item, cell)
		if (i+1)%perLine == 0 {
			out = append(out, strings.TrimRight(line, " "))
			line = ""
		}
	}
	if line != "" {
		out = append(out, strings.TrimRight(line, " "))
	}
	return out
}

// focusChapter is the chapter to detail under the machine: the current
// level's, or the first one with work left.
func focusChapter(w *world) *Chapter {
	if l := w.cur.byID[w.prog.Current]; l != nil {
		return l.ch
	}
	for _, l := range w.available() {
		return l.ch
	}
	return w.cur.Chapters[len(w.cur.Chapters)-1]
}

func machineScreen(w *world, width int, a machineAnim, focus *Chapter) string {
	var b strings.Builder
	b.WriteString("  " + powerLine(w, a) + "\n\n")
	lines := machineLines(w, width-2, a)
	if lines == nil {
		b.WriteString("  " + gray("Widen the terminal to at least 74 columns to see the machine.") + "\n")
	}
	for _, l := range lines {
		b.WriteString("  " + l + "\n")
	}
	if focus != nil {
		b.WriteString("\n")
		for _, l := range chapterParts(w, focus, width-4, a) {
			b.WriteString("  " + l + "\n")
		}
	}
	legend := tui.LampPass.String() + gray(" built  ") + tui.LampRun.String() + gray(" in progress  ") +
		tui.LampOpen.String() + gray(" ready  ") + tui.LampOff.String() + gray(" locked")
	b.WriteString("\n  " + legend + "\n")
	return b.String()
}

func cmdMachine(w *world) error {
	fmt.Println()
	fmt.Println("  " + logo())
	fmt.Println()
	fmt.Print(machineScreen(w, termWidth, machineAnim{}, focusChapter(w)))
	fmt.Println()
	return nil
}
