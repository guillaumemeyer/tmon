package dashboard

import (
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/guillaumemeyer/tmon/internal/agent"
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
		// The list changed: re-capture the preview for the (possibly moved)
		// selection even if the pane target is unchanged.
		m.refreshPreview(true)
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
	case "g":
		m.cycleGroup()
	case "d":
		m.togglePreview()
	case "b", "w", "i", "a":
		m.toggleStatusFilter(statusKey(msg.String()))
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		m.jumpTo(msg.String())
	}
	return m, nil
}

// statusKey maps a filter key to its status: b=blocked, w=working (running),
// i=idle, a=active.
func statusKey(k string) agent.Status {
	switch k {
	case "b":
		return agent.StatusBlocked
	case "w":
		return agent.StatusRunning
	case "i":
		return agent.StatusIdle
	case "a":
		return agent.StatusActive
	}
	return ""
}

// toggleStatusFilter toggles the status filter: pressing the active filter's
// key again clears it, pressing another switches.
func (m *Model) toggleStatusFilter(st agent.Status) {
	if m.filterStatus == st {
		m.filterStatus = ""
	} else {
		m.filterStatus = st
	}
	m.rebuildFilter()
}

// cycleGroup advances the grouping mode: session → status → agent → session.
func (m *Model) cycleGroup() {
	m.groupMode = (m.groupMode + 1) % 3
	m.rebuildItems()
}

// jumpTo selects the Nth (1-based) agent in the filtered list.
func (m *Model) jumpTo(n string) {
	idx := int(n[0] - '1')
	if len(m.selMap) == 0 {
		return
	}
	if idx >= len(m.selMap) {
		idx = len(m.selMap) - 1
	}
	m.selected = idx
	m.refreshPreview(false)
}

// togglePreview turns the pane preview panel on/off, capturing the selected
// pane when it turns on.
func (m *Model) togglePreview() {
	m.preview = !m.preview
	if m.preview {
		m.refreshPreview(true)
	} else {
		m.previewText, m.previewPane = "", ""
	}
}

// refreshPreview re-captures the selected agent's pane for the preview
// panel. With force, the capture runs even if the pane target is unchanged
// (a full reload may have new content); without it, an unchanged selection
// keeps the existing capture.
func (m *Model) refreshPreview(force bool) {
	if !m.preview {
		return
	}
	if len(m.selMap) == 0 {
		m.previewText, m.previewPane = "", ""
		return
	}
	it := m.items[m.selMap[m.selected]]
	if it.kind != itemAgent {
		m.previewText, m.previewPane = "", ""
		return
	}
	pane := m.rows[it.rowIdx].Pane
	if !force && pane == m.previewPane {
		return
	}
	m.previewPane = pane
	m.previewText = capturePane(pane)
}

// move steps the selection by delta, wrapping at the ends like the bash
// popup's modulo navigation.
func (m *Model) move(delta int) {
	n := len(m.selMap)
	if n == 0 {
		return
	}
	m.selected = (m.selected + delta + n) % n
	m.refreshPreview(false)
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
// searched) plus the optional status filter, then rebuilds the grouped
// display items.
func (m *Model) rebuildFilter() {
	m.filtered = m.filtered[:0]
	for i, r := range m.rows {
		if m.filterStatus != "" && r.Status != m.filterStatus {
			continue
		}
		if m.query == "" {
			m.filtered = append(m.filtered, i)
			continue
		}
		q := strings.ToLower(m.query)
		hay := strings.ToLower(agentFullName(r.Label) + " " + r.SessionName + " " + r.WindowName)
		if strings.Contains(hay, q) {
			m.filtered = append(m.filtered, i)
		}
	}
	m.rebuildItems()
}

// rebuildItems groups the filtered agents according to the current group
// mode, then clamps the selection to the new range.
func (m *Model) rebuildItems() {
	m.items = m.items[:0]
	m.selMap = m.selMap[:0]

	switch m.groupMode {
	case groupStatus:
		m.groupByStatus()
	case groupAgent:
		m.groupByAgent()
	default:
		m.groupBySession()
	}

	if len(m.selMap) == 0 {
		m.selected = 0
	} else if m.selected >= len(m.selMap) {
		m.selected = len(m.selMap) - 1
	}
}

// groupBySession groups agents into session and window headers with
// selectable agent lines. Grouping keys on the session id and window index
// (not their display names), matching the bash popup.
func (m *Model) groupBySession() {
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
}

// groupByStatus groups agents under a status header per state, most urgent
// first (blocked, active, running, idle).
func (m *Model) groupByStatus() {
	for _, st := range statusOrder {
		started := false
		for _, fi := range m.filtered {
			if m.rows[fi].Status != st {
				continue
			}
			if !started {
				m.items = append(m.items, item{kind: itemStatus, status: st})
				started = true
			}
			m.selMap = append(m.selMap, len(m.items))
			m.items = append(m.items, item{kind: itemAgent, rowIdx: fi})
		}
	}
}

// groupByAgent renders a flat agent list with no headers.
func (m *Model) groupByAgent() {
	for _, fi := range m.filtered {
		m.selMap = append(m.selMap, len(m.items))
		m.items = append(m.items, item{kind: itemAgent, rowIdx: fi})
	}
}
