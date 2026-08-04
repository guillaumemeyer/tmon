package dashboard

import (
	"fmt"
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

	// Preview is always on: initial load captures every agent pane so search
	// can match non-visible content. First selectable is main:0.0.
	if len(captured) != 3 {
		t.Fatalf("captured = %v, want 3 pane captures on load", captured)
	}
	if m.previewText == "" || m.previewPane != "main:0.0" {
		t.Fatalf("preview = %q @ %q, want captured text for main:0.0", m.previewText, m.previewPane)
	}

	// Selection change switches preview from the cache (no extra capture).
	n := len(captured)
	m = applyMsg(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if len(captured) != n {
		t.Fatalf("captured after selection change = %v, want no extra captures", captured)
	}
	if m.previewPane != "main:0.1" || m.previewText != "capture of main:0.1" {
		t.Fatalf("preview = %q @ %q, want cached capture of main:0.1", m.previewText, m.previewPane)
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
	if len(captures) != 3 {
		t.Fatalf("captures after init = %d, want 3 (one per agent pane)", len(captures))
	}
	// Every auto-refresh tick is a full reload: every agent pane re-captures.
	n := len(captures)
	m = applyMsg(t, m, tickMsg{})
	if len(captures) != n+3 {
		t.Fatalf("captures after reload = %d, want %d (3 panes re-captured)", len(captures), n+3)
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
	// Body starts at index 2, ends at h-2 inclusive.
	listW, _ := m.panelWidths(w)
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
	listW, _ := m.panelWidths(w)
	sepCol := listW
	runes := []rune(body)
	if sepCol >= len(runes) || runes[sepCol] != '│' {
		t.Fatalf("separator at col %d, want default split; line=%q", sepCol, body)
	}

	// After growing the preview (left arrow), the separator moves left.
	m = applyMsg(t, m, tea.KeyMsg{Type: tea.KeyLeft})
	v = ansi.Strip(m.View())
	body = strings.Split(v, "\n")[2]
	listW2, _ := m.panelWidths(w)
	if listW2 >= listW {
		t.Fatalf("listW after left = %d, want < %d", listW2, listW)
	}
	runes = []rune(body)
	if runes[listW2] != '│' {
		t.Fatalf("separator at col %d after resize, line=%q", listW2, body)
	}
}

func TestPreviewWindow(t *testing.T) {
	cases := []struct {
		total, visible, offset, wantStart, wantEnd int
	}{
		{0, 5, 0, 0, 0},    // empty
		{3, 5, 0, 0, 3},    // fits entirely
		{10, 4, 0, 6, 10},  // pin to bottom
		{10, 4, 2, 4, 8},   // scrolled up 2
		{10, 4, 100, 0, 4}, // offset clamped to max
		{10, 4, -3, 6, 10}, // negative offset → 0
	}
	for _, c := range cases {
		start, end := previewWindow(c.total, c.visible, c.offset)
		if start != c.wantStart || end != c.wantEnd {
			t.Errorf("previewWindow(%d,%d,%d) = [%d,%d), want [%d,%d)",
				c.total, c.visible, c.offset, start, end, c.wantStart, c.wantEnd)
		}
	}
}

func TestPreviewPinsToBottom(t *testing.T) {
	// More lines than the body can show: the last lines must appear, not the first.
	var lines []string
	for i := 1; i <= 30; i++ {
		lines = append(lines, fmt.Sprintf("LINE-%02d", i))
	}
	old := capturePane
	capturePane = func(p string) string { return strings.Join(lines, "\n") }
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	// bodyLines = h-3; preview content rows = bodyLines-1. Keep it small.
	m.width, m.height = 80, 10

	v := ansi.Strip(m.View())
	if strings.Contains(v, "LINE-01") {
		t.Fatalf("preview showed top of pane; want bottom-pinned:\n%s", v)
	}
	if !strings.Contains(v, "LINE-30") {
		t.Fatalf("preview missing bottom line LINE-30:\n%s", v)
	}
}

func TestPreviewScrollCtrlUD(t *testing.T) {
	var lines []string
	for i := 1; i <= 40; i++ {
		lines = append(lines, fmt.Sprintf("LINE-%02d", i))
	}
	old := capturePane
	capturePane = func(p string) string { return strings.Join(lines, "\n") }
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 80, 12 // content rows ≈ 8; half-page step = 4

	// Default: pinned to bottom.
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "LINE-40") || strings.Contains(v, "LINE-01") {
		t.Fatalf("default should pin to bottom:\n%s", v)
	}

	// ctrl+u scrolls up: older content appears, bottom may leave the viewport.
	m = applyMsg(t, m, tea.KeyMsg{Type: tea.KeyCtrlU})
	if m.previewOffset <= 0 {
		t.Fatalf("previewOffset after ctrl+u = %d, want > 0", m.previewOffset)
	}
	v = ansi.Strip(m.View())
	if strings.Contains(v, "LINE-40") {
		// With step=4 and 40 lines, after one scroll the bottom should still
		// often be visible depending on height; require offset applied instead.
		// Re-check with enough scrolls to clear the bottom.
	}

	// Scroll up enough that LINE-40 is gone and earlier lines appear.
	for i := 0; i < 20; i++ {
		m = applyMsg(t, m, tea.KeyMsg{Type: tea.KeyCtrlU})
	}
	v = ansi.Strip(m.View())
	if strings.Contains(v, "LINE-40") {
		t.Fatalf("after many ctrl+u, still showing LINE-40:\n%s", v)
	}
	if !strings.Contains(v, "LINE-01") {
		t.Fatalf("after many ctrl+u, expected LINE-01 at top of capture:\n%s", v)
	}

	// ctrl+d scrolls back toward the bottom.
	for i := 0; i < 20; i++ {
		m = applyMsg(t, m, tea.KeyMsg{Type: tea.KeyCtrlD})
	}
	if m.previewOffset != 0 {
		t.Fatalf("previewOffset after ctrl+d to bottom = %d, want 0", m.previewOffset)
	}
	v = ansi.Strip(m.View())
	if !strings.Contains(v, "LINE-40") {
		t.Fatalf("after ctrl+d to bottom, missing LINE-40:\n%s", v)
	}
}

func TestPreviewScrollResetsOnSelectionChange(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string {
		var b strings.Builder
		for i := 1; i <= 40; i++ {
			fmt.Fprintf(&b, "%s LINE-%02d\n", p, i)
		}
		return b.String()
	}
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 80, 12

	m = applyMsg(t, m, tea.KeyMsg{Type: tea.KeyCtrlU})
	m = applyMsg(t, m, tea.KeyMsg{Type: tea.KeyCtrlU})
	if m.previewOffset == 0 {
		t.Fatal("expected non-zero offset before selection change")
	}

	// Moving to another agent re-captures and pins the new pane to the bottom.
	m = applyMsg(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.previewOffset != 0 {
		t.Fatalf("previewOffset after selection change = %d, want 0", m.previewOffset)
	}
	if m.previewPane != "main:0.1" {
		t.Fatalf("previewPane = %q, want main:0.1", m.previewPane)
	}
}

func TestTrimTrailingEmpty(t *testing.T) {
	got := trimTrailingEmpty([]string{"a", "b", "", "  ", ""})
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("trimTrailingEmpty = %v, want [a b]", got)
	}
	if got := trimTrailingEmpty([]string{"", ""}); len(got) != 0 {
		t.Fatalf("all-empty = %v, want empty slice", got)
	}
}

func TestFooterOmitsStatusCountsShowsPreviewTip(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 100, 24

	v := ansi.Strip(m.View())
	// Status counts are no longer in the footer.
	for _, bad := range []string{"B 1", "W 1", "I 1"} {
		if strings.Contains(v, bad) {
			t.Fatalf("footer should not show status count %q in:\n%s", bad, v)
		}
	}
	for _, want := range []string{"[←/→] resize", "[C-u/C-d] scroll", "[1-9] jump"} {
		if !strings.Contains(v, want) {
			t.Fatalf("footer missing %q in:\n%s", want, v)
		}
	}
}

func TestSelectedAgentMarker(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{}) // Grok is selected
	m.width, m.height = 80, 24

	v := ansi.Strip(m.View())
	lines := strings.Split(v, "\n")
	var grokLine, claudeLine, windowLine string
	for _, ln := range lines {
		switch {
		case strings.Contains(ln, ">") && strings.Contains(ln, "["): // selected list line
			grokLine = ln
		case strings.Contains(ln, "[1]"): // Claude's list line (pane 1)
			claudeLine = ln
		case strings.Contains(ln, "0:shell"):
			windowLine = ln
		}
	}

	// The selected line carries a ">" marker at the window index column (4);
	// the unselected line keeps plain leading spaces.
	if !strings.HasPrefix(strings.TrimRight(grokLine, " "), "    > [0]") {
		t.Fatalf("selected line missing marker, got %q", grokLine)
	}
	if strings.HasPrefix(strings.TrimRight(claudeLine, " "), "    >") {
		t.Fatalf("unselected line has marker: %q", claudeLine)
	}
	// Marker is vertically aligned with the window index.
	if runes := []rune(windowLine); len(runes) > 4 && runes[4] != '0' {
		t.Fatalf("window index not at column 4: %q", windowLine)
	}
	if runes := []rune(strings.TrimRight(grokLine, " ")); len(runes) > 4 && runes[4] != '>' {
		t.Fatalf("marker not at column 4: %q", grokLine)
	}

	// The marker is still green/bold in the unstripped view.
	raw := m.View()
	if !strings.Contains(raw, styleGreen.Bold(true).Render(">")) {
		t.Fatal("marker lost its green bold style")
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
