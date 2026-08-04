package dashboard

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/guillaumemeyer/tmon/internal/agent"
)

func TestStatusFilters(t *testing.T) {
	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
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

	// w → working only (Grok).
	m = applyMsg(t, m, key('w'))
	if m.filterStatus != agent.StatusWorking || len(m.filtered) != 1 || m.rows[m.filtered[0]].Label != "Grok" {
		t.Fatalf("w filter: filter = %v, filtered = %v, want only Grok", m.filterStatus, m.filtered)
	}

	// i → idle only (Codex).
	m = applyMsg(t, m, key('i'))
	if len(m.filtered) != 1 || m.rows[m.filtered[0]].Label != "Codex" {
		t.Fatalf("i filter: filtered = %v, want only Codex", m.filtered)
	}
}

func TestStatusFilterCombinesWithQuery(t *testing.T) {
	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})

	m = applyMsg(t, m, key('w')) // working only
	m = applyMsg(t, m, key('/'))
	m = applyMsg(t, m, key('g'))
	m = applyMsg(t, m, key('r'))
	if len(m.filtered) != 1 || m.rows[m.filtered[0]].Label != "Grok" {
		t.Fatalf("filtered = %v, want Grok (working + name match)", m.filtered)
	}
}

func TestNumberJump(t *testing.T) {
	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
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

func TestPreviewAlwaysCapturesSelection(t *testing.T) {
	var captured []string
	old := capturePane
	capturePane = func(p string) string {
		captured = append(captured, p)
		return "capture of " + p
	}
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})

	// Preview is always on: initial load captures the first agent's pane.
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
}

func TestPreviewRecapturesOnReload(t *testing.T) {
	var captures []string
	old := capturePane
	capturePane = func(p string) string { captures = append(captures, p); return "x" }
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	if len(captures) != 1 {
		t.Fatalf("captures after init = %d, want 1", len(captures))
	}
	// Every auto-refresh tick is a full reload: rows refresh and the pane
	// re-captures even though the selection's pane target is unchanged.
	m = applyMsg(t, m, tickMsg{})
	if len(captures) != 2 || captures[1] != "main:0.0" {
		t.Fatalf("captures after reload = %v, want re-capture of main:0.0", captures)
	}
}

func TestPreviewViewShowsSeparatorAndContent(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "pane-content-line" }
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 120, 24

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

// TestPreviewLayoutAlignment checks that every body row has the │ separator
// in the same column and that list + sep + preview span the full width.
func TestPreviewLayoutAlignment(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string {
		return "line one of pane\nline two is longer than the panel width should truncate\nline three"
	}
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	const w, h = 100, 20
	m.width, m.height = w, h

	v := ansi.Strip(m.View())
	rows := strings.Split(v, "\n")
	if len(rows) != h {
		t.Fatalf("rows = %d, want %d", len(rows), h)
	}

	// Header + divider + body + footer: separator column only on body rows.
	// Body starts at index 2, ends at h-2 inclusive. Preview is half width.
	panelW := w / 2
	listW := w - panelW - 1
	sepCol := listW // 0-based index of │

	for i := 2; i < h-1; i++ {
		line := rows[i]
		if got := ansi.StringWidth(line); got != w {
			t.Fatalf("body row %d width = %d, want %d:\n%q", i, got, w, line)
		}
		// After Strip, list + preview are single-width ASCII so rune index
		// matches display column; the separator must sit at listW.
		runes := []rune(line)
		if sepCol >= len(runes) || runes[sepCol] != '│' {
			t.Fatalf("body row %d: expected │ at col %d, got %q\n%s", i, sepCol, line, v)
		}
	}
}

func TestPreviewPreservesColors(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string {
		return "\x1b[32mgreen text\x1b[0m\n\x1b[31mred line\x1b[0m"
	}
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 80, 16

	v := m.View()
	if !strings.Contains(v, "\x1b[32m") || !strings.Contains(v, "green text") {
		t.Fatalf("view lost green SGR / text:\n%q", v)
	}
	if !strings.Contains(v, "\x1b[31m") || !strings.Contains(v, "red line") {
		t.Fatalf("view lost red SGR / text:\n%q", v)
	}
	// Each colored preview line ends with a reset so styles cannot bleed.
	if !strings.Contains(v, "green text") || strings.Count(v, sgrReset) < 2 {
		t.Fatalf("expected SGR resets after preview lines, got %d resets", strings.Count(v, sgrReset))
	}
}

func TestPreviewIsHalfWidth(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	const w = 80
	m.width, m.height = w, 12

	v := ansi.Strip(m.View())
	body := strings.Split(v, "\n")[2]
	// panelW = w/2, listW = w - panelW - 1 → separator column.
	sepCol := w - w/2 - 1
	runes := []rune(body)
	if sepCol >= len(runes) || runes[sepCol] != '│' {
		t.Fatalf("separator at col %d, want half-width split; line=%q", sepCol, body)
	}
}

func TestFooterShowsStatusCounts(t *testing.T) {
	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 80, 24

	v := ansi.Strip(m.View())
	// testRows: 1 blocked, 1 working, 1 idle → B 1  W 1  I 1.
	for _, want := range []string{"B 1", "W 1", "I 1", "[1-9] jump"} {
		if !strings.Contains(v, want) {
			t.Fatalf("footer missing %q in:\n%s", want, v)
		}
	}
}

func TestFooterShowsActiveFilter(t *testing.T) {
	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
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
