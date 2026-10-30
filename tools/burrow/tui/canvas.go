package tui

import (
	"strings"

	"github.com/mattn/go-runewidth"
)

// Canvas is a grid of character cells. Boxes, text and wires are drawn on it
// and then rendered to lines of styled text.
type Canvas struct {
	W, H  int
	cells [][]cell
}

type cell struct {
	r    rune
	ink  Ink
	wire uint8 // connector directions, for wire cells
}

// Directions for wires.
const (
	Up uint8 = 1 << iota
	Down
	Left
	Right
)

var wireGlyph = map[uint8]rune{
	Up | Down: '│', Left | Right: '─',
	Up | Right: '╰', Up | Left: '╯', Down | Right: '╭', Down | Left: '╮',
	Up | Down | Right: '├', Up | Down | Left: '┤',
	Left | Right | Down: '┬', Left | Right | Up: '┴',
	Up | Down | Left | Right: '┼',
	Up:                       '│', Down: '│', Left: '─', Right: '─',
}

// NewCanvas returns a blank w by h canvas.
func NewCanvas(w, h int) *Canvas {
	c := &Canvas{W: w, H: h, cells: make([][]cell, h)}
	for y := range c.cells {
		c.cells[y] = make([]cell, w)
		for x := range c.cells[y] {
			c.cells[y][x] = cell{r: ' '}
		}
	}
	return c
}

func (c *Canvas) in(x, y int) bool { return x >= 0 && y >= 0 && x < c.W && y < c.H }

// Set puts one rune at (x, y).
func (c *Canvas) Set(x, y int, r rune, ink Ink) {
	if c.in(x, y) {
		c.cells[y][x] = cell{r: r, ink: ink}
	}
}

// Text writes s starting at (x, y), clipped to the canvas.
func (c *Canvas) Text(x, y int, s string, ink Ink) {
	for _, r := range s {
		c.Set(x, y, r, ink)
		x += max(1, runewidth.RuneWidth(r))
	}
}

// TextMax writes s, cutting it with an ellipsis to fit n cells.
func (c *Canvas) TextMax(x, y int, s string, n int, ink Ink) {
	c.Text(x, y, Truncate(s, n), ink)
}

// Fill clears a rectangle to spaces.
func (c *Canvas) Fill(x, y, w, h int) {
	for yy := y; yy < y+h; yy++ {
		for xx := x; xx < x+w; xx++ {
			c.Set(xx, yy, ' ', Plain)
		}
	}
}

// BoxStyle picks the border glyphs for Box.
type BoxStyle int

const (
	Rounded BoxStyle = iota
	Heavy
	Double
)

var borders = map[BoxStyle][6]rune{
	Rounded: {'╭', '╮', '╰', '╯', '─', '│'},
	Heavy:   {'┏', '┓', '┗', '┛', '━', '┃'},
	Double:  {'╔', '╗', '╚', '╝', '═', '║'},
}

// Box draws a w by h border with its top-left corner at (x, y), clears the
// inside, and writes title into the top edge.
func (c *Canvas) Box(x, y, w, h int, style BoxStyle, ink Ink, title string, titleInk Ink) {
	b := borders[style]
	c.Fill(x+1, y+1, w-2, h-2)
	c.Set(x, y, b[0], ink)
	c.Set(x+w-1, y, b[1], ink)
	c.Set(x, y+h-1, b[2], ink)
	c.Set(x+w-1, y+h-1, b[3], ink)
	for xx := x + 1; xx < x+w-1; xx++ {
		c.Set(xx, y, b[4], ink)
		c.Set(xx, y+h-1, b[4], ink)
	}
	for yy := y + 1; yy < y+h-1; yy++ {
		c.Set(x, yy, b[5], ink)
		c.Set(x+w-1, yy, b[5], ink)
	}
	if title != "" && w > 6 {
		c.TextMax(x+2, y, " "+title+" ", w-4, titleInk)
	}
}

// Wire adds connector directions to the cell at (x, y) and redraws it, so
// crossing and joining wires get the right junction glyph.
func (c *Canvas) Wire(x, y int, dirs uint8, ink Ink) {
	if !c.in(x, y) {
		return
	}
	cl := &c.cells[y][x]
	if cl.wire == 0 && cl.r != ' ' {
		return // never draw a wire over text or a box
	}
	cl.wire |= dirs
	cl.r = wireGlyph[cl.wire]
	// A lit wire wins over an unlit one at a junction.
	if cl.ink == (Ink{}) || ink.Color != Faint {
		cl.ink = ink
	}
}

// Point is a cell position.
type Point struct{ X, Y int }

// Route draws an orthogonal wire through the points (each step must be
// horizontal or vertical). With arrow set, the last cell becomes an arrowhead.
func (c *Canvas) Route(pts []Point, ink Ink, arrow bool) {
	for i := 0; i+1 < len(pts); i++ {
		a, b := pts[i], pts[i+1]
		dx, dy := sign(b.X-a.X), sign(b.Y-a.Y)
		out, in := dirOf(dx, dy), dirOf(-dx, -dy)
		for p := a; p != b; p = (Point{p.X + dx, p.Y + dy}) {
			c.Wire(p.X, p.Y, out, ink)
			c.Wire(p.X+dx, p.Y+dy, in, ink)
		}
	}
	if arrow && len(pts) >= 2 {
		a, b := pts[len(pts)-2], pts[len(pts)-1]
		head := map[Point]rune{{1, 0}: '▶', {-1, 0}: '◀', {0, 1}: '▼', {0, -1}: '▲'}[Point{sign(b.X - a.X), sign(b.Y - a.Y)}]
		if c.in(b.X, b.Y) {
			c.cells[b.Y][b.X] = cell{r: head, ink: ink}
		}
	}
}

func dirOf(dx, dy int) uint8 {
	switch {
	case dx > 0:
		return Right
	case dx < 0:
		return Left
	case dy > 0:
		return Down
	default:
		return Up
	}
}

func sign(v int) int {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	}
	return 0
}

// Lines renders the canvas, one string per row, with trailing blank rows and
// trailing spaces removed.
func (c *Canvas) Lines() []string {
	last := c.H - 1
	for last >= 0 && c.blank(last) {
		last--
	}
	out := make([]string, 0, last+1)
	for y := 0; y <= last; y++ {
		row := c.cells[y]
		end := len(row)
		for end > 0 && row[end-1].r == ' ' {
			end--
		}
		var b strings.Builder
		for x := 0; x < end; {
			ink := row[x].ink
			var run strings.Builder
			for x < end && row[x].ink == ink {
				run.WriteRune(row[x].r)
				x++
			}
			b.WriteString(ink.Render(run.String()))
		}
		out = append(out, b.String())
	}
	return out
}

func (c *Canvas) blank(y int) bool {
	for _, cl := range c.cells[y] {
		if cl.r != ' ' {
			return false
		}
	}
	return true
}

// String renders the canvas with each line indented by indent.
func (c *Canvas) String(indent string) string {
	var b strings.Builder
	for _, l := range c.Lines() {
		b.WriteString(indent + l + "\n")
	}
	return b.String()
}

// Truncate cuts s to n cells, ending with an ellipsis when it was cut.
func Truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if runewidth.StringWidth(s) <= n {
		return s
	}
	return runewidth.Truncate(s, n, "…")
}
