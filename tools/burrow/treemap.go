package main

import (
	"fmt"
	"strings"

	"burrowtool/tui"
)

// The tech tree is drawn on a tui.Canvas: four columns of chapter boxes per
// row, with routed wires between rows.

const (
	colW  = 18
	gapX  = 2
	boxH  = 4
	gapY  = 3
	rowsH = boxH + gapY
)

func cmdMap(w *world) error {
	if termWidth < mapWidth+4 {
		return cmdList(w, nil)
	}
	fmt.Println()
	fmt.Println("  " + logo())
	fmt.Println()
	fmt.Print(mapString(w))
	fmt.Println()
	fmt.Println("  " + gray(green(gDone)+" complete  "+cyan(gCurrent)+" in progress  "+gOpen+" unlocked  "+faint(gLocked)+" locked  "+amber("OPT")+" optional chapter"))
	fmt.Println("  " + gray("A chapter unlocks when every arrow into it is green. ") + bold("burrow list") + gray(" for levels."))
	fmt.Println()
	return nil
}

const mapWidth = 4*colW + 3*gapX

// mapString draws the chapter tech tree.
func mapString(w *world) string {
	maxRow := 0
	for _, ch := range w.cur.Chapters {
		maxRow = max(maxRow, ch.Pos.Row)
	}
	width := 4*colW + 3*gapX
	cv := tui.NewCanvas(width, (maxRow+1)*rowsH)
	type rect struct{ x, y, w int }
	rects := map[string]rect{}
	for _, ch := range w.cur.Chapters {
		x := (ch.Pos.Col - 1) * (colW + gapX)
		bw := ch.Pos.Span*colW + (ch.Pos.Span-1)*gapX
		rects[ch.Key] = rect{x, ch.Pos.Row * rowsH, bw}
	}
	// Connectors first, so boxes draw over them.
	for _, ch := range w.cur.Chapters {
		to := rects[ch.Key]
		for _, k := range ch.Needs {
			from := rects[k]
			ink := tui.InkFaint
			if w.chapterDone(w.cur.byKey[k]) {
				ink = tui.InkGreen
			}
			x1, x2 := from.x+from.w/2, to.x+to.w/2
			turn := to.y - 2
			cv.Route([]tui.Point{{X: x1, Y: from.y + boxH}, {X: x1, Y: turn}, {X: x2, Y: turn}, {X: x2, Y: to.y - 1}}, ink, true)
		}
	}
	for _, ch := range w.cur.Chapters {
		r := rects[ch.Key]
		s := w.chapterState(ch)
		edge, name := tui.InkFaint, tui.InkGray
		switch s {
		case stDone:
			edge, name = tui.InkGreen, tui.InkGreen
		case stCurrent:
			edge, name = tui.InkCyan, tui.InkCyan.WithBold()
		case stOpen:
			edge, name = tui.Ink{Color: tui.White}, tui.InkBold
		}
		if ch.Optional && s != stDone {
			edge = tui.InkAmber
			if s == stLocked {
				edge = tui.Ink{Color: tui.AmberDim}
			}
		}
		n := 0
		for _, l := range ch.Levels {
			if w.levelDone(l) {
				n++
			}
		}
		cv.Box(r.x, r.y, r.w, boxH, tui.Rounded, edge, strings.ToUpper(chapterLabel(ch)), tui.InkGray)
		glyph := map[state]string{stDone: gDone, stCurrent: gCurrent, stOpen: gOpen, stLocked: gLocked}[s]
		cv.TextMax(r.x+2, r.y+1, glyph+" "+ch.Name, r.w-4, name)
		x := r.x + 2
		for _, l := range ch.Levels {
			if x < r.x+r.w-8 {
				g, ink := lampFor(w, l).Glyph(0)
				cv.Set(x, r.y+2, g, ink)
				x += 2
			}
		}
		cnt := fmt.Sprintf("%d/%d", n, len(ch.Levels))
		cv.Text(r.x+r.w-2-len(cnt), r.y+2, cnt, tui.InkGray)
	}
	return cv.String("  ")
}
