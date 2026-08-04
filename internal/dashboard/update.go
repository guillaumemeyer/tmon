package dashboard

import (
	"sort"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/guillaumemeyer/tmon/internal/agent"
)

// initMsg requests the initial full load.
type initMsg struct{}

// tickMsg fires every refreshInterval and triggers a full data reload.
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

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case initMsg:
		return m.doLoad()

	case tickMsg:
		return m.doLoad()

	default:
		return m, nil
	}
}

// doLoad runs a refresh and applies the result; on failure the previous data
// is kept so the popup never blanks out.
func (m Model) doLoad() (Model, tea.Cmd) {
	if m.loader == nil {
		return m, nil
	}
	data, err := m.loader()
	if err != nil {
		return m, nil
	}
	m.rows = data.Rows
	// Capture every agent pane so fuzzy search can match preview content
	// that is not currently visible in the right-hand panel.
	m.refreshPaneCache()
	m.rebuildFilter()
	// Point the preview panel at the (possibly moved) selection using the
	// cache filled above.
	m.refreshPreview(true)
	return m, nil
}

// refreshPaneCache re-captures every agent pane into paneCache so search
// can match against full pane text. Stale targets are pruned.
func (m *Model) refreshPaneCache() {
	if m.paneCache == nil {
		m.paneCache = make(map[string]string)
	}
	live := make(map[string]bool, len(m.rows))
	for _, r := range m.rows {
		if r.Pane == "" || r.Pane == "?" {
			continue
		}
		live[r.Pane] = true
		m.paneCache[r.Pane] = capturePane(r.Pane)
	}
	for k := range m.paneCache {
		if !live[k] {
			delete(m.paneCache, k)
		}
	}
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
	case "left", "h":
		m.resizePreview(previewResizeStep)
	case "right", "l":
		m.resizePreview(-previewResizeStep)
	case "ctrl+u":
		m.scrollPreview(m.previewScrollStep())
	case "ctrl+d":
		m.scrollPreview(-m.previewScrollStep())
	case "enter", " ":
		return m.focusSelected()
	case "b", "w", "i":
		m.toggleStatusFilter(statusKey(msg.String()))
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		m.jumpTo(msg.String())
	}
	return m, nil
}

// handleMouse maps a left click in the popup viewport to a list row. A click
// on an agent line selects it and immediately focuses its pane (the same
// action as Enter). Clicks on session/window headers, the preview panel, and
// the chrome rows (header, divider, footer) are ignored.
func (m Model) handleMouse(msg tea.MouseMsg) (Model, tea.Cmd) {
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return m, nil
	}
	// Rows 0 (header) and 1 (divider) are chrome; body rows start at 2.
	bodyRow := msg.Y - 2
	di := m.itemAtRow(bodyRow, m.height-3)
	if di < 0 {
		return m, nil
	}
	// Only clicks on the list column act; the preview panel is a no-op.
	if m.width > 0 {
		_, panelW := m.panelWidths(m.width)
		if listW := m.width - panelW - 1; msg.X >= listW {
			return m, nil
		}
	}
	if m.items[di].kind != itemAgent {
		return m, nil // session/window headers are not clickable
	}
	for i, sel := range m.selMap {
		if sel == di {
			m.selected = i
			return m.focusSelected()
		}
	}
	return m, nil
}

// itemAtRow returns the index into m.items of the item rendered at the
// given body row (0-based, below the header/divider), or -1 when the row is
// blank padding or past the last fully rendered item. Agent items occupy
// two rows (name + cwd/pause line) — three when the agent has usage stats —
// while session and window headers take one.
func (m Model) itemAtRow(row, bodyLines int) int {
	if row < 0 || bodyLines < 1 || len(m.filtered) == 0 {
		return -1
	}
	line := 0
	for di, it := range m.items {
		n := 1
		if it.kind == itemAgent {
			n = 2
			if !m.rows[it.rowIdx].Usage.Empty() {
				n = 3
			}
		}
		if row < line+n {
			if line+n <= bodyLines {
				return di // the item was rendered in full
			}
			return -1 // past the last rendered item (list overflow)
		}
		line += n
	}
	return -1
}

// resizePreview grows or shrinks the preview panel by deltaPct percentage
// points and persists the new width when it changes.
func (m *Model) resizePreview(deltaPct int) {
	if deltaPct == 0 {
		return
	}
	next := clampPreviewPct(m.previewPct + deltaPct)
	if next == m.previewPct {
		return
	}
	m.previewPct = next
	m.saveSettings()
}

// previewScrollStep is half the preview body height (at least 1), matching
// vim-style ctrl+u / ctrl+d half-page scrolling.
func (m Model) previewScrollStep() int {
	body := m.height - 3 // header + divider + footer
	if body < 1 {
		body = 1
	}
	// One row is the preview header; the rest is content.
	content := body - 1
	if content < 1 {
		return 1
	}
	step := content / 2
	if step < 1 {
		return 1
	}
	return step
}

// scrollPreview moves the preview window by delta lines (positive = up
// toward older content, negative = down toward the bottom). The offset is
// clamped against the current capture length and viewport size.
func (m *Model) scrollPreview(delta int) {
	if delta == 0 {
		return
	}
	content := trimTrailingEmpty(strings.Split(m.previewText, "\n"))
	if len(content) == 1 && content[0] == "" {
		content = nil
	}
	body := m.height - 3
	if body < 1 {
		body = 1
	}
	visible := body - 1
	if visible < 1 {
		visible = 1
	}
	maxOff := len(content) - visible
	if maxOff < 0 {
		maxOff = 0
	}
	off := m.previewOffset + delta
	if off < 0 {
		off = 0
	}
	if off > maxOff {
		off = maxOff
	}
	m.previewOffset = off
}

// statusKey maps a filter key to its status: b=blocked, w=working, i=idle.
func statusKey(k string) agent.Status {
	switch k {
	case "b":
		return agent.StatusBlocked
	case "w":
		return agent.StatusWorking
	case "i":
		return agent.StatusIdle
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

// refreshPreview points the preview panel at the selected agent's pane.
// With force, content is re-read from the cache (or captured if missing)
// even when the pane target is unchanged. Changing panes resets the scroll
// offset so the new pane pins to the bottom again.
func (m *Model) refreshPreview(force bool) {
	if len(m.selMap) == 0 {
		m.previewText, m.previewPane, m.previewOffset = "", "", 0
		return
	}
	it := m.items[m.selMap[m.selected]]
	if it.kind != itemAgent {
		m.previewText, m.previewPane, m.previewOffset = "", "", 0
		return
	}
	pane := m.rows[it.rowIdx].Pane
	if !force && pane == m.previewPane {
		return
	}
	if pane != m.previewPane {
		m.previewOffset = 0
	}
	m.previewPane = pane
	m.previewText = m.cachedPane(pane)
}

// cachedPane returns the capture for pane, filling the cache on a miss.
func (m *Model) cachedPane(pane string) string {
	if pane == "" || pane == "?" {
		return ""
	}
	if m.paneCache != nil {
		if text, ok := m.paneCache[pane]; ok {
			return text
		}
	}
	text := capturePane(pane)
	if m.paneCache == nil {
		m.paneCache = make(map[string]string)
	}
	m.paneCache[pane] = text
	return text
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

// rebuildFilter re-applies the query with Telescope/fzy-style fuzzy matching
// over the session title, agent name, working directory, and full pane
// capture (including content not currently visible in the preview panel),
// plus the optional status filter. With a non-empty query, matches are
// ranked by score (best first). Then rebuilds the grouped display items.
func (m *Model) rebuildFilter() {
	type scored struct {
		idx   int
		score int
	}
	var matches []scored
	for i, r := range m.rows {
		if m.filterStatus != "" && r.Status != m.filterStatus {
			continue
		}
		if m.query == "" {
			matches = append(matches, scored{idx: i, score: 0})
			continue
		}
		if s := m.agentSearchScore(r); s >= 0 {
			matches = append(matches, scored{idx: i, score: s})
		}
	}
	if m.query != "" {
		// Stable rank: higher score first; ties keep original row order.
		sort.SliceStable(matches, func(i, j int) bool {
			return matches[i].score > matches[j].score
		})
	}
	m.filtered = m.filtered[:0]
	for _, s := range matches {
		m.filtered = append(m.filtered, s.idx)
	}
	m.rebuildItems()
}

// agentSearchScore returns the best fuzzy score for query against the
// agent's session title, display name (includes Hermes profile), CWD
// (absolute and display forms), and pane capture text. Returns -1 when
// nothing matches.
func (m *Model) agentSearchScore(r Row) int {
	q := m.query
	title := r.Title
	name := agentDisplayName(r)
	cwd := r.CWD
	if disp := displayCWD(r.CWD); disp != "" && disp != cwd {
		cwd = cwd + " " + disp
	}
	preview := ""
	if r.Pane != "" && r.Pane != "?" && m.paneCache != nil {
		preview = ansi.Strip(m.paneCache[r.Pane])
	}

	best := -1
	for _, field := range []string{title, name, r.Profile, cwd, preview} {
		if field == "" {
			continue
		}
		if s := fuzzyScoreTerms(q, field); s > best {
			best = s
		}
	}
	// Also allow a term to hit across fields (e.g. name + path fragments)
	// by scoring the concatenated haystack — but only if per-field failed,
	// or if it scores higher.
	hay := title + "\n" + name + "\n" + r.Profile + "\n" + cwd + "\n" + preview
	if s := fuzzyScoreTerms(q, hay); s > best {
		best = s
	}
	return best
}

// rebuildItems rebuilds the display list from filtered agents, then clamps
// the selection. With an active search query the list is flat and
// score-ordered (Telescope-style); otherwise agents are grouped by
// session → window → agent.
func (m *Model) rebuildItems() {
	m.items = m.items[:0]
	m.selMap = m.selMap[:0]

	if m.query != "" {
		for _, fi := range m.filtered {
			m.selMap = append(m.selMap, len(m.items))
			m.items = append(m.items, item{kind: itemAgent, rowIdx: fi})
		}
	} else {
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

	if len(m.selMap) == 0 {
		m.selected = 0
	} else if m.selected >= len(m.selMap) {
		m.selected = len(m.selMap) - 1
	}
}
