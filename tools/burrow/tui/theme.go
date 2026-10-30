// Package tui is the drawing kit behind the burrow command: a colour palette,
// a character canvas with wires and boxes, status lamps, and the Bubble Tea
// models that animate them. Lip Gloss does the styling; Bubble Tea runs the
// interactive views.
package tui

import "github.com/charmbracelet/lipgloss"

// The palette. Everything the CLI draws uses these, so the look stays consistent.
var (
	Cyan    = lipgloss.Color("80")
	Green   = lipgloss.Color("114")
	Red     = lipgloss.Color("203")
	Amber   = lipgloss.Color("215")
	Yellow  = lipgloss.Color("221")
	Magenta = lipgloss.Color("176")
	Blue    = lipgloss.Color("75")
	Gray    = lipgloss.Color("244")
	Faint   = lipgloss.Color("239")
	White   = lipgloss.Color("252")
)

// Ink is how one cell is drawn. It's comparable, so runs of equal ink can be
// rendered together.
type Ink struct {
	Color  lipgloss.Color
	Bold   bool
	Italic bool
}

// Style returns the Lip Gloss style for an ink.
func (i Ink) Style() lipgloss.Style {
	s := lipgloss.NewStyle()
	if i.Color != "" {
		s = s.Foreground(i.Color)
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
	// Burrow's palette is fixed, so there's no need to ask the terminal for
	// its background colour. Asking writes an escape query and waits for the
	// answer, which some terminals never send.
	lipgloss.SetHasDarkBackground(true)
}

// Color is a 256-colour palette entry, for the odd colour outside the palette.
func Color(code string) lipgloss.Color { return lipgloss.Color(code) }
