package dashboard

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
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
	// Full multi-pane capture is only needed while the user is searching
	// (fuzzy match over pane text). Otherwise the selected-row preview
	// alone is enough and avoids N capture-pane spawns per tick.
	if m.searching {
		m.refreshPaneCache()
	}
	m.rebuildFilter()
	// Point the preview panel at the (possibly moved) selection.
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

// handleKey dispatches a key press in the current mode. Search mode
// consumes printable keys and Backspace for the query, Esc to leave
// search, up/down to move the agent selection, and Enter to focus the
// selected pane. Theme mode routes to the theme selector; navigation mode
// handles movement, focus, filter entry and quitting. tmon-owned keys are
// intercepted here; everything else (j/k/up/down/g/G/…) is forwarded to
// the bubbles list.
func (m Model) handleKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	if m.searching {
		switch msg.String() {
		case "esc":
			m.searching = false
			m.query = ""
			m.searchInput.Reset()
			m.searchInput.Blur()
			m.rebuildFilter()
			return m, nil
		case "ctrl+c":
			return m, tea.Quit
		case "up", "down":
			// Arrow keys move the list; they do not leave the query editor.
			var cmd tea.Cmd
			m.agentList, cmd = m.agentList.Update(msg)
			m.refreshPreview(false)
			return m, cmd
		case "enter":
			return m.focusSelected()
		}
		prev := m.searchInput.Value()
		var cmd tea.Cmd
		m.searchInput, cmd = m.searchInput.Update(msg)
		if v := m.searchInput.Value(); v != prev {
			m.query = v
			m.rebuildFilter()
			// Keep the preview on the preserved selection (not the first hit).
			m.refreshPreview(false)
		}
		return m, cmd
	}

	if m.themeMode {
		return m.handleThemeKey(msg)
	}

	switch msg.String() {
	case "/":
		m.searching = true
		// One full capture when search opens so fuzzy match has pane text.
		m.refreshPaneCache()
		m.query = ""
		m.searchInput.Reset()
		m.searchInput.Focus()
		return m, textinput.Blink
	case "t":
		m.themeCommitted = m.theme
		m.themeMode = true
		// Highlight the theme already in effect so the list and the
		// palette preview match the popup (not the first preset).
		m = m.selectThemeByName(m.theme.Name)
	case "esc", "q", "ctrl+c":
		return m, tea.Quit
	case "left", "h":
		m.resizePreview(previewResizeStep)
	case "right", "l":
		m.resizePreview(-previewResizeStep)
	case "ctrl+u":
		before := m.preview.YOffset
		m.preview.HalfPageUp()
		// Leave the tail only when the offset actually moved up.
		if m.preview.YOffset < before {
			m.previewFollowBottom = false
		}
	case "ctrl+d":
		m.preview.HalfPageDown()
		m.previewFollowBottom = m.preview.AtBottom()
	case "enter", " ":
		return m.focusSelected()
	case "v":
		m.viewMode = nextView(m.viewMode)
		m.listScroll = 0
		// Re-filter so list order is session/window/pane again, then
		// rebuildItems reorders for projects/status. Keeps the selection
		// by PID across the layout change.
		m.rebuildFilter()
		m.saveSettings()
	case "b", "w", "i":
		m.toggleStatusFilter(statusKey(msg.String()))
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

// handleThemeKey dispatches keys in the theme selector: browsing forwards
// to the themes list and previews the highlighted preset live on the whole
// popup; enter/space commit the highlighted theme (persist it and close
// the selector); esc/q revert to the theme that was in effect when the
// selector opened (nothing persists); ctrl+c quits the popup.
func (m Model) handleThemeKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.themeMode = false
		m = m.WithTheme(m.themeCommitted) // discard the live preview
		// Reverting replaced the spinner (committed theme color); re-arm
		// its tick so the animation keeps running after the old chain.
		return m, func() tea.Msg { return m.spinner.Tick() }
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
		m = m.applyThemePreview()
	}
	return m, nil
}

// handleMouse maps mouse events: drag the │ separator to resize the preview,
// or left-click an agent row to focus its pane (same as Enter). Clicks on
// chrome rows, the preview panel body, and blank padding are ignored. The
// theme selector is keyboard-only. Screen coordinates are shifted one cell
// in from each edge first, because the popup's rounded border is drawn
// inside the canvas.
func (m Model) handleMouse(msg tea.MouseMsg) (Model, tea.Cmd) {
	if m.themeMode {
		return m, nil
	}
	msg.X--
	msg.Y--
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
			if m.width > 2 {
				if listW, _ := m.panelWidths(m.width - 2); msg.X >= listW {
					before := m.preview.YOffset
					m.preview.Update(msg)
					if msg.Button == tea.MouseButtonWheelUp && m.preview.YOffset < before {
						m.previewFollowBottom = false
					} else if msg.Button == tea.MouseButtonWheelDown {
						m.previewFollowBottom = m.preview.AtBottom()
					}
				}
			}
			return m, nil
		}
		if msg.Button != tea.MouseButtonLeft {
			return m, nil
		}
		// Drag the list|preview separator (hit target ±1 cell).
		if m.width > 2 {
			listW, _ := m.panelWidths(m.width - 2)
			if msg.X >= listW-1 && msg.X <= listW+1 {
				m.draggingSplit = true
				m.setPreviewPctFromX(msg.X)
				return m, nil
			}
		}
		// Only clicks on the list column act; the preview panel is a no-op.
		if m.width > 2 {
			_, panelW := m.panelWidths(m.width - 2)
			if listW := m.width - 2 - panelW - 1; msg.X >= listW {
				return m, nil
			}
		}
		// The wordmark rows and divider are chrome; body rows start right after.
		// While searching, the last body row of the list is the query input.
		topChrome := m.mainTopChrome()
		bodyRow := msg.Y - topChrome
		if bodyRow < 0 || bodyRow >= bodyLinesFor(m.height-2, topChrome) {
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
// Section headers, the search input row, and partial rows are ignored.
// Reports whether an agent was hit.
func (m *Model) clickAgentAt(bodyRow int) bool {
	if bodyRow < 0 {
		return false
	}
	bodyH := bodyLinesFor(m.height-2, m.mainTopChrome())
	listBodyH := bodyH
	if m.searching {
		listBodyH = bodyH - 1
		if listBodyH < 1 {
			listBodyH = 1
		}
		if bodyRow >= listBodyH {
			return false // search input row
		}
	}
	entries := m.buildListEntries()
	if len(entries) == 0 {
		return false
	}
	rowH := m.agentRowHeight()
	m.clampListScroll(listBodyH, rowH)
	// Absolute content line under the cursor.
	absLine := m.listScroll + bodyRow
	starts := entryStartLines(entries, rowH)
	for i, e := range entries {
		if !e.isAgent() {
			continue
		}
		start := starts[i]
		end := start + e.height(rowH)
		if absLine < start || absLine >= end {
			continue
		}
		// Only fully visible agent rows are click targets.
		if start < m.listScroll || end > m.listScroll+listBodyH {
			return false
		}
		if e.agent < 0 || e.agent >= len(m.filtered) {
			return false
		}
		m.agentList.Select(e.agent)
		m.refreshPreview(false)
		return true
	}
	return false
}

// setPreviewPctFromX sets previewPct so the separator sits near column x.
// x is in content coordinates (inside the popup's drawn border), and the
// split only ever moves within the inner canvas. Does not persist; the
// caller saves on drag release.
func (m *Model) setPreviewPctFromX(x int) {
	innerW := m.width - 2 // content width inside the border
	if innerW < 2 {
		return
	}
	// list | sep | preview  →  preview fraction of (innerW - 1) after the sep.
	listW := x
	if listW < 0 {
		listW = 0
	}
	if listW > innerW-1 {
		listW = innerW - 1
	}
	panelW := innerW - 1 - listW
	pct := (panelW * 100) / (innerW - 1)
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

// refreshPreview points the preview panel at the selected agent's pane.
// With force, content is re-read from the cache (or captured if missing)
// even when the pane target is unchanged. Changing panes (and refreshes
// while the user is still following the tail) pin the content to the
// bottom of the viewport.
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
	// On a forced reload, drop the selected pane's cache entry so the
	// preview shows fresh content without re-capturing every agent pane.
	if force && m.paneCache != nil {
		delete(m.paneCache, pane)
	}
	changed := pane != m.previewPane
	if changed {
		// A new pane always follows the tail; the user can scroll up later.
		m.previewFollowBottom = true
	}
	m.previewPane = pane
	m.previewText = m.cachedPane(pane)
	// Trim blank edges so the bottom pin lands on real content (tmux pane
	// captures pad with empty lines and end with a newline).
	m.preview.SetContent(strings.Join(trimEmptyEdges(strings.Split(m.previewText, "\n")), "\n"))
	if m.previewFollowBottom {
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

// rebuildFilter re-applies the query over agent name, session title, tmux
// path, project (path + git), and full pane capture. Each whitespace-
// separated word is AND'd: every term must appear as a case-insensitive
// substring of at least one field. Matching agents keep list order
// (session/window/pane), not score rank. The previously selected agent is
// kept when it still matches.
func (m *Model) rebuildFilter() {
	// Capture selection from the list items before filtered order changes.
	selPID := m.selectedAgentPID()

	m.filtered = m.filtered[:0]
	for i, r := range m.rows {
		if m.filterStatus != "" && r.Status != m.filterStatus {
			continue
		}
		if m.query != "" && !m.agentMatchesQuery(r) {
			continue
		}
		m.filtered = append(m.filtered, i)
	}
	m.rebuildItems(selPID)
}

// selectedAgentPID is the PID of the agent currently selected in the list,
// or 0 when nothing is selected. Uses the bubbles item (not filtered+index)
// so it stays correct while rebuildFilter rewrites filtered.
func (m Model) selectedAgentPID() int {
	item := m.agentList.SelectedItem()
	if item == nil {
		return 0
	}
	ai, ok := item.(agentItem)
	if !ok {
		return 0
	}
	return ai.row.PID
}

// agentMatchesQuery reports whether every whitespace-separated term in the
// current query appears as a case-insensitive substring of at least one
// searchable field. Substring matching (not fuzzy subsequence) keeps full
// pane captures from matching almost every short query.
func (m *Model) agentMatchesQuery(r Row) bool {
	terms := strings.Fields(m.query)
	if len(terms) == 0 {
		return true
	}
	fields := m.agentSearchFields(r)
	// Lower-case fields once for all terms.
	lowers := make([]string, len(fields))
	for i, f := range fields {
		if f != "" {
			lowers[i] = strings.ToLower(f)
		}
	}
	for _, term := range terms {
		t := strings.ToLower(term)
		matched := false
		for _, f := range lowers {
			if f != "" && strings.Contains(f, t) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

// agentSearchFields are the discrete fields search matches against. Each
// term must hit at least one field; terms are not allowed to span fields.
func (m *Model) agentSearchFields(r Row) []string {
	name := agentDisplayName(r)
	// Project: working directory (raw + home-relative display) and git context.
	// Avoid duplicating identical path forms so fuzzy terms cannot chain
	// across two copies of the same cwd (e.g. "gb" vs "blog blog").
	projectParts := []string{r.CWD}
	if disp := displayCWD(r.CWD); disp != "" && disp != r.CWD {
		projectParts = append(projectParts, disp)
	}
	if r.Branch != "" {
		projectParts = append(projectParts, r.Branch)
	}
	if r.PR != "" {
		projectParts = append(projectParts, r.PR, "#"+r.PR)
	}
	project := strings.Join(projectParts, " ")
	// Tmux path: structured names/indexes and the rendered path string.
	tmux := strings.Join([]string{
		r.SessionName, r.SessionID,
		r.WindowName, r.WindowIndex,
		r.PaneIndex, r.Pane,
		tmuxPath(r),
	}, " ")
	preview := ""
	if r.Pane != "" && r.Pane != "?" && m.paneCache != nil {
		preview = ansi.Strip(m.paneCache[r.Pane])
	}
	return []string{
		name,
		r.Profile,
		r.Label,
		r.Title, // session / conversation title
		tmux,
		project,
		preview,
	}
}

// rebuildItems rebuilds the flat agent list from the filtered rows (one
// item per agent) and feeds it to the bubbles list. For projects/status
// views, filtered is reordered to the visual section order so j/k matches
// the screen. Each item carries the agent's stripped pane capture so
// search can match content beyond the visible preview. selPID is restored
// when that agent is still in the list; otherwise the index is only
// clamped into range (no jump to the first match).
func (m *Model) rebuildItems(selPID int) {
	if selPID == 0 {
		selPID = m.selectedAgentPID()
	}
	m.filtered = m.orderFilteredForView(m.filtered)

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

	n := len(items)
	if n == 0 {
		return
	}
	if selPID != 0 {
		for i, fi := range m.filtered {
			if m.rows[fi].PID == selPID {
				m.agentList.Select(i)
				return
			}
		}
	}
	// Previous selection left the filter: keep a valid index without
	// forcing the cursor to the top-ranked match.
	if m.agentList.Index() >= n {
		m.agentList.Select(n - 1)
	}
}
