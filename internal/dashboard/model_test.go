package dashboard

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/guillaumemeyer/tmon/internal/agent"
)

// fakeLoader returns fixed data on every load.
type fakeLoader struct {
	data Data
}

func (f *fakeLoader) load() (Data, error) {
	return f.data, nil
}

// testRows: two sessions, two windows, three agents, matching the grouping
// scenarios in the bash popup tests. Grok carries a session title (like a
// Grok generated_title); the others have none.
func testRows() []Row {
	return []Row{
		{PID: 10, Label: "Grok", Title: "Popup preview scroll", Status: agent.StatusWorking, CWD: "code/tmon",
			Pane: "main:0.0", SessionID: "1", SessionName: "main", WindowIndex: "0", WindowName: "shell", PaneIndex: "0"},
		{PID: 11, Label: "Claude", Status: agent.StatusBlocked, CWD: "site",
			Pane: "main:0.1", SessionID: "1", SessionName: "main", WindowIndex: "0", WindowName: "shell", PaneIndex: "1"},
		{PID: 12, Label: "Codex", Status: agent.StatusIdle, CWD: "blog",
			Pane: "side:3.0", SessionID: "2", SessionName: "side", WindowIndex: "3", WindowName: "code", PaneIndex: "0"},
	}
}

// applyMsg runs msg through Update and returns the resulting model. The
// returned command is dropped — the tests drive the tick cadence manually.
func applyMsg(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	nm, _ := m.Update(msg)
	return nm.(Model)
}

// key builds a KeyMsg for a printable rune the way the input reader would.
func key(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

func TestInitialLoadIsFull(t *testing.T) {
	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)

	if len(m.rows) != 0 {
		t.Fatalf("expected no rows before the initial load, got %d", len(m.rows))
	}

	m = applyMsg(t, m, initMsg{})
	if len(m.rows) != 3 {
		t.Fatalf("rows after initial load = %d, want 3", len(m.rows))
	}
}

func TestTickReloadsRows(t *testing.T) {
	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})

	// Every tick is a full reload: rows are refreshed from the loader.
	f.data = Data{Rows: testRows()[:2]}
	m = applyMsg(t, m, tickMsg{})
	if len(m.rows) != 2 {
		t.Fatalf("rows after tick reload = %d, want 2", len(m.rows))
	}
}

func TestGrouping(t *testing.T) {
	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})

	// main → 0:shell → 2 agents; side → 3:code → 1 agent.
	want := []itemKind{itemSession, itemWindow, itemAgent, itemAgent, itemSession, itemWindow, itemAgent}
	if len(m.items) != len(want) {
		t.Fatalf("items = %d, want %d", len(m.items), len(want))
	}
	for i, kind := range want {
		if m.items[i].kind != kind {
			t.Fatalf("item %d kind = %v, want %v", i, m.items[i].kind, kind)
		}
	}

	// Selectable map points at the three agent lines: items 2, 3, 6.
	if got := m.selMap; len(got) != 3 || got[0] != 2 || got[1] != 3 || got[2] != 6 {
		t.Fatalf("selMap = %v, want [2 3 6]", got)
	}
}

func TestFilterFuzzyNameAndCWD(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "" }
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})

	cases := []struct {
		query   string
		want    []string // exact labels in score order (nil wantAny)
		wantAny []string // unordered set of labels (when ranking is flexible)
		wantN   int      // if > 0, only check count + membership via wantAny
	}{
		{query: "grok", want: []string{"Grok"}},   // full name "Grok Build"
		{query: "gb", want: []string{"Grok"}},     // fuzzy subsequence of "Grok Build"
		{query: "blog", want: []string{"Codex"}},  // CWD is searched
		{query: "site", want: []string{"Claude"}}, // CWD
		{query: "popup", want: []string{"Grok"}},  // session title is searched
		{query: "SHELL"},                          // window name is not a search field
		{query: "main"},                           // session name is not a search field
		{query: "", want: []string{"Grok", "Claude", "Codex"}},
		// "code" hits Claude/Codex names and Grok's cwd "code/tmon".
		{query: "code", wantAny: []string{"Grok", "Claude", "Codex"}, wantN: 3},
	}
	for _, c := range cases {
		m = applyMsg(t, m, key('/')) // filtering happens in search mode
		for _, r := range []rune(c.query) {
			m = applyMsg(t, m, key(r))
		}
		got := labelsOf(m)
		switch {
		case c.wantN > 0:
			if len(got) != c.wantN {
				t.Fatalf("query %q: filtered = %v (len %d), want %d", c.query, got, len(got), c.wantN)
			}
			set := map[string]bool{}
			for _, l := range got {
				set[l] = true
			}
			for _, l := range c.wantAny {
				if !set[l] {
					t.Fatalf("query %q: filtered = %v, missing %s", c.query, got, l)
				}
			}
		default:
			if len(got) != len(c.want) {
				t.Fatalf("query %q: filtered = %v, want %v", c.query, got, c.want)
			}
			for i, label := range c.want {
				if got[i] != label {
					t.Fatalf("query %q: filtered[%d] = %s, want %s", c.query, i, got[i], label)
				}
			}
		}
		// Clear the query and leave search mode for the next case.
		for range c.query {
			m = applyMsg(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
		}
		m = applyMsg(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	}
}

func TestFilterFuzzyPreviewContent(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string {
		switch p {
		case "main:0.0":
			return "running tests in package dashboard"
		case "main:0.1":
			return "waiting for user approval [y/N]"
		case "side:3.0":
			return "refactoring the auth middleware"
		default:
			return ""
		}
	}
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})

	// Fuzzy match against content that is not the selected agent's preview.
	m = applyMsg(t, m, key('/'))
	for _, r := range []rune("middleware") {
		m = applyMsg(t, m, key(r))
	}
	if len(m.filtered) != 1 || m.rows[m.filtered[0]].Label != "Codex" {
		t.Fatalf("preview search: filtered = %v, want only Codex", labelsOf(m))
	}

	// Subsequence across preview text.
	for range "middleware" {
		m = applyMsg(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	for _, r := range []rune("aprvl") { // approval
		m = applyMsg(t, m, key(r))
	}
	if len(m.filtered) != 1 || m.rows[m.filtered[0]].Label != "Claude" {
		t.Fatalf("fuzzy preview search: filtered = %v, want only Claude", labelsOf(m))
	}
}

func labelsOf(m Model) []string {
	out := make([]string, len(m.filtered))
	for i, fi := range m.filtered {
		out[i] = m.rows[fi].Label
	}
	return out
}

func TestSearchModeKeys(t *testing.T) {
	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})

	// "/" enters search mode.
	m = applyMsg(t, m, key('/'))
	if !m.searching {
		t.Fatal("expected search mode after /")
	}

	// Printable runes append to the query and re-filter. "co" is a fuzzy
	// hit on Claude/Codex names and Grok's "code/tmon" cwd.
	m = applyMsg(t, m, key('c'))
	m = applyMsg(t, m, key('o'))
	if m.query != "co" {
		t.Fatalf("query = %q, want \"co\"", m.query)
	}
	if len(m.filtered) != 3 {
		t.Fatalf("filtered = %v, want all 3 agents", labelsOf(m))
	}

	// Continue typing to "codex" — narrows to Codex only.
	for _, r := range []rune{'d', 'e', 'x'} {
		m = applyMsg(t, m, key(r))
	}
	if m.query != "codex" {
		t.Fatalf("query = %q, want \"codex\"", m.query)
	}
	if len(m.filtered) != 1 || m.rows[m.filtered[0]].Label != "Codex" {
		t.Fatalf("filtered = %v, want only Codex", labelsOf(m))
	}

	// Non-printable keys (e.g. function keys) are ignored in search mode.
	m = applyMsg(t, m, tea.KeyMsg{Type: tea.KeyF1})
	if m.query != "codex" {
		t.Fatalf("query changed by F1: %q", m.query)
	}

	// Esc leaves search mode but the filter stays.
	m = applyMsg(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.searching {
		t.Fatal("expected search mode to end on Esc")
	}
	if m.query != "codex" || len(m.filtered) != 1 {
		t.Fatalf("filter should persist after Esc (query=%q filtered=%d)", m.query, len(m.filtered))
	}
}

func TestNavigationWraps(t *testing.T) {
	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})

	if m.selected != 0 {
		t.Fatalf("initial selection = %d, want 0", m.selected)
	}

	steps := []struct {
		msg  tea.Msg
		want int
	}{
		{tea.KeyMsg{Type: tea.KeyDown}, 1},
		{key('j'), 2},
		{key('j'), 0}, // wraps forward
		{tea.KeyMsg{Type: tea.KeyUp}, 2},
		{key('k'), 1}, // wraps backward
		{key('k'), 0},
		{key('k'), 2},
	}
	for i, s := range steps {
		m = applyMsg(t, m, s.msg)
		if m.selected != s.want {
			t.Fatalf("step %d: selection = %d, want %d", i, m.selected, s.want)
		}
	}
}

func TestSelectionClampsOnFilter(t *testing.T) {
	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})

	m = applyMsg(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = applyMsg(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.selected != 2 {
		t.Fatalf("selection = %d, want 2", m.selected)
	}

	// Filtering down to one agent clamps the selection to 0.
	m = applyMsg(t, m, key('/'))
	for _, r := range []rune("aud") { // matches only "Claude Code"
		m = applyMsg(t, m, key(r))
	}
	if len(m.filtered) != 1 {
		t.Fatalf("filtered = %d, want 1", len(m.filtered))
	}
	if m.selected != 0 {
		t.Fatalf("selection after clamp = %d, want 0", m.selected)
	}
}

func TestFocusSwitchesToSelectedPane(t *testing.T) {
	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})

	var focused string
	m.focusCmd = func(r Row) tea.Cmd {
		focused = r.Pane
		return nil
	}

	// First agent is Grok at main:0.0; Enter focuses it.
	m = applyMsg(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if focused != "main:0.0" {
		t.Fatalf("focused %q, want main:0.0", focused)
	}

	// Space and l also focus; move the selection first.
	m = applyMsg(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = applyMsg(t, m, key(' '))
	if focused != "main:0.1" {
		t.Fatalf("focused %q, want main:0.1", focused)
	}
	m = applyMsg(t, m, key('l'))
	if focused != "main:0.1" {
		t.Fatalf("focused %q, want main:0.1", focused)
	}

	// Right arrow resizes the preview — it must not focus.
	focused = ""
	m = applyMsg(t, m, tea.KeyMsg{Type: tea.KeyRight})
	if focused != "" {
		t.Fatalf("right arrow focused %q; want resize only", focused)
	}
	if m.previewPct == defaultPreviewPct {
		t.Fatalf("previewPct after right = %d, want a resize", m.previewPct)
	}
}

func TestPreviewResizeKeys(t *testing.T) {
	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	if m.previewPct != defaultPreviewPct {
		t.Fatalf("default previewPct = %d, want %d", m.previewPct, defaultPreviewPct)
	}

	// Left grows the preview, right shrinks it.
	m = applyMsg(t, m, tea.KeyMsg{Type: tea.KeyLeft})
	if m.previewPct != defaultPreviewPct+previewResizeStep {
		t.Fatalf("after left: previewPct = %d, want %d", m.previewPct, defaultPreviewPct+previewResizeStep)
	}

	m = applyMsg(t, m, tea.KeyMsg{Type: tea.KeyRight})
	m = applyMsg(t, m, tea.KeyMsg{Type: tea.KeyRight})
	if m.previewPct != defaultPreviewPct-previewResizeStep {
		t.Fatalf("after right×2: previewPct = %d, want %d", m.previewPct, defaultPreviewPct-previewResizeStep)
	}

	// Clamp at max (reached by pressing left repeatedly).
	for i := 0; i < 30; i++ {
		m = applyMsg(t, m, tea.KeyMsg{Type: tea.KeyLeft})
	}
	if m.previewPct != maxPreviewPct {
		t.Fatalf("previewPct at max = %d, want %d", m.previewPct, maxPreviewPct)
	}

	// Clamp at min (reached by pressing right repeatedly).
	for i := 0; i < 30; i++ {
		m = applyMsg(t, m, tea.KeyMsg{Type: tea.KeyRight})
	}
	if m.previewPct != minPreviewPct {
		t.Fatalf("previewPct at min = %d, want %d", m.previewPct, minPreviewPct)
	}
}

func TestPreviewPctPersists(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/dashboard.json"

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true).WithSettingsPath(path)
	m = applyMsg(t, m, initMsg{})
	m = applyMsg(t, m, tea.KeyMsg{Type: tea.KeyLeft})
	m = applyMsg(t, m, tea.KeyMsg{Type: tea.KeyLeft})
	want := defaultPreviewPct + 2*previewResizeStep
	if m.previewPct != want {
		t.Fatalf("previewPct = %d, want %d", m.previewPct, want)
	}

	// New model with the same settings path reloads the saved width.
	m2 := New(f.load, true).WithSettingsPath(path)
	if m2.previewPct != want {
		t.Fatalf("reloaded previewPct = %d, want %d", m2.previewPct, want)
	}
}

func TestFocusWithNothingSelectable(t *testing.T) {
	m := New(func() (Data, error) { return Data{}, nil }, true)
	m = applyMsg(t, m, initMsg{})

	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("expected no command with an empty list, got %v", cmd)
	}
	if _, ok := nm.(Model); !ok {
		t.Fatal("expected a Model back")
	}
}

// click builds a left-button press at cell (x, y) in the popup viewport.
func click(x, y int) tea.MouseMsg {
	return tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: x, Y: y}
}

func TestMouseClickFocusesAgent(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 100, 24

	var focused string
	m.focusCmd = func(r Row) tea.Cmd {
		focused = r.Pane
		return nil
	}

	// Body rows: 0=session, 1=window, 2=Grok, 3=Claude, 4=session, 5=window,
	// 6=Codex. Clicking an agent row selects it and focuses its pane.
	m = applyMsg(t, m, click(2, 4))
	if focused != "main:0.0" {
		t.Fatalf("click Grok: focused %q, want main:0.0", focused)
	}

	m = applyMsg(t, m, click(2, 5))
	if focused != "main:0.1" {
		t.Fatalf("click Claude: focused %q, want main:0.1", focused)
	}

	m = applyMsg(t, m, click(2, 8))
	if focused != "side:3.0" {
		t.Fatalf("click Codex: focused %q, want side:3.0", focused)
	}
}

func TestMouseClickIgnoresHeadersAndChrome(t *testing.T) {
	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 100, 24

	var focused string
	m.focusCmd = func(r Row) tea.Cmd {
		focused = r.Pane
		return nil
	}

	// Session header (row 0 of body) and window header (row 1) do nothing.
	m = applyMsg(t, m, click(2, 2))
	if focused != "" {
		t.Fatalf("click on session header focused %q", focused)
	}
	m = applyMsg(t, m, click(2, 3))
	if focused != "" {
		t.Fatalf("click on window header focused %q", focused)
	}
	// Chrome rows: header (y=0), divider (y=1), footer (y=23).
	for _, y := range []int{0, 1, 23} {
		m = applyMsg(t, m, click(2, y))
		if focused != "" {
			t.Fatalf("click on chrome row y=%d focused %q", y, focused)
		}
	}
	// Click on the preview panel (right of the separator) does nothing.
	m = applyMsg(t, m, click(60, 4))
	if focused != "" {
		t.Fatalf("click on preview panel focused %q", focused)
	}
	// A release event is never an action.
	m = applyMsg(t, m, tea.MouseMsg{Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft, X: 2, Y: 4})
	if focused != "" {
		t.Fatalf("mouse release focused %q", focused)
	}
}

func TestMouseClickFocusesInSearchFlatList(t *testing.T) {
	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 100, 24

	var focused string
	m.focusCmd = func(r Row) tea.Cmd {
		focused = r.Pane
		return nil
	}

	// Narrow to a single agent: the flat list has one row at body row 0.
	m = applyMsg(t, m, key('/'))
	for _, r := range []rune{'c', 'o', 'd', 'e', 'x'} {
		m = applyMsg(t, m, key(r))
	}
	if len(m.items) != 1 {
		t.Fatalf("filtered items = %d, want 1", len(m.items))
	}
	m = applyMsg(t, m, click(2, 2))
	if focused != "side:3.0" {
		t.Fatalf("click in search list: focused %q, want side:3.0", focused)
	}
}

func TestQuitKeys(t *testing.T) {
	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})

	for _, k := range []tea.KeyMsg{{Type: tea.KeyEsc}, key('q')} {
		_, cmd := m.Update(k)
		if cmd == nil {
			t.Fatalf("key %s: expected a quit command", k)
		}
	}
}

func TestDropLastRune(t *testing.T) {
	cases := []struct{ in, want string }{
		{"abc", "ab"},
		{"café", "caf"}, // multi-byte rune removed whole
		{"", ""},
		{"a", ""},
	}
	for _, c := range cases {
		if got := dropLastRune(c.in); got != c.want {
			t.Fatalf("dropLastRune(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSortRowsNumeric(t *testing.T) {
	rows := []Row{
		{PID: 1, SessionID: "1", WindowIndex: "10", PaneIndex: "0"},
		{PID: 2, SessionID: "2", WindowIndex: "0", PaneIndex: "1"},
		{PID: 3, SessionID: "1", WindowIndex: "2", PaneIndex: "0"},
		{PID: 4, SessionID: "1", WindowIndex: "10", PaneIndex: "1"},
		{PID: 5, SessionID: "?", WindowIndex: "?", PaneIndex: "?"}, // unpaned sorts first
	}
	sortRows(rows)

	want := []int{5, 3, 1, 4, 2} // unpaned(0), s1/w2, s1/w10/p0, s1/w10/p1, s2/w0
	for i, pid := range want {
		if rows[i].PID != pid {
			t.Fatalf("rows[%d].PID = %d, want %d (order: %v)", i, rows[i].PID, pid, pidsOf(rows))
		}
	}
}

func pidsOf(rows []Row) []int {
	out := make([]int, len(rows))
	for i, r := range rows {
		out[i] = r.PID
	}
	return out
}

func TestViewEmptyState(t *testing.T) {
	m := New(func() (Data, error) { return Data{}, nil }, true)
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 80, 24

	v := m.View()
	for _, want := range []string{"[@] tmon", "No agents detected."} {
		if !strings.Contains(v, want) {
			t.Fatalf("view missing %q:\n%s", want, v)
		}
	}
}

func TestViewEmptyStateWithFilter(t *testing.T) {
	m := New(func() (Data, error) { return Data{}, nil }, true)
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 80, 24

	m = applyMsg(t, m, key('/'))
	m = applyMsg(t, m, key('x'))
	v := m.View()
	if !strings.Contains(v, `No agents match "x"`) {
		t.Fatalf("view missing the no-match message:\n%s", v)
	}
	if !strings.Contains(v, "0/0") {
		t.Fatalf("view missing the match count:\n%s", v)
	}
}

func TestViewRendersGroupedList(t *testing.T) {
	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	// Wide enough that the long session title does not truncate the cwd.
	m.width, m.height = 140, 24

	v := m.View()
	for _, want := range []string{
		"[@] tmon",
		"main",                              // session header
		"0:shell",                           // window sub-header
		"Popup preview scroll (Grok Build)", // session title + name
		"Claude Code", "Codex CLI",
		"side", "3:code",
		"code/tmon", // cwd
	} {
		if !strings.Contains(v, want) {
			t.Fatalf("view missing %q:\n%s", want, v)
		}
	}
	if strings.Count(v, "\n")+1 != 24 {
		t.Fatalf("view has %d lines, want 24", strings.Count(v, "\n")+1)
	}
}

func TestFit(t *testing.T) {
	if got := fit("hello world", 5); got != "hello" {
		t.Fatalf("fit = %q, want \"hello\"", got)
	}
	// Short strings are padded to exactly w cells.
	if got := fit("short", 10); got != "short     " {
		t.Fatalf("fit = %q, want \"short     \"", got)
	}
	// Emoji are double-width: 🧠 + 1 char = 3 cells, truncate to 2 = the icon.
	if got := fit("🧠x", 2); got != "🧠" {
		t.Fatalf("fit = %q, want the icon alone", got)
	}
	// Empty input becomes w spaces.
	if got := fit("", 4); got != "    " {
		t.Fatalf("fit empty = %q, want 4 spaces", got)
	}
}
