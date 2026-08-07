package dashboard

import (
	"sort"
	"strings"

	"github.com/guillaumemeyer/tmon/internal/agent"
)

// ViewMode is how the agent list is grouped in the popup.
type ViewMode int

const (
	viewList ViewMode = iota
	viewProjects
	viewStatus
	viewAgents
)

// viewCycle is the order of views when the user presses v.
var viewCycle = []ViewMode{viewList, viewProjects, viewStatus, viewAgents}

// String is the persisted settings key for a view (stable across renames).
func (v ViewMode) String() string {
	switch v {
	case viewProjects:
		return "projects"
	case viewStatus:
		return "status"
	case viewAgents:
		return "agents"
	default:
		return "list"
	}
}

// Label is the human-readable view name shown in the footer.
func (v ViewMode) Label() string {
	switch v {
	case viewProjects:
		return "By project"
	case viewStatus:
		return "By status"
	case viewAgents:
		return "By agent"
	default:
		return "List"
	}
}

// parseViewMode maps a settings string to a ViewMode; unknown values become list.
func parseViewMode(s string) ViewMode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "projects":
		return viewProjects
	case "status":
		return viewStatus
	case "agents":
		return viewAgents
	default:
		return viewList
	}
}

// nextView returns the next view in the cycle after v.
func nextView(v ViewMode) ViewMode {
	for i, cur := range viewCycle {
		if cur == v {
			return viewCycle[(i+1)%len(viewCycle)]
		}
	}
	return viewList
}

// listEntry is one unit in the agent list column: a section header (one
// line), a blank spacer between groups (one line), or an agent row (three
// lines).
type listEntry struct {
	section string // non-empty for a section header
	blank   bool   // empty separator line between groups
	agent   int    // index into filtered / agentList items for agent rows
}

// agentItemHeight is the height of one agent row in the list column:
// name, project, pane. The usage lines live in the preview pane.
const agentItemHeight = 3

// agentRowHeight returns the uniform height of one agent row in the list
// column. Every row is exactly agentItemHeight lines now that the usage
// and quota lines render in the preview pane, so the flat layout stays
// aligned without per-agent padding.
func (m Model) agentRowHeight() int {
	return agentItemHeight
}

// entryHeight is the number of body lines one list entry occupies. rowH is
// the uniform agent-row height (agentItemHeight plus quota rows).
func (e listEntry) height(rowH int) int {
	if e.blank || e.section != "" {
		return 1
	}
	return rowH
}

// isAgent reports whether the entry is a selectable agent row.
func (e listEntry) isAgent() bool {
	return !e.blank && e.section == ""
}

// buildListEntries lays out the filtered agents for the current view mode.
// List is flat. Projects groups by CWD (headers sorted alphabetically).
// Status groups by blocked, working, idle (fixed order). Agents groups by
// agent type (headers sorted alphabetically). Empty groups are omitted.
// Inside a group, agents keep the order of m.filtered.
//
// Callers that need j/k to follow the screen must first put m.filtered in
// visual order via orderFilteredForView (see rebuildItems).
func (m Model) buildListEntries() []listEntry {
	return m.entriesFor(m.filtered)
}

// orderFilteredForView reorders row indices into the visual agent order for
// the current view (list order unchanged; projects/status/agents follow
// section layout). Selection keys then match what the user sees.
func (m Model) orderFilteredForView(filtered []int) []int {
	if len(filtered) == 0 {
		return filtered
	}
	if m.viewMode == viewList {
		return filtered
	}
	entries := m.entriesFor(filtered)
	out := make([]int, 0, len(filtered))
	for _, e := range entries {
		if e.isAgent() {
			out = append(out, filtered[e.agent])
		}
	}
	return out
}

// entriesFor builds section/agent entries from a filtered index slice.
// e.agent is an index into filtered (not into rows).
func (m Model) entriesFor(filtered []int) []listEntry {
	n := len(filtered)
	if n == 0 {
		return nil
	}
	switch m.viewMode {
	case viewProjects:
		return projectEntries(m.rows, filtered)
	case viewStatus:
		return statusEntries(m.rows, filtered)
	case viewAgents:
		return agentEntries(m.rows, filtered)
	default:
		out := make([]listEntry, n)
		for i := range filtered {
			out[i] = listEntry{agent: i}
		}
		return out
	}
}

// projectEntries groups agents by git root when one is known, falling back
// to the working directory. Project headers sort alphabetically by their
// display path, with the branch (and PR number) tag appended when the agent
// is in a repository — "~/code/tmon (main · #42)". Agents inside a group
// keep filtered order.
func projectEntries(rows []Row, filtered []int) []listEntry {
	type group struct {
		key   string
		label string
		idxs  []int
	}
	byKey := make(map[string]*group)
	var order []string
	for i, fi := range filtered {
		r := rows[fi]
		key := r.CWD
		label := displayCWD(r.CWD)
		if r.GitRoot != "" {
			key = r.GitRoot
			label = displayCWD(r.GitRoot)
		}
		if key == "" {
			key = "?"
		}
		if label == "" {
			label = "?"
		}
		if tag := gitTagString(r); tag != "" {
			label += tag
		}
		g, ok := byKey[key]
		if !ok {
			g = &group{key: key, label: label}
			byKey[key] = g
			order = append(order, key)
		}
		g.idxs = append(g.idxs, i)
	}
	sort.SliceStable(order, func(i, j int) bool {
		return byKey[order[i]].label < byKey[order[j]].label
	})
	out := make([]listEntry, 0, len(filtered)+2*len(order))
	for i, key := range order {
		if i > 0 {
			out = append(out, listEntry{blank: true})
		}
		g := byKey[key]
		out = append(out, listEntry{section: g.label})
		for _, ai := range g.idxs {
			out = append(out, listEntry{agent: ai})
		}
	}
	return out
}

// statusEntries groups agents by status in fixed order: blocked, working,
// idle. Empty status groups are omitted. Unknown statuses land in a
// trailing "other" section so they stay reachable.
func statusEntries(rows []Row, filtered []int) []listEntry {
	order := []agent.Status{agent.StatusBlocked, agent.StatusWorking, agent.StatusIdle}
	buckets := make(map[agent.Status][]int, 4)
	var other []int
	for i, fi := range filtered {
		st := rows[fi].Status
		switch st {
		case agent.StatusBlocked, agent.StatusWorking, agent.StatusIdle:
			buckets[st] = append(buckets[st], i)
		default:
			other = append(other, i)
		}
	}
	out := make([]listEntry, 0, len(filtered)+6)
	first := true
	appendStatusGroup := func(label string, idxs []int) {
		if len(idxs) == 0 {
			return
		}
		if !first {
			out = append(out, listEntry{blank: true})
		}
		first = false
		out = append(out, listEntry{section: label})
		for _, ai := range idxs {
			out = append(out, listEntry{agent: ai})
		}
	}
	for _, st := range order {
		appendStatusGroup(string(st), buckets[st])
	}
	appendStatusGroup("other", other)
	return out
}

// agentEntries groups agents by their detected type (the signature label,
// e.g. "Grok", "Claude", "Hermes"). Section headers show the display name
// ("Grok Build", "Hermes Agent", ...) and sort alphabetically. Unknown or
// empty labels land in a "?" section. Agents inside a group keep filtered
// order.
func agentEntries(rows []Row, filtered []int) []listEntry {
	type group struct {
		key   string
		label string
		idxs  []int
	}
	byKey := make(map[string]*group)
	var order []string
	for i, fi := range filtered {
		r := rows[fi]
		key := r.Label
		label := agentFullName(r.Label)
		if key == "" {
			key = "?"
		}
		if label == "" {
			label = "?"
		}
		g, ok := byKey[key]
		if !ok {
			g = &group{key: key, label: label}
			byKey[key] = g
			order = append(order, key)
		}
		g.idxs = append(g.idxs, i)
	}
	sort.SliceStable(order, func(i, j int) bool {
		return byKey[order[i]].label < byKey[order[j]].label
	})
	out := make([]listEntry, 0, len(filtered)+2*len(order))
	for i, key := range order {
		if i > 0 {
			out = append(out, listEntry{blank: true})
		}
		g := byKey[key]
		out = append(out, listEntry{section: g.label})
		for _, ai := range g.idxs {
			out = append(out, listEntry{agent: ai})
		}
	}
	return out
}

// entryStartLines returns the cumulative line offset of each entry, plus a
// final element equal to the total content height.
func entryStartLines(entries []listEntry, rowH int) []int {
	starts := make([]int, len(entries)+1)
	for i, e := range entries {
		starts[i+1] = starts[i] + e.height(rowH)
	}
	return starts
}

// selectedEntryLine is the starting body line of the selected agent entry,
// or 0 when nothing is selected.
func (m Model) selectedEntryLine(entries []listEntry, rowH int) int {
	sel := m.agentList.Index()
	if sel < 0 || len(entries) == 0 {
		return 0
	}
	starts := entryStartLines(entries, rowH)
	for i, e := range entries {
		if e.isAgent() && e.agent == sel {
			return starts[i]
		}
	}
	return 0
}

// clampListScroll keeps listScroll in range for the current content height
// and viewport, and pulls the selected agent into view when needed.
func (m *Model) clampListScroll(bodyLines, rowH int) {
	entries := m.buildListEntries()
	starts := entryStartLines(entries, rowH)
	total := starts[len(starts)-1]
	if bodyLines < 1 {
		bodyLines = 1
	}
	maxScroll := total - bodyLines
	if maxScroll < 0 {
		maxScroll = 0
	}
	// Bring the selected agent fully into the viewport.
	if len(m.filtered) > 0 {
		selLine := m.selectedEntryLine(entries, rowH)
		selEnd := selLine + rowH
		if selLine < m.listScroll {
			m.listScroll = selLine
		}
		if selEnd > m.listScroll+bodyLines {
			m.listScroll = selEnd - bodyLines
		}
	}
	if m.listScroll < 0 {
		m.listScroll = 0
	}
	if m.listScroll > maxScroll {
		m.listScroll = maxScroll
	}
}
