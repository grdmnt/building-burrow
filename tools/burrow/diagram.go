package main

import (
	"burrowtool/tui"
)

// lampFor is the lamp that shows a level's part on diagrams.
func lampFor(w *world, l *Level) tui.Lamp {
	switch w.levelState(l) {
	case stDone:
		return tui.LampPass
	case stCurrent:
		return tui.LampRun
	case stOpen:
		return tui.LampOpen
	}
	return tui.LampOff
}

// fitDiagram draws how a level fits: the earlier parts it uses on the left,
// wired into the part it builds on the right.
func fitDiagram(w *world, l *Level, width int) []string {
	var inputs []*Level
	for _, part := range l.Consumes {
		if p := w.cur.producer(part); p != nil {
			inputs = append(inputs, p)
		}
	}
	bw := min(26, (width-8)/2)
	cw := min(30, width-bw-7)
	if cw < 16 {
		return nil
	}
	const ih, gap, ch = 3, 1, 4
	stack := max(len(inputs)*(ih+gap)-gap, ch)
	busX := bw + 2
	cx := busX + 3
	cy := (stack - ch) / 2
	cv := tui.NewCanvas(cx+cw, stack)

	// The part this level builds.
	cv.Box(cx, cy, cw, ch, tui.Heavy, tui.InkCyan, "YOU BUILD · "+l.ID, tui.InkCyan.WithBold())
	cv.TextMax(cx+2, cy+1, l.Unlock, cw-4, tui.InkAmber.WithBold())
	cv.TextMax(cx+2, cy+2, l.Title, cw-4, tui.InkGray)

	if len(inputs) == 0 {
		cv.TextMax(0, cy+1, "nothing plugs in yet:", bw, tui.InkGray)
		cv.TextMax(0, cy+2, "this is a first part", bw, tui.InkGray)
		return cv.Lines()
	}
	target := cy + 1
	for i, in := range inputs {
		y := i * (ih + gap)
		done := w.levelDone(in)
		edge, wire := tui.InkGray, tui.InkFaint
		if done {
			edge, wire = tui.InkGreen, tui.InkGreen
		}
		cv.Box(0, y, bw, ih, tui.Rounded, edge, in.ID, tui.InkGray)
		r, ink := lampFor(w, in).Glyph(0)
		cv.Set(2, y+1, r, ink)
		cv.TextMax(4, y+1, in.Unlock, bw-6, tui.Plain)
		cv.Route([]tui.Point{{X: bw, Y: y + 1}, {X: busX, Y: y + 1}, {X: busX, Y: target}, {X: cx - 1, Y: target}}, wire, true)
	}
	return cv.Lines()
}
