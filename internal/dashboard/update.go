package dashboard

import (
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
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

	case spinner.TickMsg:
		// Advance the working-agent spinner and re-schedule the next frame.
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

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
// printable keys, Backspace and Esc; theme mode routes to the theme
// selector; navigation mode handles movement, focus, filter entry and
// quitting. tmon-owned keys are intercepted here; everything else
// (j/k/up/down/g/G/…) is forwarded to the bubbles list.
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

	if m.themeMode {
		return m.handleThemeKey(msg)
	}

	switch msg.String() {
	case "/":
		m.searching = true
	case "t":
		m.themeMode = true
	case "esc", "q", "ctrl+c":
		return m, tea.Quit
	case "left", "h":
		m.resizePreview(previewResizeStep)
	case "right", "l":
		m.resizePreview(-previewResizeStep)
	case "ctrl+u":
		m.preview.HalfPageUp()
	case "ctrl+d":
		m.preview.HalfPageDown()
	case "enter", " ":
		return m.focusSelected()
	case "b", "w", "i":
		m.toggleStatusFilter(statusKey(msg.String()))
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		m.jumpTo(msg.String())
	default:
		// Everything else is list navigation: j/k/up/down/g/G/home/end.
		var cmd tea.Cmd
		m.agentList, cmd = m.agentList.Update(msg)
		if cmd != nil {
			return m, cmd
		}
		m.refreshPreview(false)
	}
	return m, nil
}

// handleThemeKey dispatches keys in the theme selector: esc/q return to the
// agent list without applying, enter/space apply the highlighted theme (and
// persist it), ctrl+c quits the popup, and everything else (j/k/up/down/
// g/G/…) is forwarded to the themes list, which re-resolves the palette
// preview on selection.
func (m Model) handleThemeKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.themeMode = false
	case "ctrl+c":
		return m, tea.Quit
	case "enter", " ":
		m = m.applyThemeSelection()
		m.themeMode = false
		// Applying replaces the spinner (new theme color); re-arm its tick
		// so the animation keeps running after the old tick chain is orphaned.
		return m, func() tea.Msg { return m.spinner.Tick() }
	default:
		var cmd tea.Cmd
		m.themes, cmd = m.themes.Update(msg)
		if cmd != nil {
			return m, cmd
		}
	}
	return m, nil
}

// handleMouse maps mouse events: drag the │ separator to resize the preview,
// or left-click an agent row to focus its pane (same as Enter). Clicks on
// chrome rows, the preview panel body, and blank padding are ignored. The
// theme selector is keyboard-only.
func (m Model) handleMouse(msg tea.MouseMsg) (Model, tea.Cmd) {
	if m.themeMode {
		return m, nil
	}
	switch msg.Action {
	case tea.MouseActionRelease:
		if m.draggingSplit {
			m.draggingSplit = false
			m.saveSettings()
		}
		return m, nil

	case tea.MouseActionMotion:
		if m.draggingSplit {
			m.setPreviewPctFromX(msg.X)
		}
		return m, nil

	case tea.MouseActionPress:
		// The mouse wheel scrolls the preview panel; the viewport moves
		// itself (up = older content, down = toward the bottom).
		if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
			if m.width > 0 {
				if listW, _ := m.panelWidths(m.width); msg.X >= listW {
					m.preview.Update(msg)
				}
			}
			return m, nil
		}
		if msg.Button != tea.MouseButtonLeft {
			return m, nil
		}
		// Drag the list|preview separator (hit target ±1 cell).
		if m.width > 0 {
			listW, _ := m.panelWidths(m.width)
			if msg.X >= listW-1 && msg.X <= listW+1 {
				m.draggingSplit = true
				m.setPreviewPctFromX(msg.X)
				return m, nil
			}
		}
		// Only clicks on the list column act; the preview panel is a no-op.
		if m.width > 0 {
			_, panelW := m.panelWidths(m.width)
			if listW := m.width - panelW - 1; msg.X >= listW {
				return m, nil
			}
		}
		// Rows 0 (header) and 1 (divider) are chrome; body rows start at 2.
		bodyRow := msg.Y - 2
		if bodyRow < 0 || bodyRow >= m.height-3 {
			return m, nil
		}
		if m.clickAgentAt(bodyRow) {
			return m.focusSelected()
		}
		return m, nil
	}
	return m, nil
}

// clickAgentAt maps a body row to the agent rendered there and selects it.
// Rows are uniform (agentDelegate.Height() lines each); clicks past the
// last visible item are ignored. Reports whether an agent was hit.
func (m *Model) clickAgentAt(bodyRow int) bool {
	const itemH = 4 // agentDelegate.Height()
	idxInView := bodyRow / itemH
	visible := m.agentList.VisibleItems()
	if idxInView < 0 || idxInView >= len(visible) {
		return false
	}
	// Only count fully rendered rows: a partially clipped item cannot be
	// the click target.
	if (idxInView+1)*itemH > m.height-3 {
		return false
	}
	hit, ok := visible[idxInView].(agentItem)
	if !ok {
		return false
	}
	for i, item := range m.agentList.Items() {
		if ai, ok := item.(agentItem); ok && ai.row.PID == hit.row.PID {
			m.agentList.Select(i)
			m.refreshPreview(false)
			return true
		}
	}
	return false
}

// setPreviewPctFromX sets previewPct so the separator sits near column x.
// Does not persist; the caller saves on drag release.
func (m *Model) setPreviewPctFromX(x int) {
	if m.width < 2 {
		return
	}
	// list | sep | preview  →  preview fraction of (width - 1) after the sep.
	listW := x
	if listW < 0 {
		listW = 0
	}
	if listW > m.width-1 {
		listW = m.width - 1
	}
	panelW := m.width - 1 - listW
	pct := (panelW * 100) / (m.width - 1)
	m.previewPct = clampPreviewPct(pct)
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
	if len(m.filtered) == 0 {
		return
	}
	idx := int(n[0] - '1')
	if idx >= len(m.filtered) {
		idx = len(m.filtered) - 1
	}
	m.agentList.Select(idx)
	m.refreshPreview(false)
}

// refreshPreview points the preview panel at the selected agent's pane.
// With force, content is re-read from the cache (or captured if missing)
// even when the pane target is unchanged. Changing panes pins the new pane
// to the bottom of the viewport.
func (m *Model) refreshPreview(force bool) {
	r := m.selectedRow()
	if r == nil {
		m.previewText, m.previewPane = "", ""
		m.preview.SetContent("")
		return
	}
	pane := r.Pane
	if !force && pane == m.previewPane {
		return
	}
	changed := pane != m.previewPane
	m.previewPane = pane
	m.previewText = m.cachedPane(pane)
	// Trim trailing blanks so the bottom pin lands on real content (tmux
	// pane captures end with a newline).
	m.preview.SetContent(strings.Join(trimTrailingEmpty(strings.Split(m.previewText, "\n")), "\n"))
	if changed {
		m.preview.GotoBottom()
	}
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

// focusSelected switches the tmux client to the selected agent's pane and
// quits the popup. Returns no command when nothing is selectable.
func (m Model) focusSelected() (Model, tea.Cmd) {
	r := m.selectedRow()
	if r == nil {
		return m, nil
	}
	return m, m.focusCmd(*r)
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

// rebuildItems rebuilds the flat agent list from the filtered rows (one
// item per agent, in filtered order) and feeds it to the bubbles list.
// Each item carries the agent's stripped pane capture so search can match
// content beyond the visible preview. The bubbles list keeps its cursor
// when items change, so the selection is clamped back into range here.
func (m *Model) rebuildItems() {
	items := make([]list.Item, 0, len(m.filtered))
	for _, fi := range m.filtered {
		r := m.rows[fi]
		capture := ""
		if r.Pane != "" && r.Pane != "?" && m.paneCache != nil {
			capture = ansi.Strip(m.paneCache[r.Pane])
		}
		items = append(items, agentItem{row: r, capture: capture})
	}
	_ = m.agentList.SetItems(items)
	if n := len(items); n > 0 && m.agentList.Index() >= n {
		m.agentList.Select(n - 1)
	}
}
