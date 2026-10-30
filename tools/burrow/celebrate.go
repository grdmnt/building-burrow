package main

import (
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// switchOnModel plays the moment a level completes: the machine is drawn with
// the new part's lamp flashing and its block lighting up, then it settles.
type switchOnModel struct {
	w     *world
	part  string
	focus *Chapter
	frame int
}

const switchOnFrames = 22

func (m *switchOnModel) Init() tea.Cmd { return boardTick() }

func (m *switchOnModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(msgTick); ok {
		m.frame++
		if m.frame >= switchOnFrames {
			return m, tea.Quit
		}
		return m, boardTick()
	}
	return m, nil
}

func (m *switchOnModel) View() string {
	return machineScreen(m.w, termWidth, machineAnim{part: m.part, frame: m.frame}, m.focus)
}

// switchOn animates level l's part coming online. It must run before l is
// marked complete, since the animation draws the part as newly switched on.
func switchOn(w *world, l *Level) {
	if !animate || termWidth < machineWidth+4 {
		return
	}
	m := &switchOnModel{w: w, part: l.ID, focus: l.ch}
	p := tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(os.Stdout), tea.WithoutSignalHandler())
	done := make(chan struct{})
	go func() { p.Run(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		p.Kill()
	}
}
