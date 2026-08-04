package dashboard

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/theme"
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
	for _, want := range []string{"[↑/↓ j/k] navigate", "[←/→ h/l] resize preview", "[C-u/C-d] scroll preview", "[1-9] jump"} {
		if !strings.Contains(v, want) {
			t.Fatalf("footer missing %q in:\n%s", want, v)
		}
	}
}

func TestFooterShowsVersionBottomLeft(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true).WithVersion("0.4.2")
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 100, 24

	v := ansi.Strip(m.View())
	rows := strings.Split(v, "\n")
	footer := rows[len(rows)-1]
	// One space from the border, then the version.
	if !strings.HasPrefix(footer, " 0.4.2") {
		t.Fatalf("footer should start with the version, got %q", footer)
	}

	// Without a version, the footer does not start with a bare " v…".
	m2 := New(f.load, true)
	m2 = applyMsg(t, m2, initMsg{})
	m2.width, m2.height = 100, 24
	v2 := ansi.Strip(m2.View())
	footer2 := strings.Split(v2, "\n")[len(rows)-1]
	if strings.Contains(footer2, "0.4.2") {
		t.Fatalf("footer without version should not contain 0.4.2, got %q", footer2)
	}
}

func TestSelectedAgentHighlight(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{}) // Grok is selected
	m.width, m.height = 80, 24

	listW, _ := m.panelWidths(80)
	raw := m.View()

	// The selected name row is highlighted with the full-line selection
	// style: bold accent on the selection background, padded to the list
	// width. The old ">" marker is gone.
	if !strings.Contains(raw, m.st.selText.Render(fit("      Popup preview scroll (Grok Build)", listW))) {
		t.Fatalf("selected name row missing the selText highlight:\n%q", raw)
	}
	// The cwd row gets the dim selection background too.
	if !strings.Contains(raw, m.st.selDim.Render(fit("      code/tmon", listW))) {
		t.Fatalf("selected cwd row missing the selection background:\n%q", raw)
	}

	// Unselected rows keep the plain bold style and no marker.
	if !strings.Contains(raw, m.st.white.Bold(true).Render("      Claude Code")) {
		t.Fatalf("unselected name row should use the plain bold style:\n%q", raw)
	}
	for _, ln := range strings.Split(ansi.Strip(raw), "\n") {
		if strings.Contains(ln, "Claude Code") && strings.Contains(ln, ">") {
			t.Fatalf("unselected row still has a marker: %q", ln)
		}
	}
}

func TestAgentRowsAreTwoLines(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{}) // Grok selected first
	m.width, m.height = 80, 24

	v := ansi.Strip(m.View())
	lines := strings.Split(v, "\n")

	// Split each full row at the │ separator and match against the list
	// column only — the preview header repeats the selected agent's name.
	for i, ln := range lines {
		list := strings.SplitN(ln, "│", 2)[0]
		switch {
		case strings.Contains(list, "Popup preview scroll (Grok Build)"):
			if !strings.Contains(lines[i+1], "code/tmon") {
				t.Fatalf("Grok cwd line = %q, want code/tmon", lines[i+1])
			}
			if strings.Contains(lines[i+1], "paused") {
				t.Fatalf("working agent should not show pause status: %q", lines[i+1])
			}
		case strings.Contains(ln, "Claude Code"):
			if !strings.Contains(lines[i+1], "site") || !strings.Contains(lines[i+1], "paused") {
				t.Fatalf("blocked agent cwd line = %q, want site + paused", lines[i+1])
			}
		case strings.Contains(ln, "Codex CLI"):
			if !strings.Contains(lines[i+1], "blog") || strings.Contains(lines[i+1], "paused") {
				t.Fatalf("idle agent cwd line = %q, want blog only", lines[i+1])
			}
		}
	}

	// The selected name row uses the selection highlight; unselected rows
	// keep the plain bold/dim styles, and the pause status keeps the orange
	// "blocked" color.
	raw := m.View()
	listW, _ := m.panelWidths(80)
	if !strings.Contains(raw, m.st.selText.Render(fit("      Popup preview scroll (Grok Build)", listW))) {
		t.Fatal("selected agent name should be highlighted in the raw view")
	}
	if !strings.Contains(raw, m.st.white.Bold(true).Render("      Claude Code")) {
		t.Fatal("unselected agent name should stay plain bold in the raw view")
	}
	if !strings.Contains(raw, styleDim.Render("site")) {
		t.Fatal("cwd should be dimmed in the raw view")
	}
	if !strings.Contains(raw, styleOrange.Render("  paused")) {
		t.Fatal("blocked agent should show an orange pause status")
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

func TestAgentRowsThreeLinesWithUsage(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	rows := testRows()
	rows[0].Usage = agent.Usage{TokensUsed: 52367, WindowTokens: 200000} // Grok
	f := &fakeLoader{data: Data{Rows: rows}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m = applyMsg(t, m, tea.KeyMsg{Type: tea.KeyDown}) // select Claude so Grok renders unstyled
	m.width, m.height = 80, 24

	v := ansi.Strip(m.View())
	lines := strings.Split(v, "\n")

	var stats string
	for i, ln := range lines {
		list := strings.SplitN(ln, "│", 2)[0]
		switch {
		case strings.Contains(list, "Popup preview scroll (Grok Build)"):
			// The agent with usage spans three list rows: name, cwd, stats.
			if !strings.Contains(lines[i+1], "code/tmon") {
				t.Fatalf("Grok cwd line = %q, want code/tmon", lines[i+1])
			}
			stats = lines[i+2]
		case strings.Contains(list, "Claude Code"):
			// The agent without usage stays at two rows: the line after its
			// cwd is the next session header, not a stats line.
			if strings.Contains(lines[i+2], "ctx ") {
				t.Fatalf("Claude has no usage but rendered a stats line: %q", lines[i+2])
			}
		}
	}
	if !strings.Contains(stats, "ctx 52.4k/200k ██░░░░░░░░ 26%") {
		t.Fatalf("stats line = %q, want ctx 52.4k/200k ██░░░░░░░░ 26%%", stats)
	}

	// The stats text is dimmed and the bar is green in the raw view.
	raw := m.View()
	if !strings.Contains(raw, m.st.green.Render("██░░░░░░░░")) {
		t.Fatal("usage bar should be green in the raw view")
	}
}

func TestUsageLineFormat(t *testing.T) {
	m := Model{st: defaultStyles, theme: theme.Default, contextWarn: defaultContextWarn}
	cases := []struct {
		name string
		u    agent.Usage
		want string
	}{
		{"empty", agent.Usage{}, ""},
		{"tokens only", agent.Usage{TokensUsed: 13025}, "ctx 13k"},
		{"tokens and window", agent.Usage{TokensUsed: 52367, WindowTokens: 200000}, "ctx 52.4k/200k ██░░░░░░░░ 26%"},
		{"million window", agent.Usage{TokensUsed: 123456, WindowTokens: 1000000}, "ctx 123k/1M █░░░░░░░░░ 12%"},
		{"quota only", agent.Usage{QuotaPct: 38, QuotaReset: "14:00"}, "62% left · reset 14:00"},
		{"all", agent.Usage{TokensUsed: 52367, WindowTokens: 200000, QuotaPct: 38, QuotaReset: "14:00"}, "ctx 52.4k/200k ██░░░░░░░░ 26% · 62% left · reset 14:00"},
		{"over quota clamps", agent.Usage{QuotaPct: 120, QuotaReset: "14:00"}, "0% left · reset 14:00"},
		{"warn threshold", agent.Usage{TokensUsed: 180000, WindowTokens: 200000}, "ctx 180k/200k █████████░ 90%"},
	}
	for _, tc := range cases {
		if got := ansi.Strip(m.usageLine(tc.u, false)); got != tc.want {
			t.Errorf("%s: usageLine(%+v) = %q, want %q", tc.name, tc.u, got, tc.want)
		}
	}
}

func TestUsageBarColor(t *testing.T) {
	m := Model{st: defaultStyles, theme: theme.Default, contextWarn: defaultContextWarn}
	bar := func(pct int) string { return m.contextBar(pct, m.st.green, m.st.warn) }
	if got := bar(26); got != m.st.green.Render("██░░░░░░░░") {
		t.Fatalf("26%% bar = %q, want green", got)
	}
	// At the warn threshold (85%) and above, the bar switches to warn.
	if got := bar(85); got != m.st.warn.Render("████████░░") {
		t.Fatalf("85%% bar = %q, want warn", got)
	}
	if got := bar(90); got != m.st.warn.Render("█████████░") {
		t.Fatalf("90%% bar = %q, want warn", got)
	}
	// Over 100% clamps to a full bar.
	if got := bar(250); got != m.st.warn.Render("██████████") {
		t.Fatalf("250%% bar = %q, want full warn bar", got)
	}

	// A custom threshold (@tmon-context-warn) moves the warn switch point.
	m2 := m.WithContextWarn(70)
	if got := m2.contextBar(75, m2.st.green, m2.st.warn); got != m2.st.warn.Render("███████░░░") {
		t.Fatalf("75%% bar with 70%% threshold = %q, want warn", got)
	}
	// A threshold of 0 disables the warn color entirely.
	m3 := m.WithContextWarn(0)
	if got := m3.contextBar(90, m3.st.green, m3.st.warn); got != m3.st.green.Render("█████████░") {
		t.Fatalf("90%% bar with 0 threshold = %q, want green", got)
	}
}

func TestHeaderFleetCounts(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 80, 24

	// ASCII theme: one of each status — 🚨→B, ⚡️→W, 💤→I.
	raw := m.View()
	if !strings.Contains(raw, m.st.cyan.Bold(true).Render(" [@] tmon")) {
		t.Fatalf("header missing the title:\n%s", raw)
	}
	for _, want := range []string{m.st.orange.Render("B1"), m.st.green.Render("W1"), m.st.blue.Render("I1")} {
		if !strings.Contains(raw, want) {
			t.Fatalf("header missing fleet count %q in:\n%s", want, raw)
		}
	}

	// The emoji theme renders the same counts with emoji glyphs.
	m3 := m.WithTheme(theme.Default)
	raw3 := ansi.Strip(m3.View())
	for _, want := range []string{"🚨1", "⚡️1", "💤1"} {
		if !strings.Contains(raw3, want) {
			t.Fatalf("emoji header missing %q in:\n%s", want, raw3)
		}
	}

	// Only non-zero statuses are shown: a single working agent shows W only.
	rows := testRows()[:1]
	m2 := New(func() (Data, error) { return Data{Rows: rows}, nil }, true)
	m2 = applyMsg(t, m2, initMsg{})
	m2.width, m2.height = 80, 24
	header := strings.Split(ansi.Strip(m2.View()), "\n")[0]
	if !strings.Contains(header, "W1") {
		t.Fatalf("single-agent header missing W1: %q", header)
	}
	if strings.Contains(header, "B") || strings.Contains(header, "I") {
		t.Fatalf("single-agent header should only show W: %q", header)
	}
}

func TestCwdLineShowsStatusAge(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	rows := testRows()
	now := time.Now().Unix()
	rows[1].LastTs = now      // Claude blocked, just now
	rows[2].LastTs = now - 90 // Codex idle, 90s ago
	f := &fakeLoader{data: Data{Rows: rows}}
	m := New(f.load, false) // emoji theme
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 80, 24

	v := ansi.Strip(m.View())
	lines := strings.Split(v, "\n")
	for i, ln := range lines {
		list := strings.SplitN(ln, "│", 2)[0]
		switch {
		case strings.Contains(list, "Claude Code"):
			if !strings.Contains(lines[i+1], "🚨 now") {
				t.Fatalf("blocked cwd line = %q, want 🚨 now", lines[i+1])
			}
		case strings.Contains(list, "Codex CLI"):
			if !strings.Contains(lines[i+1], "💤 1m") {
				t.Fatalf("idle cwd line = %q, want 💤 1m", lines[i+1])
			}
		}
	}
}

func TestHumanTokens(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{999, "999"},
		{13025, "13k"},
		{51660, "51.7k"},
		{100000, "100k"},
		{262144, "262k"},
		{1000000, "1M"},
		{2500000, "2.5M"},
	}
	for _, tc := range cases {
		if got := humanTokens(tc.n); got != tc.want {
			t.Errorf("humanTokens(%d) = %q, want %q", tc.n, got, tc.want)
		}
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
