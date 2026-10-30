package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"burrowtool/tui"
)

// The browser is the full-screen home of burrow: the level list on the left,
// the selected level's page on the right, and tabs for the machine and the
// chapter map. Actions that print (start, test, watch) quit the browser first
// and then run in the normal terminal.

type browseTab int

const (
	tabLevels browseTab = iota
	tabMachine
	tabMap
)

type browseModel struct {
	w      *world
	levels []*Level // selectable rows, in list order
	sel    int
	tab    browseTab
	onPage bool // keyboard focus is on the page, not the list
	page   viewport.Model
	full   viewport.Model // machine and map tabs
	width  int
	height int
	action string // what to run after quitting: start, test, watch
	note   string // one-line message in the help bar
}

const listW = 34

func newBrowse(w *world) *browseModel {
	m := &browseModel{w: w, levels: w.cur.flat}
	cur := w.prog.Current
	if cur == "" {
		if av := w.available(); len(av) > 0 {
			cur = av[0].ID
		}
	}
	for i, l := range m.levels {
		if l.ID == cur {
			m.sel = i
		}
	}
	return m
}

func (m *browseModel) Init() tea.Cmd { return nil }

func (m *browseModel) selected() *Level { return m.levels[m.sel] }

func (m *browseModel) layout() {
	bodyH := max(3, m.height-4)
	m.page = viewport.New(max(20, m.width-listW-3), bodyH)
	m.page.MouseWheelEnabled = true
	m.full = viewport.New(max(20, m.width-2), bodyH)
	m.full.MouseWheelEnabled = true
	m.refresh()
}

// refresh redraws the page and the full-width tab for the current selection.
func (m *browseModel) refresh() {
	l := m.selected()
	w := m.page.Width
	src, _ := os.ReadFile(m.w.cur.levelDir(l) + "/README.md")
	head := "  " + gray(chapterLabel(l.ch)+" · "+l.ch.Name) + gray("  ·  ") + stateWord(m.w, l) + gray("  ·  ") + pips(l.Difficulty) +
		"\n  " + gray("builds ") + amber(bold(l.Unlock))
	m.page.SetContent(head + "\n\n" + levelBody(m.w, l, string(src), w-4))
	m.page.GotoTop()
	switch m.tab {
	case tabMachine:
		m.full.SetContent(machineScreen(m.w, m.full.Width, machineAnim{}, l.ch))
	case tabMap:
		if m.full.Width < mapWidth+4 {
			m.full.SetContent("\n  " + gray("Widen the terminal to see the map."))
		} else {
			m.full.SetContent("\n" + mapString(m.w))
		}
	}
}

func stateWord(w *world, l *Level) string {
	switch w.levelState(l) {
	case stDone:
		return green("complete")
	case stCurrent:
		return cyan("in progress")
	case stOpen:
		return "ready to start"
	}
	return gray("locked: " + w.lockReason(l))
}

func (m *browseModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, nil
	case tea.KeyMsg:
		m.note = ""
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			if msg.String() == "esc" && m.onPage {
				m.onPage = false
				return m, nil
			}
			return m, tea.Quit
		case "1":
			m.tab = tabLevels
			return m, nil
		case "2", "m":
			m.tab = tabMachine
			m.refresh()
			return m, nil
		case "3":
			m.tab = tabMap
			m.refresh()
			return m, nil
		case "tab":
			m.tab = (m.tab + 1) % 3
			m.refresh()
			return m, nil
		case "s", "t", "w":
			return m.act(msg.String())
		}
		if m.tab != tabLevels {
			var cmd tea.Cmd
			m.full, cmd = m.full.Update(msg)
			return m, cmd
		}
		if m.onPage {
			switch msg.String() {
			case "left", "h", "backspace":
				m.onPage = false
				return m, nil
			}
			var cmd tea.Cmd
			m.page, cmd = m.page.Update(msg)
			return m, cmd
		}
		switch msg.String() {
		case "up", "k":
			m.move(-1)
		case "down", "j":
			m.move(1)
		case "pgup":
			m.move(-10)
		case "pgdown":
			m.move(10)
		case "home", "g":
			m.move(-len(m.levels))
		case "end", "G":
			m.move(len(m.levels))
		case "right", "l", "enter", " ":
			m.onPage = true
		}
		return m, nil
	case tea.MouseMsg:
		var cmd tea.Cmd
		if m.tab == tabLevels {
			m.page, cmd = m.page.Update(msg)
		} else {
			m.full, cmd = m.full.Update(msg)
		}
		return m, cmd
	}
	return m, nil
}

func (m *browseModel) move(d int) {
	m.sel = max(0, min(len(m.levels)-1, m.sel+d))
	m.refresh()
}

// act quits the browser to run a command on the selected level, or explains
// why it can't.
func (m *browseModel) act(key string) (tea.Model, tea.Cmd) {
	l := m.selected()
	st := m.w.levelState(l)
	switch {
	case st == stLocked:
		m.note = l.ID + " is locked: " + m.w.lockReason(l)
		return m, nil
	case key == "s" && st == stDone:
		m.note = l.ID + " is already complete"
		return m, nil
	case key == "t" && !m.w.cur.hasTests(l):
		m.note = l.ID + " has no tests yet"
		return m, nil
	}
	m.action = map[string]string{"s": "start", "t": "test", "w": "watch"}[key]
	return m, tea.Quit
}

// listLines renders the level list, scrolled so the selection is visible.
func (m *browseModel) listLines(h int) []string {
	var rows []string
	selRow := 0
	var lastCh *Chapter
	for i, l := range m.levels {
		if l.ch != lastCh {
			lastCh = l.ch
			done := 0
			for _, x := range l.ch.Levels {
				if m.w.levelDone(x) {
					done++
				}
			}
			name := chapterLabel(l.ch) + " " + l.ch.Name
			ink := tui.InkGray.WithBold()
			if l.ch.Optional {
				ink = tui.InkAmber
			}
			count := fmt.Sprintf("%d/%d", done, len(l.ch.Levels))
			rows = append(rows, ink.Render(tui.Truncate(name, listW-7))+strings.Repeat(" ", max(1, listW-3-visibleLen(tui.Truncate(name, listW-7))-len(count)))+tui.InkFaint.Render(count))
		}
		lamp := lampFor(m.w, l).String()
		text := tui.Truncate(l.ID+" "+l.Title, listW-6)
		row := " " + lamp + " " + text
		if i == m.sel {
			selRow = len(rows)
			st := lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(tui.Cyan).Bold(true)
			if m.onPage {
				st = lipgloss.NewStyle().Foreground(tui.Cyan).Bold(true)
			}
			row = " " + lamp + " " + st.Render(" "+text+" ")
		} else if m.w.levelState(l) == stLocked {
			row = " " + lamp + " " + faint(text)
		}
		rows = append(rows, row)
	}
	top := max(0, min(selRow-h/3, len(rows)-h))
	end := min(len(rows), top+h)
	return rows[top:end]
}

func (m *browseModel) View() string {
	if m.width == 0 {
		return ""
	}
	tabs := []string{"1 Levels", "2 Machine", "3 Map"}
	var tb []string
	for i, t := range tabs {
		if browseTab(i) == m.tab {
			tb = append(tb, lipgloss.NewStyle().Foreground(tui.Cyan).Bold(true).Underline(true).Render(t))
		} else {
			tb = append(tb, gray(t))
		}
	}
	top := " " + bold(cyan("▞▚ ")) + bold("BUILDING BURROW") + "   " + strings.Join(tb, "   ")
	bodyH := m.height - 4
	var body string
	if m.tab == tabLevels {
		listBorder := tui.Cyan
		pageBorder := tui.Faint
		if m.onPage {
			listBorder, pageBorder = tui.Faint, tui.Cyan
		}
		list := lipgloss.NewStyle().Width(listW-2).Height(bodyH).
			Border(lipgloss.RoundedBorder(), false, true, false, false).BorderForeground(listBorder).
			Render(strings.Join(m.listLines(bodyH), "\n"))
		page := lipgloss.NewStyle().Border(lipgloss.RoundedBorder(), false, false, false, false).BorderForeground(pageBorder).
			Render(m.page.View())
		body = lipgloss.JoinHorizontal(lipgloss.Top, list, " ", page)
	} else {
		body = m.full.View()
	}
	keys := func(k, label string) string { return bold(cyan(k)) + gray(" "+label) }
	help := keys("↑↓", "select") + "  " + keys("⏎/→", "read") + "  " + keys("←", "back") + "  " + keys("tab", "view") + "  " +
		keys("s", "start") + "  " + keys("t", "test") + "  " + keys("w", "watch") + "  " + keys("q", "quit")
	if m.tab != tabLevels {
		help = keys("↑↓ pgup pgdn", "scroll") + "  " + keys("tab", "view") + "  " + keys("1 2 3", "tabs") + "  " +
			keys("s", "start") + "  " + keys("t", "test") + "  " + keys("w", "watch") + "  " + keys("q", "quit")
	} else if m.onPage {
		help = keys("↑↓ pgup pgdn", "scroll") + "  " + keys("←", "back to list") + "  " + keys("s", "start") + "  " +
			keys("t", "test") + "  " + keys("w", "watch") + "  " + keys("q", "quit")
	}
	if m.note != "" {
		help = amber(m.note)
	}
	scroll := ""
	if m.tab == tabLevels && m.onPage {
		scroll = gray(fmt.Sprintf("  %3.0f%%", m.page.ScrollPercent()*100))
	}
	return top + "\n" + faint(strings.Repeat("─", m.width)) + "\n" + body + "\n" + " " + help + scroll
}

func cmdBrowse(w *world) error {
	m := newBrowse(w)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		return err
	}
	if m.action == "" {
		return nil
	}
	l := m.selected()
	switch m.action {
	case "start":
		return cmdStart(w, []string{l.ID})
	case "test":
		return cmdTest(w, []string{l.ID})
	case "watch":
		if w.levelState(l) == stOpen && !w.levelDone(l) {
			if err := startLevel(w, l, false); err != nil {
				return err
			}
		}
		return cmdWatch(w, []string{l.ID})
	}
	return nil
}
