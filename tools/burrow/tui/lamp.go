package tui

// Lamp is the state of one indicator light: a test, a part, a level.
type Lamp int

const (
	LampOff Lamp = iota
	LampRun
	LampPass
	LampFail
	LampSkip
	LampNew  // just switched on; flashes
	LampOpen // ready to start
)

// Glyph returns the rune and ink for a lamp. frame animates running and new
// lamps; pass 0 for a still picture.
func (l Lamp) Glyph(frame int) (rune, Ink) {
	switch l {
	case LampRun:
		if frame%6 < 3 {
			return '◉', InkCyan
		}
		return '◎', InkCyan
	case LampPass:
		return '●', InkGreen
	case LampFail:
		return '●', InkRed
	case LampSkip:
		return '◌', InkGray
	case LampNew:
		if frame%4 < 2 {
			return '●', Ink{Color: Yellow, Bold: true}
		}
		return '○', InkAmber
	case LampOpen:
		return '○', Ink{Color: White}
	}
	return '○', InkFaint
}

// String renders the lamp as styled text.
func (l Lamp) String() string {
	r, ink := l.Glyph(0)
	return ink.Render(string(r))
}

// Render renders the lamp at an animation frame.
func (l Lamp) Render(frame int) string {
	r, ink := l.Glyph(frame)
	return ink.Render(string(r))
}
