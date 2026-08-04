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
// scenarios in the bash popup tests.
func testRows() []Row {
	return []Row{
		{PID: 10, Label: "Grok", Status: agent.StatusWorking, CWD: "code/tmon",
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

func TestFilterMatchesNameSessionWindow(t *testing.T) {
	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})

	cases := []struct {
		query string
		want  []string // labels in order
	}{
		{"grok", []string{"Grok"}},            // full name "Grok Build"
		{"SHELL", []string{"Grok", "Claude"}}, // window name, case-insensitive
		{"side", []string{"Codex"}},           // session name
		{"code", []string{"Claude", "Codex"}}, // "Claude Code" and "Codex CLI" both contain "code"
		{"blog", nil},                         // cwd is deliberately not searched
		{"", []string{"Grok", "Claude", "Codex"}},
	}
	for _, c := range cases {
		m = applyMsg(t, m, key('/')) // filtering happens in search mode
		for _, r := range []rune(c.query) {
			m = applyMsg(t, m, key(r))
		}
		if len(m.filtered) != len(c.want) {
			t.Fatalf("query %q: filtered = %d, want %d", c.query, len(m.filtered), len(c.want))
		}
		for i, label := range c.want {
			if got := m.rows[m.filtered[i]].Label; got != label {
				t.Fatalf("query %q: filtered[%d] = %s, want %s", c.query, i, got, label)
			}
		}
		// Clear the query and leave search mode for the next case.
		for range c.query {
			m = applyMsg(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
		}
		m = applyMsg(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	}
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

	// Printable runes append to the query and re-filter. "co" matches
	// "Claude Code" and "Codex CLI".
	m = applyMsg(t, m, key('c'))
	m = applyMsg(t, m, key('o'))
	if m.query != "co" {
		t.Fatalf("query = %q, want \"co\"", m.query)
	}
	if len(m.filtered) != 2 {
		t.Fatalf("filtered = %d, want 2", len(m.filtered))
	}

	// Continue typing to "codex" — narrows to Codex only.
	for _, r := range []rune{'d', 'e', 'x'} {
		m = applyMsg(t, m, key(r))
	}
	if m.query != "codex" {
		t.Fatalf("query = %q, want \"codex\"", m.query)
	}
	if len(m.filtered) != 1 || m.rows[m.filtered[0]].Label != "Codex" {
		t.Fatalf("filtered = %v, want only Codex", m.filtered)
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
	m = applyMsg(t, m, key('s'))
	m = applyMsg(t, m, key('i'))
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

	// Right arrow focuses too.
	m = applyMsg(t, m, tea.KeyMsg{Type: tea.KeyRight})
	if focused != "main:0.1" {
		t.Fatalf("focused %q, want main:0.1", focused)
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
	for _, want := range []string{"[@] tmon", "No agents detected.", "▌ / to search"} {
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
	m.width, m.height = 80, 24

	v := m.View()
	for _, want := range []string{
		"[@] tmon",
		"main",    // session header
		"0:shell", // window sub-header
		"Grok Build", "Claude Code", "Codex CLI",
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
