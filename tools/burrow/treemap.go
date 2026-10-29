package main

import (
	"fmt"
	"strings"
)

// The tech tree is drawn on a character canvas: four columns of chapter
// boxes per row, with routed connectors between rows.

type cell struct {
	r     rune
	style func(string) string
	bits  int
	lit   bool
}

type canvas struct {
	w, h  int
	cells [][]cell
}

func newCanvas(w, h int) *canvas {
	c := &canvas{w: w, h: h, cells: make([][]cell, h)}
	for y := range c.cells {
		c.cells[y] = make([]cell, w)
		for x := range c.cells[y] {
			c.cells[y][x] = cell{r: ' '}
		}
	}
	return c
}

func (c *canvas) set(x, y int, r rune, style func(string) string) {
	if x < 0 || y < 0 || x >= c.w || y >= c.h {
		return
	}
	c.cells[y][x] = cell{r: r, style: style}
}

const (
	up = 1 << iota
	down
	left
	right
)

var linkGlyph = map[int]rune{
	up | down: '│', left | right: '─', up | right: '╰', up | left: '╯', down | right: '╭', down | left: '╮',
	up | down | right: '├', up | down | left: '┤', left | right | down: '┬', left | right | up: '┴',
	up | down | left | right: '┼', up: '│', down: '│', left: '─', right: '─',
}

// link adds connector directions to a cell and redraws its glyph.
func (c *canvas) link(x, y, dirs int, lit bool) {
	if x < 0 || y < 0 || x >= c.w || y >= c.h {
		return
	}
	cl := &c.cells[y][x]
	cl.bits |= dirs
	cl.lit = cl.lit || lit
	cl.r = linkGlyph[cl.bits]
	cl.style = faint
	if cl.lit {
		cl.style = green
	}
}

func (c *canvas) text(x, y int, s string, style func(string) string) {
	for i, r := range []rune(s) {
		c.cells[y][x+i] = cell{r: r, style: style}
	}
}

func (c *canvas) String() string {
	var b strings.Builder
	last := len(c.cells) - 1
	for last > 0 && strings.TrimSpace(c.rowText(last)) == "" {
		last--
	}
	for _, row := range c.cells[:last+1] {
		line := ""
		for _, cl := range row {
			s := string(cl.r)
			if cl.style != nil {
				s = cl.style(s)
			}
			line += s
		}
		b.WriteString("  " + strings.TrimRight(line, " ") + "\n")
	}
	return b.String()
}

func (c *canvas) rowText(y int) string {
	var b strings.Builder
	for _, cl := range c.cells[y] {
		b.WriteRune(cl.r)
	}
	return b.String()
}

const (
	colW  = 18
	gapX  = 2
	boxH  = 4
	gapY  = 3
	rowsH = boxH + gapY
)

func cmdMap(w *world) error {
	if termWidth < 4*colW+3*gapX+4 {
		return cmdList(w, nil)
	}
	maxRow := 0
	for _, ch := range w.cur.Chapters {
		maxRow = max(maxRow, ch.Pos.Row)
	}
	width := 4*colW + 3*gapX
	cv := newCanvas(width, (maxRow+1)*rowsH)
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
			lit := w.chapterDone(w.cur.byKey[k])
			st := faint
			if lit {
				st = green
			}
			x1 := from.x + from.w/2
			x2 := to.x + to.w/2
			y1 := from.y + boxH
			y2 := to.y - 1
			turn := y2 - 1
			for y := y1; y < turn; y++ {
				cv.link(x1, y, up|down, lit)
			}
			switch {
			case x1 == x2:
				cv.link(x1, turn, up|down, lit)
			case x2 > x1:
				cv.link(x1, turn, up|right, lit)
				for x := x1 + 1; x < x2; x++ {
					cv.link(x, turn, left|right, lit)
				}
				cv.link(x2, turn, left|down, lit)
			default:
				cv.link(x1, turn, up|left, lit)
				for x := x2 + 1; x < x1; x++ {
					cv.link(x, turn, left|right, lit)
				}
				cv.link(x2, turn, right|down, lit)
			}
			cv.set(x2, y2, '▼', st)
		}
	}
	for _, ch := range w.cur.Chapters {
		r := rects[ch.Key]
		s := w.chapterState(ch)
		edge := faint
		name := gray
		switch s {
		case stDone:
			edge, name = green, green
		case stCurrent:
			edge, name = cyan, func(x string) string { return bold(cyan(x)) }
		case stOpen:
			edge, name = func(x string) string { return x }, bold
		}
		if ch.Optional && s != stDone {
			edge = amber
			if s == stLocked {
				edge = func(x string) string { return faint(amber(x)) }
			}
		}
		n := 0
		for _, l := range ch.Levels {
			if w.levelDone(l) {
				n++
			}
		}
		// box
		cv.set(r.x, r.y, '╭', edge)
		cv.set(r.x+r.w-1, r.y, '╮', edge)
		cv.set(r.x, r.y+boxH-1, '╰', edge)
		cv.set(r.x+r.w-1, r.y+boxH-1, '╯', edge)
		for x := r.x + 1; x < r.x+r.w-1; x++ {
			cv.cells[r.y][x] = cell{r: '─', style: edge}
			cv.cells[r.y+boxH-1][x] = cell{r: '─', style: edge}
		}
		for y := r.y + 1; y < r.y+boxH-1; y++ {
			cv.cells[y][r.x] = cell{r: '│', style: edge}
			cv.cells[y][r.x+r.w-1] = cell{r: '│', style: edge}
			for x := r.x + 1; x < r.x+r.w-1; x++ {
				cv.cells[y][x] = cell{r: ' '}
			}
		}
		label := " " + strings.ToUpper(chapterLabel(ch)) + " "
		cv.text(r.x+2, r.y, label, gray)
		if ch.Optional {
			cv.text(r.x+r.w-6, r.y, " opt ", amber)
		}
		glyph := map[state]string{stDone: gDone, stCurrent: gCurrent, stOpen: gOpen, stLocked: gLocked}[s]
		cv.text(r.x+2, r.y+1, glyph+" "+truncate(ch.Name, r.w-6), name)
		// level pips
		x := r.x + 2
		for _, l := range ch.Levels {
			ls := w.levelState(l)
			p, st := '▪', faint
			switch ls {
			case stDone:
				st = green
			case stCurrent:
				p, st = '▸', cyan
			case stOpen:
				st = func(x string) string { return x }
			}
			if x < r.x+r.w-8 {
				cv.set(x, r.y+2, p, st)
				x += 2
			}
		}
		cnt := fmt.Sprintf("%d/%d", n, len(ch.Levels))
		cv.text(r.x+r.w-2-len(cnt), r.y+2, cnt, gray)
	}
	fmt.Println()
	fmt.Println("  " + logo())
	fmt.Println()
	fmt.Print(cv.String())
	fmt.Println()
	fmt.Println("  " + gray(green(gDone)+" complete  "+cyan(gCurrent)+" in progress  "+gOpen+" unlocked  "+faint(gLocked)+" locked  "+amber("opt")+" optional"))
	fmt.Println("  " + gray("A chapter unlocks when every arrow into it is green. ") + bold("burrow list") + gray(" for levels."))
	fmt.Println()
	return nil
}
