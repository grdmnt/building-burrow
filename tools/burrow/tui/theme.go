// Package tui is the drawing kit behind the burrow command: a colour palette,
// a character canvas with wires and boxes, status lamps, and the Bubble Tea
// models that animate them. Lip Gloss does the styling; Bubble Tea runs the
// interactive views.
package tui

import (
	"github.com/charmbracelet/lipgloss"
)

// Role names what a colour is for. Each role has one colour on dark
// terminals and another on light ones, so everything drawn with roles stays
// readable on both.
type Role uint8

const (
	NoColor  Role = iota
	Cyan          // accent: current level, selection, headings
	Green         // done, passing
	Red           // failing
	Amber         // parts, optional chapters
	Yellow        // inline code, highlights
	Magenta       // Go keywords
	Blue          // links
	Gray          // secondary text
	Faint         // borders, wires, locked things
	White         // emphasised text
	AmberDim      // locked optional chapters
	OnAccent      // text drawn on a Cyan background
	numRoles
)

// 256-colour palette entries for each role.
type palette [numRoles]string

var (
	darkPalette = palette{
		Cyan: "80", Green: "114", Red: "203", Amber: "215", Yellow: "221", Magenta: "176",
		Blue: "75", Gray: "244", Faint: "239", White: "252", AmberDim: "137", OnAccent: "16",
	}
	lightPalette = palette{
		Cyan: "30", Green: "28", Red: "160", Amber: "166", Yellow: "130", Magenta: "90",
		Blue: "25", Gray: "242", Faint: "250", White: "235", AmberDim: "180", OnAccent: "231",
	}
	current = &darkPalette
	light   bool
)

// SetLight switches every role to the palette for a light (true) or dark
// (false) terminal background.
func SetLight(on bool) {
	light = on
	current = &darkPalette
	if on {
		current = &lightPalette
	}
	// Tell Lip Gloss too, so it never queries the terminal itself.
	lipgloss.SetHasDarkBackground(!on)
}

// Light reports whether the light palette is in use.
func Light() bool { return light }

// Color is the role's Lip Gloss colour in the current palette.
func (r Role) Color() lipgloss.Color {
	if r == NoColor {
		return lipgloss.Color("")
	}
	return lipgloss.Color(current[r])
}

// Code is the role's ANSI SGR parameters, such as "38;5;80".
func (r Role) Code() string {
	if r == NoColor {
		return "39"
	}
	return "38;5;" + current[r]
}

// Ink is how one cell is drawn. It's comparable, so runs of equal ink can be
// rendered together.
type Ink struct {
	Color  Role
	Bold   bool
	Italic bool
}

// Style returns the Lip Gloss style for an ink.
func (i Ink) Style() lipgloss.Style {
	s := lipgloss.NewStyle()
	if i.Color != NoColor {
		s = s.Foreground(i.Color.Color())
	}
	return s.Bold(i.Bold).Italic(i.Italic)
}

// Render paints s with the ink.
func (i Ink) Render(s string) string {
	if i == (Ink{}) {
		return s
	}
	return i.Style().Render(s)
}

// Common inks.
var (
	Plain     = Ink{}
	InkFaint  = Ink{Color: Faint}
	InkGray   = Ink{Color: Gray}
	InkCyan   = Ink{Color: Cyan}
	InkGreen  = Ink{Color: Green}
	InkRed    = Ink{Color: Red}
	InkAmber  = Ink{Color: Amber}
	InkYellow = Ink{Color: Yellow}
	InkBold   = Ink{Bold: true}
)

// WithBold returns a bold copy of the ink.
func (i Ink) WithBold() Ink { i.Bold = true; return i }

func init() {
	// Until the background is known, assume dark, and stop Lip Gloss from
	// asking the terminal on its own: an unanswered query can hang.
	SetLight(false)
}
