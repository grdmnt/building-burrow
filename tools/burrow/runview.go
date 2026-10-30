package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"burrowtool/tui"
)

// A runView shows a test run as it happens. On a terminal it's a Bubble Tea
// board of lamps, one per test, with results printed above it; elsewhere it
// just prints the result lines.
type runView interface {
	phase(p string)
	start(key, name string)
	finish(key, status, line string)
	done(ok bool)
}

func newRunView(quiet bool) runView {
	if !animate {
		return plainView{quiet: quiet}
	}
	m := &boardModel{start: time.Now(), phase: "Compiling", state: map[string]tui.Lamp{}, quiet: quiet}
	p := tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(os.Stdout), tea.WithoutSignalHandler())
	v := &teaView{p: p, exited: make(chan struct{})}
	go func() {
		p.Run()
		close(v.exited)
	}()
	return v
}

type plainView struct{ quiet bool }

func (plainView) phase(string)         {}
func (plainView) start(string, string) {}
func (v plainView) finish(_, _, line string) {
	if !v.quiet && line != "" {
		fmt.Println(line)
	}
}
func (plainView) done(bool) {}

type teaView struct {
	p      *tea.Program
	exited chan struct{}
}

type (
	msgPhase  string
	msgStart  struct{ key, name string }
	msgFinish struct{ key, status, line string }
	msgDone   struct{ ok bool }
	msgTick   struct{}
)

func (v *teaView) phase(p string)           { v.p.Send(msgPhase(p)) }
func (v *teaView) start(key, name string)   { v.p.Send(msgStart{key, name}) }
func (v *teaView) finish(k, s, line string) { v.p.Send(msgFinish{k, s, line}) }
func (v *teaView) done(ok bool) {
	v.p.Send(msgDone{ok})
	select {
	case <-v.exited:
	case <-time.After(3 * time.Second):
		v.p.Kill()
		<-v.exited
	}
}

// boardModel is the lamp board: every top-level test is a lamp that goes from
// running (cyan) to passed (green) or failed (red). When the whole run passes,
// a light sweeps across the board.
type boardModel struct {
	start   time.Time
	phase   string
	keys    []string
	names   map[string]string
	state   map[string]tui.Lamp
	pass    int
	fail    int
	skip    int
	running string
	frame   int
	done    bool
	sweep   int // frames of the all-green sweep left; 0 when not sweeping
	quiet   bool
}

const sweepFrames = 14

func boardTick() tea.Cmd {
	return tea.Tick(70*time.Millisecond, func(time.Time) tea.Msg { return msgTick{} })
}

func (m *boardModel) Init() tea.Cmd { return boardTick() }

func (m *boardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case msgTick:
		m.frame++
		if m.done {
			if m.sweep--; m.sweep <= 0 {
				return m, tea.Quit
			}
		}
		return m, boardTick()
	case msgPhase:
		m.phase = string(msg)
	case msgStart:
		if _, ok := m.state[msg.key]; !ok {
			m.keys = append(m.keys, msg.key)
			if m.names == nil {
				m.names = map[string]string{}
			}
			m.names[msg.key] = msg.name
		}
		m.state[msg.key] = tui.LampRun
		m.running = msg.name
	case msgFinish:
		if _, ok := m.state[msg.key]; !ok {
			m.keys = append(m.keys, msg.key)
		}
		switch msg.status {
		case "pass":
			m.state[msg.key] = tui.LampPass
			m.pass++
		case "fail":
			m.state[msg.key] = tui.LampFail
			m.fail++
		default:
			m.state[msg.key] = tui.LampSkip
			m.skip++
		}
		if !m.quiet && msg.line != "" {
			return m, tea.Println(msg.line)
		}
	case msgDone:
		m.done = true
		if msg.ok && len(m.keys) > 0 {
			m.sweep = sweepFrames
			return m, nil
		}
		return m, tea.Quit
	}
	return m, nil
}

func (m *boardModel) View() string {
	perRow := max(8, (min(termWidth, 100)-6)/2)
	var b strings.Builder
	head := (sweepFrames - m.sweep) * (len(m.keys) + 6) / sweepFrames
	for i, k := range m.keys {
		if i%perRow == 0 {
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString("  ")
		}
		lamp := m.state[k]
		if m.done && m.sweep > 0 && lamp == tui.LampPass && i <= head && i > head-4 {
			b.WriteString(tui.Ink{Color: tui.Yellow, Bold: true}.Render("◆") + " ")
			continue
		}
		b.WriteString(lamp.Render(m.frame) + " ")
	}
	if len(m.keys) > 0 {
		b.WriteString("\n")
	}
	if m.done {
		return b.String()
	}
	sp := cyan(spinFrames[m.frame%len(spinFrames)])
	counts := ""
	if m.pass+m.fail+m.skip > 0 {
		counts = "  " + green(fmt.Sprint(m.pass)) + gray(" passed") + ifs(m.fail > 0, "  "+red(fmt.Sprint(m.fail))+gray(" failed"))
	}
	now := ""
	if m.running != "" && m.phase == "Testing" {
		now = gray("  · " + truncate(humanize(shortName(m.running)), 32))
	}
	b.WriteString("  " + sp + " " + bold(m.phase) + gray("  "+duration(time.Since(m.start))) + counts + now)
	return b.String()
}
