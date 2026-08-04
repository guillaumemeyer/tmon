package dashboard

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/guillaumemeyer/tmon/internal/agent"
)

func TestGroupByStatus(t *testing.T) {
	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load)
	m = applyMsg(t, m, initMsg{})

	if m.groupMode != groupSession {
		t.Fatalf("initial group mode = %d, want session", m.groupMode)
	}

	m = applyMsg(t, m, key('g'))
	if m.groupMode != groupStatus {
		t.Fatalf("group mode after g = %d, want status", m.groupMode)
	}

	// testRows: Claude blocked, Grok active, Codex paused. Expect one header
	// per present status in urgency order: blocked, active, paused.
	want := []itemKind{itemStatus, itemAgent, itemStatus, itemAgent, itemStatus, itemAgent}
	if len(m.items) != len(want) {
		t.Fatalf("items = %d, want %d: %+v", len(m.items), len(want), m.items)
	}
	wantStatus := []agent.Status{agent.StatusBlocked, agent.StatusActive, agent.StatusPaused}
	pos := 0
	for i, kind := range want {
		if m.items[i].kind != kind {
			t.Fatalf("item %d kind = %v, want %v", i, m.items[i].kind, kind)
		}
		if kind == itemStatus {
			if m.items[i].status != wantStatus[pos] {
				t.Fatalf("header %d status = %v, want %v", i, m.items[i].status, wantStatus[pos])
			}
			pos++
		}
	}
	// selMap points at the three agent lines: items 1, 3, 5.
	if got := m.selMap; len(got) != 3 || got[0] != 1 || got[1] != 3 || got[2] != 5 {
		t.Fatalf("selMap = %v, want [1 3 5]", got)
	}
}

func TestGroupByAgent(t *testing.T) {
	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load)
	m = applyMsg(t, m, initMsg{})

	m = applyMsg(t, m, key('g')) // status
	m = applyMsg(t, m, key('g')) // agent
	if m.groupMode != groupAgent {
		t.Fatalf("group mode = %d, want agent", m.groupMode)
	}
	for i, it := range m.items {
		if it.kind != itemAgent {
			t.Fatalf("item %d kind = %v, want flat agent list", i, it.kind)
		}
	}
	if len(m.selMap) != 3 {
		t.Fatalf("selMap = %v, want 3 selectable", m.selMap)
	}
}

func TestStatusFilters(t *testing.T) {
	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load)
	m = applyMsg(t, m, initMsg{})

	// b → blocked only (Claude).
	m = applyMsg(t, m, key('b'))
	if m.filterStatus != agent.StatusBlocked {
		t.Fatalf("filter = %v, want blocked", m.filterStatus)
	}
	if len(m.filtered) != 1 || m.rows[m.filtered[0]].Label != "Claude" {
		t.Fatalf("filtered = %v, want only Claude", m.filtered)
	}

	// b again clears.
	m = applyMsg(t, m, key('b'))
	if m.filterStatus != "" || len(m.filtered) != 3 {
		t.Fatalf("filter after toggle = %v, filtered %d, want cleared", m.filterStatus, len(m.filtered))
	}

	// a → active only (Grok).
	m = applyMsg(t, m, key('a'))
	if len(m.filtered) != 1 || m.rows[m.filtered[0]].Label != "Grok" {
		t.Fatalf("a filter: filtered = %v, want only Grok", m.filtered)
	}

	// w → running: none in the fixture.
	m = applyMsg(t, m, key('w'))
	if m.filterStatus != agent.StatusRunning || len(m.filtered) != 0 {
		t.Fatalf("w filter: filtered = %v, want none", m.filtered)
	}

	// p → paused only (Codex).
	m = applyMsg(t, m, key('p'))
	if len(m.filtered) != 1 || m.rows[m.filtered[0]].Label != "Codex" {
		t.Fatalf("p filter: filtered = %v, want only Codex", m.filtered)
	}
}

func TestStatusFilterCombinesWithQuery(t *testing.T) {
	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load)
	m = applyMsg(t, m, initMsg{})

	m = applyMsg(t, m, key('a')) // active only
	m = applyMsg(t, m, key('/'))
	m = applyMsg(t, m, key('g'))
	m = applyMsg(t, m, key('r'))
	if len(m.filtered) != 1 || m.rows[m.filtered[0]].Label != "Grok" {
		t.Fatalf("filtered = %v, want Grok (active + name match)", m.filtered)
	}
}

func TestNumberJump(t *testing.T) {
	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load)
	m = applyMsg(t, m, initMsg{})

	m = applyMsg(t, m, key('2'))
	if m.selected != 1 {
		t.Fatalf("selection after 2 = %d, want 1", m.selected)
	}
	m = applyMsg(t, m, key('1'))
	if m.selected != 0 {
		t.Fatalf("selection after 1 = %d, want 0", m.selected)
	}
	m = applyMsg(t, m, key('9')) // beyond the list clamps to the last
	if m.selected != 2 {
		t.Fatalf("selection after 9 = %d, want 2 (clamped)", m.selected)
	}
}

func TestPreviewToggleCapturesSelection(t *testing.T) {
	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load)
	m = applyMsg(t, m, initMsg{})

	var captured []string
	old := capturePane
	capturePane = func(p string) string {
		captured = append(captured, p)
		return "capture of " + p
	}
	t.Cleanup(func() { capturePane = old })

	m = applyMsg(t, m, key('d'))
	if !m.preview {
		t.Fatal("d did not enable the preview")
	}
	if len(captured) != 1 || captured[0] != "main:0.0" {
		t.Fatalf("captured = %v, want the first agent's pane main:0.0", captured)
	}
	if m.previewText == "" || m.previewPane != "main:0.0" {
		t.Fatalf("preview = %q @ %q, want captured text for main:0.0", m.previewText, m.previewPane)
	}

	// Selection change re-captures the new pane.
	m = applyMsg(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if len(captured) != 2 || captured[1] != "main:0.1" {
		t.Fatalf("captured = %v, want second capture of main:0.1", captured)
	}

	// Toggling off clears the preview state.
	m = applyMsg(t, m, key('d'))
	if m.preview || m.previewText != "" || m.previewPane != "" {
		t.Fatalf("preview state after toggle-off = %v/%q/%q, want cleared", m.preview, m.previewText, m.previewPane)
	}
}

func TestPreviewRecapturesOnFullReload(t *testing.T) {
	f := &modeAwareLoader{full: Data{Rows: testRows(), Frame: 0}}
	m := New(f.load)
	m = applyMsg(t, m, initMsg{})

	var captures []string
	old := capturePane
	capturePane = func(p string) string { captures = append(captures, p); return "x" }
	t.Cleanup(func() { capturePane = old })

	m = applyMsg(t, m, key('d'))
	if len(captures) != 1 {
		t.Fatalf("captures after toggle = %d, want 1", len(captures))
	}
	// Three light ticks keep rows cached (no recapture)…
	for i := 0; i < 3; i++ {
		m = applyMsg(t, m, tickMsg{})
	}
	if len(captures) != 1 {
		t.Fatalf("captures after light ticks = %d, want unchanged", len(captures))
	}
	// …the fourth is a full reload: rows refresh and the pane re-captures
	// even though the selection's pane target is unchanged.
	m = applyMsg(t, m, tickMsg{})
	if len(captures) != 2 || captures[1] != "main:0.0" {
		t.Fatalf("captures after full reload = %v, want re-capture of main:0.0", captures)
	}
}

// modeAwareLoader mimics DefaultLoader: light refreshes return only a frame,
// full reloads return rows.
type modeAwareLoader struct {
	modes []Mode
	full  Data
}

func (f *modeAwareLoader) load(mode Mode) (Data, error) {
	f.modes = append(f.modes, mode)
	if mode == ModeLight {
		return Data{Frame: 1}, nil
	}
	return f.full, nil
}

func TestPreviewViewShowsSeparatorAndContent(t *testing.T) {
	f := &fakeLoader{data: Data{Rows: testRows(), Frame: 2}}
	m := New(f.load)
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 120, 24

	old := capturePane
	capturePane = func(p string) string { return "pane-content-line" }
	t.Cleanup(func() { capturePane = old })

	m = applyMsg(t, m, key('d'))
	v := m.View()
	for _, want := range []string{"│", "pane-content-line", "Grok Build"} {
		if !strings.Contains(v, want) {
			t.Fatalf("view missing %q:\n%s", want, v)
		}
	}
	if strings.Count(v, "\n")+1 != 24 {
		t.Fatalf("view has %d lines, want 24", strings.Count(v, "\n")+1)
	}
}

func TestFooterShowsStatusCounts(t *testing.T) {
	f := &fakeLoader{data: Data{Rows: testRows(), Frame: 2}}
	m := New(f.load)
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 80, 24

	v := ansi.Strip(m.View())
	// testRows: 1 blocked, 1 active, 1 paused → ? 1  ● 1  ‖ 1.
	for _, want := range []string{"? 1", "● 1", "‖ 1", "[1-9] jump"} {
		if !strings.Contains(v, want) {
			t.Fatalf("footer missing %q in:\n%s", want, v)
		}
	}
}

func TestFooterShowsActiveFilter(t *testing.T) {
	f := &fakeLoader{data: Data{Rows: testRows(), Frame: 2}}
	m := New(f.load)
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 80, 24

	m = applyMsg(t, m, key('b'))
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "b:blocked") {
		t.Fatalf("footer missing the filter label in:\n%s", v)
	}
}

func TestAgeString(t *testing.T) {
	now := time.Now().Unix()
	cases := []struct {
		lastTs int64
		want   string
	}{
		{0, ""},
		{now, "now"},
		{now - 90, "1m"},
		{now - 45*60, "45m"},
		{now - 3*3600, "3h"},
		{now - 50*3600, "2d"},
	}
	for _, c := range cases {
		if got := ageString(c.lastTs); got != c.want {
			t.Fatalf("ageString(%d) = %q, want %q", c.lastTs, got, c.want)
		}
	}
}

func TestParsePaneTarget(t *testing.T) {
	cases := []struct {
		in                 string
		session, win, pane string
		ok                 bool
	}{
		{"main:0.0", "main", "0", "0", true},
		{"side:3.0", "side", "3", "0", true},
		{"main:0", "main", "0", "?", true},
		{"?", "?", "?", "?", false},
		{"main", "?", "?", "?", false},
		{"", "?", "?", "?", false},
	}
	for _, c := range cases {
		s, w, p, ok := parsePaneTarget(c.in)
		if s != c.session || w != c.win || p != c.pane || ok != c.ok {
			t.Fatalf("parsePaneTarget(%q) = (%q,%q,%q,%v), want (%q,%q,%q,%v)",
				c.in, s, w, p, ok, c.session, c.win, c.pane, c.ok)
		}
	}
}
