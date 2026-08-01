package dashboard

import (
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
)

// initMsg requests the initial full load.
type initMsg struct{}

// tickMsg fires every refreshInterval; every fullReloadTicks-th tick does a
// full data reload instead of the cheap frame refresh.
type tickMsg struct{}

func tickCmd() tea.Cmd {
	return tea.Tick(refreshInterval, func(time.Time) tea.Msg { return tickMsg{} })
}

// Update handles window resizes, keys, and the auto-refresh cadence.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case initMsg:
		return m.doLoad(ModeFull)

	case tickMsg:
		m.ticks++
		mode := ModeLight
		if m.ticks%fullReloadTicks == 0 {
			mode = ModeFull
		}
		return m.doLoad(mode)

	default:
		return m, nil
	}
}

// doLoad runs a refresh and applies the result; on failure the previous data
// is kept so the popup never blanks out.
func (m Model) doLoad(mode Mode) (Model, tea.Cmd) {
	if m.loader == nil {
		return m, nil
	}
	data, err := m.loader(mode)
	if err != nil {
		return m, nil
	}
	if data.Rows != nil {
		m.rows = data.Rows
		m.rebuildFilter()
	}
	m.frame = data.Frame
	return m, nil
}

// handleKey dispatches a key press in the current mode. Search mode consumes
// printable keys, Backspace and Esc; navigation mode handles movement, focus,
// filter entry and quitting.
func (m Model) handleKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	if m.searching {
		switch msg.String() {
		case "esc":
			m.searching = false // the filter stays applied
		case "backspace":
			if m.query != "" {
				m.query = dropLastRune(m.query)
				m.rebuildFilter()
			}
		case "ctrl+c":
			return m, tea.Quit
		default:
			if len(msg.Runes) > 0 {
				changed := false
				for _, r := range msg.Runes { // handles pasted text too
					if unicode.IsPrint(r) {
						m.query += string(r)
						changed = true
					}
				}
				if changed {
					m.rebuildFilter()
				}
			}
		}
		return m, nil
	}

	switch msg.String() {
	case "/":
		m.searching = true
	case "esc", "q", "ctrl+c":
		return m, tea.Quit
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "enter", " ", "l", "right":
		return m.focusSelected()
	}
	return m, nil
}

// move steps the selection by delta, wrapping at the ends like the bash
// popup's modulo navigation.
func (m *Model) move(delta int) {
	n := len(m.selMap)
	if n == 0 {
		return
	}
	m.selected = (m.selected + delta + n) % n
}

// focusSelected switches the tmux client to the selected agent's pane and
// quits the popup. Returns no command when nothing is selectable.
func (m Model) focusSelected() (Model, tea.Cmd) {
	if len(m.selMap) == 0 {
		return m, nil
	}
	it := m.items[m.selMap[m.selected]]
	if it.kind != itemAgent {
		return m, nil // safety: only agent lines are selectable
	}
	return m, m.focusCmd(m.rows[it.rowIdx])
}

// dropLastRune removes the final rune of s (backspace semantics — the query
// can contain multi-byte runes).
func dropLastRune(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	return string(r[:len(r)-1])
}

// noneID is a sentinel that can never equal a tmux session/window id.
const noneID = "\x00"

// rebuildFilter re-applies the query (case-insensitive, matching the agent's
// full name, session name and window name — exactly what the bash popup
// searched) and rebuilds the grouped display items.
func (m *Model) rebuildFilter() {
	m.filtered = m.filtered[:0]
	if m.query == "" {
		for i := range m.rows {
			m.filtered = append(m.filtered, i)
		}
	} else {
		q := strings.ToLower(m.query)
		for i, r := range m.rows {
			hay := strings.ToLower(agentFullName(r.Label) + " " + r.SessionName + " " + r.WindowName)
			if strings.Contains(hay, q) {
				m.filtered = append(m.filtered, i)
			}
		}
	}
	m.rebuildItems()
}

// rebuildItems groups the filtered agents into session and window headers
// with selectable agent lines, then clamps the selection to the new range.
// Grouping keys on the session id and window index (not their display names),
// matching the bash popup.
func (m *Model) rebuildItems() {
	m.items = m.items[:0]
	m.selMap = m.selMap[:0]

	lastSession, lastWindow := noneID, noneID
	for _, fi := range m.filtered {
		r := m.rows[fi]
		if r.SessionID != lastSession {
			m.items = append(m.items, item{kind: itemSession, sessionName: r.SessionName})
			lastSession, lastWindow = r.SessionID, noneID
		}
		if r.WindowIndex != lastWindow {
			m.items = append(m.items, item{kind: itemWindow, windowIdx: r.WindowIndex, windowName: r.WindowName})
			lastWindow = r.WindowIndex
		}
		m.selMap = append(m.selMap, len(m.items))
		m.items = append(m.items, item{kind: itemAgent, rowIdx: fi})
	}

	if len(m.selMap) == 0 {
		m.selected = 0
	} else if m.selected >= len(m.selMap) {
		m.selected = len(m.selMap) - 1
	}
}
