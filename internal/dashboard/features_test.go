package dashboard

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/theme"
	"github.com/muesli/termenv"
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

	// Idle load only captures the selected pane for the preview panel
	// (full multi-pane cache waits until the user opens search with /).
	if len(captured) != 1 || captured[0] != "main:0.0" {
		t.Fatalf("captured = %v, want only selected pane main:0.0 on load", captured)
	}
	if m.previewText == "" || m.previewPane != "main:0.0" {
		t.Fatalf("preview = %q @ %q, want captured text for main:0.0", m.previewText, m.previewPane)
	}

	// Selection change re-captures the new selection (no full-cache).
	n := len(captured)
	m = applyMsg(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if len(captured) != n+1 {
		t.Fatalf("captured after selection change = %v, want one extra capture", captured)
	}
	if m.previewPane != "main:0.1" || m.previewText != "capture of main:0.1" {
		t.Fatalf("preview = %q @ %q, want capture of main:0.1", m.previewText, m.previewPane)
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
	// Without search mode, only the selected pane is captured per load.
	if len(captures) != 1 {
		t.Fatalf("captures after init = %d, want 1 (selected pane only)", len(captures))
	}
	n := len(captures)
	_ = applyMsg(t, m, tickMsg{})
	if len(captures) != n+1 {
		t.Fatalf("captures after reload = %d, want %d (selected pane re-captured)", len(captures), n+1)
	}
}

func TestRefreshPaneCacheCapturesAllPanesOnce(t *testing.T) {
	// Parallel capture must hit every live pane exactly once and prune
	// stale targets. The fake is shared across workers, so it records
	// under a mutex. Run under -race.
	var mu sync.Mutex
	captured := make(map[string]int)
	old := capturePane
	capturePane = func(p string) string {
		mu.Lock()
		captured[p]++
		mu.Unlock()
		return "content of " + p
	}
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m.rows = testRows()
	m.paneCache = map[string]string{"old:9.9": "stale"}
	m.refreshPaneCache()

	if len(captured) != 3 {
		t.Fatalf("captured = %v, want 3 distinct panes", captured)
	}
	for p, n := range captured {
		if n != 1 {
			t.Errorf("pane %q captured %d times, want 1", p, n)
		}
		if m.paneCache[p] != "content of "+p {
			t.Errorf("paneCache[%q] = %q, want captured content", p, m.paneCache[p])
		}
	}
	if _, ok := m.paneCache["old:9.9"]; ok {
		t.Error("stale pane target was not pruned")
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

	// Top border, mainHeaderHeight header rows, divider, then the body;
	// another divider, footer, and bottom border below. The │ separator
	// column lives on body rows only: body starts right after the top
	// divider, ends before the bottom divider.
	listW, _ := m.panelWidths(w - 2)
	sepCol := listW + 1 // 0-based index of │, one cell in from the left border
	bodyStart := 1 + mainHeaderHeight + 1

	for i := bodyStart; i < h-3; i++ {
		line := rows[i]
		if got := ansi.StringWidth(line); got != w {
			t.Fatalf("body row %d width = %d, want %d:\n%q", i, got, w, line)
		}
		// After Strip, list + preview are single-width ASCII so rune index
		// matches display column; the separator must sit at sepCol.
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

func TestPopupPaintedWithThemeBackground(t *testing.T) {
	// The test runner's terminal is colorless (lipgloss's Ascii profile);
	// force truecolor so the background sequences are actually emitted.
	orig := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(orig) })

	old := capturePane
	capturePane = func(p string) string {
		return "\x1b[32mgreen text\x1b[0m\n\x1b[49mstriped\x1b[0m\n\x1b[48;5;0mblackbg\x1b[0m"
	}
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 100, 24

	seq := backgroundSeq(m.st.bg)
	if seq == "" {
		t.Fatal("the default theme should define a popup background")
	}

	// Every row of the popup starts with the theme background so the panel
	// is solid, not transparent.
	lines := strings.Split(m.View(), "\n")
	if len(lines) != m.height {
		t.Fatalf("View = %d lines, want %d", len(lines), m.height)
	}
	for i, ln := range lines {
		if !strings.HasPrefix(ln, seq) {
			t.Fatalf("line %d not painted with the theme background:\n%q", i, ln)
		}
	}

	// Embedded SGR resets (from the colored pane capture) must not punch
	// holes in the background: every reset is followed by a re-assertion.
	for i, ln := range lines {
		if strings.Count(ln, sgrReset) > strings.Count(ln, seq) {
			t.Fatalf("line %d has a reset without a background re-assertion:\n%q", i, ln)
		}
		// A \x1b[49m (default background) reset gets the same treatment.
		if strings.Contains(ln, "striped") && strings.Count(ln, seq) < 2 {
			t.Fatalf("line %d lost its background after a \\x1b[49m reset:\n%q", i, ln)
		}
		// An explicit background from the capture is overridden too, so the
		// preview stays a solid theme-colored panel.
		if strings.Contains(ln, "blackbg") && strings.Count(ln, seq) < 3 {
			t.Fatalf("line %d kept a capture background instead of the theme's:\n%q", i, ln)
		}
	}

	// Switching theme repaints the popup with the new background.
	m2 := m.WithTheme(theme.Resolve(theme.Options{Name: "nord"}))
	seq2 := backgroundSeq(m2.st.bg)
	if seq2 == seq {
		t.Fatal("nord background should differ from the default theme's")
	}
	if v2 := m2.View(); !strings.HasPrefix(v2, seq2) {
		t.Fatal("nord popup not painted with its own background")
	}
}

// TestReassertBackground checks that every SGR form which leaves the
// background at the terminal default gets the theme background re-asserted
// after it, while explicit backgrounds and unrelated codes pass through.
func TestReassertBackground(t *testing.T) {
	seq := "\x1b[48;5;235m"
	cases := []struct {
		name string
		in   string
		want string // "" means the input must pass through unchanged
	}{
		{"plain text", "hello", ""},
		{"non-sgr csi untouched", "a\x1b[2Jb", ""},
		{"cursor move untouched", "a\x1b[1;5Hb", ""},
		{"bare reset", "a\x1b[mb", "a\x1b[m" + seq + "b"},
		{"full reset", "a\x1b[0mb", "a\x1b[0m" + seq + "b"},
		{"default bg", "a\x1b[49mb", "a\x1b[49m" + seq + "b"},
		{"combined fg+bg reset", "a\x1b[39;49mb", "a\x1b[39;49m" + seq + "b"},
		{"reset with fg", "a\x1b[0;31mb", "a\x1b[0;31m" + seq + "b"},
		{"fg only keeps bg", "a\x1b[31mb", ""},
		{"attributes keep bg", "a\x1b[1;5mb", ""},
		{"explicit indexed bg kept", "a\x1b[48;5;0mb", ""},
		{"explicit rgb bg kept", "a\x1b[48;2;10;20;30mb", ""},
		{"explicit bg then fg kept", "a\x1b[48;5;0m\x1b[31mb", ""},
		{"fg color index is not a reset", "a\x1b[38;5;49mb", ""},
		{"explicit bg then reset", "a\x1b[48;5;0mb\x1b[0mc", "a\x1b[48;5;0mb\x1b[0m" + seq + "c"},
		{"default after rgb bg", "a\x1b[48;2;10;20;30m\x1b[49mb", "a\x1b[48;2;10;20;30m\x1b[49m" + seq + "b"},
		{"reset then re-set bg", "a\x1b[0m\x1b[48;5;0mb", "a\x1b[0m" + seq + "\x1b[48;5;0mb"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := reassertBackground(tc.in, seq)
			if tc.want == "" {
				if got != tc.in {
					t.Fatalf("reassertBackground(%q) = %q, want %q unchanged", tc.in, got, tc.in)
				}
				return
			}
			if got != tc.want {
				t.Fatalf("reassertBackground(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestForceBackground checks that the preview's background override makes
// the theme background win over any background the captured content sets,
// while leaving foreground colors and attributes alone.
func TestForceBackground(t *testing.T) {
	seq := "\x1b[48;5;235m"
	cases := []struct {
		name string
		in   string
		want string // "" means the input must pass through unchanged
	}{
		{"plain text", "hello", ""},
		{"non-sgr csi untouched", "a\x1b[2Jb", ""},
		{"fg only untouched", "a\x1b[31mb", ""},
		{"attributes untouched", "a\x1b[1;5mb", ""},
		{"fg color index is not a reset", "a\x1b[38;5;49mb", ""},
		{"bare reset", "a\x1b[mb", "a\x1b[m" + seq + "b"},
		{"full reset", "a\x1b[0mb", "a\x1b[0m" + seq + "b"},
		{"default bg", "a\x1b[49mb", "a\x1b[49m" + seq + "b"},
		{"reset with fg", "a\x1b[0;31mb", "a\x1b[0;31m" + seq + "b"},
		{"indexed bg overridden", "a\x1b[48;5;0mb", "a\x1b[48;5;0m" + seq + "b"},
		{"red bg overridden", "a\x1b[41mb", "a\x1b[41m" + seq + "b"},
		{"bright bg overridden", "a\x1b[101mb", "a\x1b[101m" + seq + "b"},
		{"rgb bg overridden", "a\x1b[48;2;10;20;30mb", "a\x1b[48;2;10;20;30m" + seq + "b"},
		{"fg after overridden bg keeps theme", "a\x1b[48;5;0m\x1b[31mb", "a\x1b[48;5;0m" + seq + "\x1b[31mb"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := forceBackground(tc.in, seq)
			if tc.want == "" {
				if got != tc.in {
					t.Fatalf("forceBackground(%q) = %q, want %q unchanged", tc.in, got, tc.in)
				}
				return
			}
			if got != tc.want {
				t.Fatalf("forceBackground(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
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
	body := strings.Split(v, "\n")[1+mainHeaderHeight+1] // first body row, below border+header+divider
	listW, _ := m.panelWidths(w - 2)
	sepCol := listW + 1
	runes := []rune(body)
	if sepCol >= len(runes) || runes[sepCol] != '│' {
		t.Fatalf("separator at col %d, want default split; line=%q", sepCol, body)
	}

	// After growing the preview (left arrow), the separator moves left.
	m = applyMsg(t, m, tea.KeyMsg{Type: tea.KeyLeft})
	v = ansi.Strip(m.View())
	body = strings.Split(v, "\n")[1+mainHeaderHeight+1]
	listW2, _ := m.panelWidths(w - 2)
	if listW2 >= listW {
		t.Fatalf("listW after left = %d, want < %d", listW2, listW)
	}
	runes = []rune(body)
	if runes[listW2+1] != '│' {
		t.Fatalf("separator at col %d after resize, line=%q", listW2+1, body)
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
	// Tall enough for header + a few content rows + preview bottom chrome.
	m.width, m.height = 80, 16

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
	m.width, m.height = 80, 12 // preview content rows ≈ 8; half-page step = 4

	// Default: pinned to bottom.
	if !m.preview.AtBottom() {
		t.Fatal("default should pin the preview to the bottom")
	}
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "LINE-40") || strings.Contains(v, "LINE-01") {
		t.Fatalf("default should pin to bottom:\n%s", v)
	}

	// ctrl+u scrolls up: older content appears, the bottom leaves the view.
	for i := 0; i < 20; i++ {
		m = applyMsg(t, m, tea.KeyMsg{Type: tea.KeyCtrlU})
	}
	if m.preview.AtBottom() {
		t.Fatal("after many ctrl+u the preview should not be at the bottom")
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
	if !m.preview.AtBottom() {
		t.Fatal("after ctrl+d to bottom, the preview should be at the bottom")
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
	if m.preview.AtBottom() {
		t.Fatal("expected non-bottom preview before selection change")
	}

	// Moving to another agent re-captures and pins the new pane to the bottom.
	m = applyMsg(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if !m.preview.AtBottom() {
		t.Fatal("preview should pin to bottom after selection change")
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

func TestTrimEmptyEdges(t *testing.T) {
	got := trimEmptyEdges([]string{"", "  ", "a", "b", "", "\x1b[0m", ""})
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("trimEmptyEdges = %v, want [a b]", got)
	}
	if got := trimEmptyEdges([]string{"", ""}); len(got) != 0 {
		t.Fatalf("all-empty = %v, want empty slice", got)
	}
}

// TestPreviewFollowsBottomOnRefresh covers the "content mid-panel" bug: a
// force reload grows the capture while the user is still following the
// tail. The preview must stay on the latest lines, not the old YOffset.
func TestPreviewFollowsBottomOnRefresh(t *testing.T) {
	var n int
	old := capturePane
	capturePane = func(p string) string {
		n++
		var lines []string
		// First capture is short (fits in the default viewport height);
		// second is long enough that YOffset 0 would leave the tail off-screen.
		count := 10
		if n > 1 {
			count = 40
		}
		for i := 1; i <= count; i++ {
			lines = append(lines, fmt.Sprintf("LINE-%02d", i))
		}
		return strings.Join(lines, "\n")
	}
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 80, 12 // body preview rows ≈ 8

	if !m.previewFollowBottom {
		t.Fatal("expected follow-bottom after init")
	}
	// Force a second load with longer content (same pane).
	m = applyMsg(t, m, tickMsg{})
	if !m.previewFollowBottom {
		t.Fatal("force refresh must keep follow-bottom")
	}
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "LINE-40") {
		t.Fatalf("after refresh, want tail LINE-40:\n%s", v)
	}
	if strings.Contains(v, "LINE-01") {
		t.Fatalf("after refresh, still showing top of capture:\n%s", v)
	}
}

// TestPreviewShortContentBottomAligned checks that a short pane capture
// sits on the bottom of the preview panel (terminal style), not mid-panel
// with empty rows underneath.
func TestPreviewShortContentBottomAligned(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string {
		return "only\nthree\nlines"
	}
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	const w, h = 80, 24
	m.width, m.height = w, h

	v := ansi.Strip(m.View())
	rows := strings.Split(v, "\n")
	listW, _ := m.panelWidths(w - 2)
	sepCol := listW + 1 // 0-based │ index, one cell in from the left border
	// body: after top border + header rows + top divider; before bottom
	// divider + footer + bottom border.
	bodyStart := 1 + mainHeaderHeight + 1
	bodyEnd := h - 3

	var prevBody []string
	for i := bodyStart; i < bodyEnd; i++ {
		runes := []rune(rows[i])
		if sepCol+1 >= len(runes) {
			t.Fatalf("row %d too short for sepCol %d: %q", i, sepCol, rows[i])
		}
		// Preview cells sit after the separator; drop the outer right border.
		cell := string(runes[sepCol+1:])
		if len(cell) > 0 {
			// strip trailing border glyph if present
			cr := []rune(cell)
			if cr[len(cr)-1] == '│' {
				cell = string(cr[:len(cr)-1])
			}
		}
		prevBody = append(prevBody, strings.TrimSpace(cell))
	}
	if len(prevBody) < 7 {
		t.Fatalf("too few preview body rows: %v\n%s", prevBody, v)
	}
	// The first five body rows are the stats block (usage header, divider,
	// session line, separator) plus the agent header; the last two are
	// separator + tips. The viewport sits between them and is bottom-aligned.
	body := prevBody[5:]
	if len(body) < 5 {
		t.Fatalf("viewport body too short: %v", body)
	}
	// Drop the preview chrome (separator + tips).
	content := body[:len(body)-2]
	if len(content) < 3 {
		t.Fatalf("content area too short: %v", content)
	}
	got := content[len(content)-3:]
	want := []string{"only", "three", "lines"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bottom of preview content = %v, want %v\nfull body: %v\n%s", got, want, body, v)
	}
	for _, row := range content[:len(content)-3] {
		if row != "" {
			t.Fatalf("non-blank row above short content %q in %v", row, content)
		}
	}
}

// TestPreviewHeightMismatchStillPins covers GotoBottom under the default
// viewport height (20) while View uses a smaller body: without follow-
// bottom, AtBottom() is false and the mid-capture is shown.
func TestPreviewHeightMismatchStillPins(t *testing.T) {
	var lines []string
	for i := 1; i <= 50; i++ {
		lines = append(lines, fmt.Sprintf("LINE-%02d", i))
	}
	old := capturePane
	capturePane = func(p string) string { return strings.Join(lines, "\n") }
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	// Short popup, but tall enough for content + preview chrome; still well
	// under the default viewport height of 20.
	m.width, m.height = 80, 16

	v := ansi.Strip(m.View())
	if !strings.Contains(v, "LINE-50") {
		t.Fatalf("want bottom line with short panel height:\n%s", v)
	}
	if strings.Contains(v, "LINE-01") || strings.Contains(v, "LINE-20") {
		t.Fatalf("showed mid/top of capture instead of tail:\n%s", v)
	}
}

func TestFooterOmitsStatusCountsShowsPreviewTip(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	// Wide enough that footer and preview tips fit.
	m.width, m.height = 140, 24

	v := ansi.Strip(m.View())
	// Status counts are no longer in the footer.
	for _, bad := range []string{"B 1", "W 1", "I 1"} {
		if strings.Contains(v, bad) {
			t.Fatalf("footer should not show status count %q in:\n%s", bad, v)
		}
	}
	for _, want := range []string{"[t] theme", "[v] view (List)", "[↑/↓ j/k] navigate"} {
		if !strings.Contains(v, want) {
			t.Fatalf("footer missing %q in:\n%s", want, v)
		}
	}
	// Scroll / resize tips sit at the bottom of the preview pane, not the footer.
	for _, want := range []string{"[C-u/C-d] scroll preview", "[←/→ h/l · drag │] resize preview"} {
		if !strings.Contains(v, want) {
			t.Fatalf("preview pane missing %q in:\n%s", want, v)
		}
	}
	// Footer line must not carry the preview tips.
	footer := strings.Split(v, "\n")[m.height-2]
	for _, bad := range []string{"scroll preview", "resize preview"} {
		if strings.Contains(footer, bad) {
			t.Fatalf("footer should not contain %q, got %q", bad, footer)
		}
	}
	// Theme hint is in the footer (left of view), not the header.
	if !strings.Contains(footer, "[t] theme") {
		t.Fatalf("footer missing theme hint, got %q", footer)
	}
	if idxTheme, idxView := strings.Index(footer, "[t] theme"), strings.Index(footer, "[v] view"); idxTheme < 0 || idxView < 0 || idxTheme > idxView {
		t.Fatalf("theme hint should sit left of view hint, got %q", footer)
	}
	rawLines := strings.Split(v, "\n")
	if strings.Contains(rawLines[1], "[t] theme") {
		t.Fatalf("theme hint should not be on the top header row, got: %q", rawLines[1])
	}
}

func TestHeaderShowsVersionNextToLogo(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true).WithVersion("v0.4.2")
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 100, 24

	v := ansi.Strip(m.View())
	rows := strings.Split(v, "\n")
	// Header row 2 is below the top border and the first logo line.
	if len(rows) < 3 {
		t.Fatalf("view too short: %d lines", len(rows))
	}
	logoLine2 := rows[2] // border, logo[0], logo[1]
	// One space margin between the wordmark and the version.
	want := asciiLogo[1] + " v0.4.2"
	if !strings.Contains(logoLine2, want) {
		t.Fatalf("second logo line should contain %q, got %q", want, logoLine2)
	}
	// Version is not in the footer.
	footer := rows[len(rows)-2]
	if strings.Contains(footer, "v0.4.2") {
		t.Fatalf("footer should not contain the version, got %q", footer)
	}

	// Without a version, the logo line has no version suffix.
	m2 := New(f.load, true)
	m2 = applyMsg(t, m2, initMsg{})
	m2.width, m2.height = 100, 24
	v2 := ansi.Strip(m2.View())
	logo2 := strings.Split(v2, "\n")[2]
	if strings.Contains(logo2, "v0.4.2") {
		t.Fatalf("logo line without version should not contain v0.4.2, got %q", logo2)
	}
}

func TestFooterTipsHaveRightMargin(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 100, 24

	footer := ansi.Strip(strings.Split(m.View(), "\n")[m.height-2]) // above the bottom border
	// The tips leave exactly one blank cell before the right border.
	content := strings.TrimSuffix(footer, "│")
	if strings.TrimRight(content, " ")+" " != content {
		t.Fatalf("footer should end with one space of margin before the border, got %q", footer)
	}
}

func TestIdentityColorsInListAndPreview(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{}) // Grok selected first
	m.width, m.height = 120, 24
	listW, _ := m.panelWidths(120 - 2)

	raw := m.View()
	// Selected Grok name: status icon (the working spinner frame) + identity
	// color on selBg.
	bg := m.st.selBgColor
	grokLine := selFit(m.st,
		m.st.selBg.Render(" ")+
			statusStyle(m.st, agent.StatusWorking).Background(bg).Render(m.spinnerFrame()+" ")+
			identityStyle(m.st, "Grok").Bold(true).Background(bg).Render("Popup preview scroll (Grok Build)"),
		listW,
	)
	if !strings.Contains(raw, grokLine) {
		t.Fatalf("selected Grok missing identity+selBg highlight:\n%q", raw)
	}
	// Preview header uses Grok identity color.
	if !strings.Contains(raw, identityStyle(m.st, "Grok").Bold(true).Render("Popup preview scroll (Grok Build)")) {
		t.Fatalf("preview header missing Grok identity color:\n%q", raw)
	}
	// Unselected Claude: status icon + brand orange on the list name.
	claudeLine := " " +
		statusStyle(m.st, agent.StatusBlocked).Render("B ") +
		identityStyle(m.st, "Claude").Bold(true).Render("Claude Code")
	if !strings.Contains(raw, claudeLine) {
		t.Fatalf("Claude list row missing identity color:\n%q", raw)
	}

	// Select Claude: preview header uses Claude identity color.
	m = applyMsg(t, m, tea.KeyMsg{Type: tea.KeyDown})
	raw = m.View()
	if !strings.Contains(raw, identityStyle(m.st, "Claude").Bold(true).Render("Claude Code")) {
		t.Fatalf("preview header should use Claude identity color:\n%q", raw)
	}
}

func TestSelectedAgentHighlight(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{}) // Grok is selected
	m.width, m.height = 120, 24

	listW, _ := m.panelWidths(120 - 2)
	raw := m.View()

	// The selected name row uses status icon (the working spinner frame) +
	// agent identity color on the selection background, padded to the list
	// width. The old ">" marker is gone.
	bg := m.st.selBgColor
	nameLine := selFit(m.st,
		m.st.selBg.Render(" ")+
			statusStyle(m.st, agent.StatusWorking).Background(bg).Render(m.spinnerFrame()+" ")+
			identityStyle(m.st, "Grok").Bold(true).Background(bg).Render("Popup preview scroll (Grok Build)"),
		listW,
	)
	if !strings.Contains(raw, nameLine) {
		t.Fatalf("selected name row missing the identity+selBg highlight:\n%q", raw)
	}
	// The project row gets the dim selection background too.
	if !strings.Contains(raw, m.st.selDim.Render(fit(" project: code/tmon", listW))) {
		t.Fatalf("selected project row missing the selection background:\n%q", raw)
	}
	// The location row gets it across the full width as well. It used to be
	// pre-rendered with a dim style before the selection wrap, and the inner
	// SGR reset dropped the highlight from the trailing padding.
	if !strings.Contains(raw, m.st.selDim.Render(fit(" location: main / shell / 0", listW))) {
		t.Fatalf("selected location row missing the selection background:\n%q", raw)
	}

	// Unselected rows use status icon + agent identity color (bold) and no marker.
	claudeLine := " " +
		statusStyle(m.st, agent.StatusBlocked).Render("B ") +
		identityStyle(m.st, "Claude").Bold(true).Render("Claude Code")
	if !strings.Contains(raw, claudeLine) {
		t.Fatalf("unselected name row should use the identity color:\n%q", raw)
	}
	for _, ln := range strings.Split(ansi.Strip(raw), "\n") {
		if strings.Contains(ln, "Claude Code") && strings.Contains(ln, ">") {
			t.Fatalf("unselected row still has a marker: %q", ln)
		}
	}
}

func TestGitTagString(t *testing.T) {
	cases := []struct {
		name string
		r    Row
		want string
	}{
		{"no git", Row{}, ""},
		{"branch only", Row{Branch: "main"}, " (main)"},
		{"branch and pr", Row{Branch: "main", PR: "42"}, " (main · #42)"},
		{"pr without branch", Row{PR: "42"}, ""},
		{"slash branch", Row{Branch: "feat/x"}, " (feat/x)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := gitTagString(c.r); got != c.want {
				t.Errorf("gitTagString(%+v) = %q, want %q", c.r, got, c.want)
			}
		})
	}
}

func TestGitTagRendersOnProjectLine(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	rows := testRows()
	rows[0].GitRoot = "/home/u/code/tmon"
	rows[0].Branch = "main"
	rows[0].PR = "42"
	f := &fakeLoader{data: Data{Rows: rows}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 120, 24

	v := ansi.Strip(m.View())
	lines := strings.Split(v, "\n")
	found := false
	for i, ln := range lines {
		parts := strings.SplitN(ln, "│", 3)
		if len(parts) < 3 {
			continue // top/bottom border
		}
		// Match the list column only: the preview header repeats the
		// selected agent's name two rows lower.
		if !strings.Contains(parts[1], "Popup preview scroll (Grok Build)") {
			continue
		}
		if !strings.Contains(lines[i+1], "project: code/tmon") {
			t.Fatalf("Grok project line = %q, want project: code/tmon", lines[i+1])
		}
		if !strings.Contains(lines[i+1], "(main · #42)") {
			t.Fatalf("Grok project line = %q, want git tag (main · #42)", lines[i+1])
		}
		found = true
	}
	if !found {
		t.Fatal("Grok row not rendered")
	}

	// An agent outside a repository renders project: cwd with no git tag.
	rows2 := testRows() // no git fields
	f2 := &fakeLoader{data: Data{Rows: rows2}}
	m2 := New(f2.load, true)
	m2 = applyMsg(t, m2, initMsg{})
	m2.width, m2.height = 120, 24
	v2Lines := strings.Split(ansi.Strip(m2.View()), "\n")
	for i, ln := range v2Lines {
		parts := strings.SplitN(ln, "│", 3)
		if len(parts) < 3 {
			continue // top/bottom border
		}
		if !strings.Contains(parts[1], "Popup preview scroll (Grok Build)") {
			continue
		}
		if !strings.Contains(v2Lines[i+1], "project: code/tmon") {
			t.Fatalf("no-git project line = %q, want project: code/tmon", v2Lines[i+1])
		}
		if strings.Contains(v2Lines[i+1], "(main") {
			t.Fatalf("no-git project line = %q, want no git tag", v2Lines[i+1])
		}
	}
}

func TestSearchMatchesBranchAndPR(t *testing.T) {
	rows := testRows()
	rows[0].Branch = "feat/login"
	rows[0].PR = "42"
	rows[1].Branch = "main"
	f := &fakeLoader{data: Data{Rows: rows}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})

	// "login" hits the branch field.
	m = applyMsg(t, m, key('/'))
	for _, r := range []rune{'l', 'o', 'g', 'i', 'n'} {
		m = applyMsg(t, m, key(r))
	}
	if len(m.filtered) != 1 || m.rows[m.filtered[0]].Label != "Grok" {
		t.Fatalf("branch search: filtered = %v, want only Grok", labelsOf(m))
	}

	// Reset and search the PR number.
	m = applyMsg(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	m = applyMsg(t, m, key('/'))
	for _, r := range []rune{'4', '2'} {
		m = applyMsg(t, m, key(r))
	}
	if len(m.filtered) != 1 || m.rows[m.filtered[0]].Label != "Grok" {
		t.Fatalf("PR search: filtered = %v, want only Grok", labelsOf(m))
	}
}

func TestAgentRowsAreTwoLines(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{}) // Grok selected first
	// Wide enough that status icon + session title fit in the list column.
	m.width, m.height = 120, 24

	v := ansi.Strip(m.View())
	lines := strings.Split(v, "\n")

	// Each row is │ list │ preview │: match against the list column only —
	// the preview header repeats the selected agent's name. The top/bottom
	// border rows have no │ and are skipped.
	for i, ln := range lines {
		parts := strings.SplitN(ln, "│", 3)
		if len(parts) < 3 {
			continue
		}
		list := parts[1]
		switch {
		case strings.Contains(list, "Popup preview scroll (Grok Build)"):
			// The working agent's status slot is the animated spinner frame
			// (frame 0 is "|"), not the static "W".
			if !strings.Contains(list, ansi.Strip(m.spinnerFrame())+" Popup preview scroll") {
				t.Fatalf("Grok name line = %q, want spinner frame before name", list)
			}
			if !strings.Contains(lines[i+1], "project: code/tmon") {
				t.Fatalf("Grok project line = %q, want project: code/tmon", lines[i+1])
			}
			if strings.Contains(lines[i+1], "paused") {
				t.Fatalf("working agent should not show pause status: %q", lines[i+1])
			}
		case strings.Contains(list, "Claude Code"):
			if !strings.Contains(list, "B Claude Code") {
				t.Fatalf("Claude name line = %q, want status icon before name", list)
			}
			if !strings.Contains(lines[i+1], "project: site") || !strings.Contains(lines[i+1], "paused") {
				t.Fatalf("blocked agent project line = %q, want project: site + paused", lines[i+1])
			}
		case strings.Contains(list, "Codex CLI"):
			if !strings.Contains(list, "I Codex CLI") {
				t.Fatalf("Codex name line = %q, want status icon before name", list)
			}
			if !strings.Contains(lines[i+1], "project: blog") || strings.Contains(lines[i+1], "paused") {
				t.Fatalf("idle agent project line = %q, want project: blog only", lines[i+1])
			}
		}
	}

	// The selected name row uses status icon + identity color on selBg;
	// unselected rows use the same pattern, and the pause status keeps
	// the orange "blocked" color.
	raw := m.View()
	listW, _ := m.panelWidths(120 - 2)
	bg := m.st.selBgColor
	nameLine := selFit(m.st,
		m.st.selBg.Render(" ")+
			statusStyle(m.st, agent.StatusWorking).Background(bg).Render(m.spinnerFrame()+" ")+
			identityStyle(m.st, "Grok").Bold(true).Background(bg).Render("Popup preview scroll (Grok Build)"),
		listW,
	)
	if !strings.Contains(raw, nameLine) {
		t.Fatal("selected agent name should be identity-colored on selBg")
	}
	claudeLine := " " +
		statusStyle(m.st, agent.StatusBlocked).Render("B ") +
		identityStyle(m.st, "Claude").Bold(true).Render("Claude Code")
	if !strings.Contains(raw, claudeLine) {
		t.Fatal("unselected agent name should use identity color")
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

func TestContextLineMovesToPreview(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	rows := testRows()
	rows[0].Usage = agent.Usage{TokensUsed: 52367, WindowTokens: 200000} // Grok
	f := &fakeLoader{data: Data{Rows: rows}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{}) // Grok selected first
	// Wide enough that status icon + session title fit in the list column.
	m.width, m.height = 120, 24

	v := ansi.Strip(m.View())
	lines := strings.Split(v, "\n")

	// Every agent spans exactly three rows: name, project, pane. The
	// context line is gone from the list column.
	for i, ln := range lines {
		parts := strings.SplitN(ln, "│", 3)
		if len(parts) < 3 {
			continue // top/bottom border
		}
		list := parts[1]
		switch {
		case strings.Contains(list, "Popup preview scroll (Grok Build)"):
			if !strings.Contains(lines[i+1], "project: code/tmon") {
				t.Fatalf("Grok project line = %q, want project: code/tmon", lines[i+1])
			}
			if !strings.Contains(lines[i+2], "location: main / shell / 0") {
				t.Fatalf("Grok pane line = %q, want location: main / shell / 0", lines[i+2])
			}
		}
		if strings.Contains(list, "context:") || strings.Contains(list, "usage:") ||
			strings.Contains(list, "📊 Usage") || strings.Contains(list, "💬 Session") {
			t.Fatalf("list column still carries a usage line: %q", ln)
		}
	}

	// The usage section stays on top. With no quota windows the header
	// shows a bare "📊 Usage: ?": the percent bar and the dollar amounts
	// only exist when a provider reports a quota window. The session line
	// carries the token counts next to the "💬 Session:" label, and the
	// context bar sits on its own line under it. The preview panel is 59
	// cells here, so the progress bar takes the full 30-cell cap; 26% of
	// 30 fills 8 cells (rounded).
	if !strings.Contains(v, "📊 Usage: ?") {
		t.Fatalf("preview missing the no-quota usage header:\n%s", v)
	}
	if !strings.Contains(v, "💬 Session: 52.4k/200k") {
		t.Fatalf("preview missing the session line with token counts:\n%s", v)
	}
	if !strings.Contains(v, "████████░░░░░░░░░░░░░░░░░░░░░░ 26%") {
		t.Fatalf("preview missing the Grok context bar:\n%s", v)
	}

	// An agent without token stats or quota still shows "📊 Usage: ?" and
	// "💬 Session: ?" in the preview.
	m = applyMsg(t, m, tea.KeyMsg{Type: tea.KeyDown}) // select Claude
	v = ansi.Strip(m.View())
	if !strings.Contains(v, "📊 Usage: ?") {
		t.Fatalf("preview should show 📊 Usage: ? for Claude:\n%s", v)
	}
	if !strings.Contains(v, "💬 Session: ?") {
		t.Fatalf("preview should show 💬 Session: ? for Claude:\n%s", v)
	}
}

func TestQuotaWindowsInPreview(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	// Claude carries three quota windows (session, weekly all-models,
	// weekly per-model) plus the monthly extra-usage dollar window; the
	// other agents have none. The usage lines render at the top of the
	// preview pane for the selected agent.
	rows := testRows()
	rows[1].Usage = agent.Usage{QuotaWindows: []agent.QuotaWindow{
		{Pct: 0, Label: "Current session"},
		{Pct: 5, Label: "Current week (all models)"},
		{Pct: 2, Label: "Current week (Fable)"},
		{Limit: 100, Spend: 18.56, Label: "Extra usage (monthly)", Currency: "$"},
	}}
	f := &fakeLoader{data: Data{Rows: rows}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m = applyMsg(t, m, tea.KeyMsg{Type: tea.KeyDown}) // select Claude
	m.width, m.height = 120, 24

	v := ansi.Strip(m.View())
	lines := strings.Split(v, "\n")

	// The list column stays three lines per agent: no usage or quota rows.
	for _, ln := range lines {
		parts := strings.SplitN(ln, "│", 3)
		if len(parts) < 3 {
			continue // top/bottom border
		}
		if strings.Contains(parts[1], "usage:") || strings.Contains(parts[1], "context:") ||
			strings.Contains(parts[1], "📊 Usage") || strings.Contains(parts[1], "💬 Session") {
			t.Fatalf("list column still carries a usage line: %q", ln)
		}
	}

	// The preview pane leads with the "📊 Usage:" header and one row per
	// quota window — the percent windows render bars, the monthly
	// extra-usage window renders its dollar amounts — above the
	// "💬 Session" line.
	for _, want := range []string{"📊 Usage", "Current session", "Current week (all models)", "Current week (Fable)", "💬 Session"} {
		if !strings.Contains(v, want) {
			t.Errorf("preview missing %q:\n%s", want, v)
		}
	}
	if !strings.Contains(v, "$18.56 used · $81.44 left · Extra usage (monthly)") {
		t.Errorf("preview missing the dollar window:\n%s", v)
	}
	// With quota data present the header reads "📊 Usage", not "📊 Usage: ?".
	if strings.Contains(v, "📊 Usage: ?") {
		t.Fatalf("preview should not show 📊 Usage: ? when quota data exists:\n%s", v)
	}
	usageIdx, sessIdx := -1, -1
	for i, ln := range lines {
		if usageIdx < 0 && strings.Contains(ln, "📊 Usage") {
			usageIdx = i
		}
		if sessIdx < 0 && strings.Contains(ln, "💬 Session") {
			sessIdx = i
		}
	}
	if usageIdx < 0 || sessIdx < 0 || usageIdx > sessIdx {
		t.Fatalf("usage rows should sit above the context line in the preview: usage@%d context@%d", usageIdx, sessIdx)
	}
}

func TestWorkingAgentShowsSpinner(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	// Emoji theme: the static working icon is ⚡️, which the spinner replaces.
	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, false)
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 120, 24

	frame := ansi.Strip(m.spinnerFrame())
	if frame == "" {
		t.Fatal("spinnerFrame() should return a frame")
	}
	lines := strings.Split(ansi.Strip(m.View()), "\n")
	var grok string
	for _, ln := range lines {
		if strings.Contains(ln, "Grok Build") {
			grok = ln
			break
		}
	}
	if grok == "" {
		t.Fatal("no working row rendered")
	}
	if !strings.Contains(grok, frame+" ") {
		t.Fatalf("working row should lead with the spinner frame %q, got %q", frame, grok)
	}
	if strings.Contains(grok, "⚡️") {
		t.Fatalf("working row should not show the static ⚡️ icon: %q", grok)
	}

	// Advancing the spinner (its own tick message) changes the frame.
	f0 := ansi.Strip(m.spinnerFrame())
	m = applyMsg(t, m, spinner.TickMsg{ID: m.spinner.ID()})
	if f1 := ansi.Strip(m.spinnerFrame()); f1 == f0 {
		t.Fatalf("spinner frame did not advance: %q", f1)
	}
}

func TestSessionPrefixFormat(t *testing.T) {
	m := Model{st: defaultStyles, theme: theme.Default}
	cases := []struct {
		name string
		u    agent.Usage
		want string
	}{
		{"empty", agent.Usage{}, "?"},
		{"quota only", agent.Usage{QuotaPct: 38, QuotaReset: "14:00"}, "?"},
		{"quota 0% used", agent.Usage{QuotaPct: 0, QuotaReset: "14:00"}, "?"},
		{"tokens only", agent.Usage{TokensUsed: 13025}, "13k"},
		{"tokens and window", agent.Usage{TokensUsed: 52367, WindowTokens: 200000}, "52.4k/200k"},
		{"million window", agent.Usage{TokensUsed: 123456, WindowTokens: 1000000}, "123k/1M"},
		{"tokens with quota", agent.Usage{TokensUsed: 52367, WindowTokens: 200000, QuotaPct: 38, QuotaReset: "14:00"}, "52.4k/200k"},
		{"warn threshold", agent.Usage{TokensUsed: 180000, WindowTokens: 200000}, "180k/200k"},
	}
	for _, tc := range cases {
		if got := ansi.Strip(sessionPrefix(m.st, tc.u)); got != tc.want {
			t.Errorf("%s: sessionPrefix(%+v) = %q, want %q", tc.name, tc.u, got, tc.want)
		}
	}
}

func TestSessionBarFormat(t *testing.T) {
	m := Model{st: defaultStyles, theme: theme.Default, contextWarn: defaultContextWarn}
	// A wide row caps the progress bar at its 30-cell maximum, making the
	// expected fill deterministic: round(width * pct / 100).
	cases := []struct {
		name string
		u    agent.Usage
		want string
	}{
		{"tokens and window", agent.Usage{TokensUsed: 52367, WindowTokens: 200000}, "████████░░░░░░░░░░░░░░░░░░░░░░ 26%"},
		{"million window", agent.Usage{TokensUsed: 123456, WindowTokens: 1000000}, "████░░░░░░░░░░░░░░░░░░░░░░░░░░ 12%"},
		{"tokens with quota", agent.Usage{TokensUsed: 52367, WindowTokens: 200000, QuotaPct: 38, QuotaReset: "14:00"}, "████████░░░░░░░░░░░░░░░░░░░░░░ 26%"},
		{"warn threshold", agent.Usage{TokensUsed: 180000, WindowTokens: 200000}, "███████████████████████████░░░ 90%"},
	}
	for _, tc := range cases {
		if got := ansi.Strip(sessionBar(m.st, m.contextWarn, tc.u, 200)); got != tc.want {
			t.Errorf("%s: sessionBar(%+v) = %q, want %q", tc.name, tc.u, got, tc.want)
		}
	}
}

func TestQuotaDetailFormat(t *testing.T) {
	m := Model{st: defaultStyles, theme: theme.Default, contextWarn: defaultContextWarn}
	// A wide row caps the bar at its 30-cell maximum; the fill is
	// deterministic: round(width * pct / 100). The reset text is built from
	// instants anchored to the current calendar day, so the same-day and
	// cross-day decisions stay stable on any date and in any time zone:
	// sameDay is always today, otherDay is always three days out.
	now := time.Now()
	year, month, day := now.Date()
	sameDay := time.Date(year, month, day, 19, 40, 0, 0, time.Local)
	otherDay := time.Date(year, month, day, 0, 59, 0, 0, time.Local).AddDate(0, 0, 3)
	cases := []struct {
		name string
		w    agent.QuotaWindow
		want string
	}{
		{"session 0%", agent.QuotaWindow{Pct: 0, Label: "Current session", ResetAt: sameDay.Format(time.RFC3339)},
			"░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░ 0% · Current session (reset at " + sameDay.Format("15:04 MST") + ")"},
		{"weekly 40%", agent.QuotaWindow{Pct: 40, Label: "Current week (all models)", ResetAt: otherDay.Format(time.RFC3339)},
			"████████████░░░░░░░░░░░░░░░░░░ 40% · Current week (all models) (reset at " + otherDay.Format("Jan 2, 15:04 MST") + ")"},
		{"scoped no reset", agent.QuotaWindow{Pct: 38, Label: "Current week (Fable)"},
			"███████████░░░░░░░░░░░░░░░░░░░ 38% · Current week (Fable)"},
		{"over 100 clamps", agent.QuotaWindow{Pct: 120, Label: "Current session"},
			"██████████████████████████████ 100% · Current session"},
		{"negative clamps", agent.QuotaWindow{Pct: -5, Label: "Current session"},
			"░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░ 0% · Current session"},
		// Dollar windows replace the bar entirely: a budget with a spent
		// amount shows consumed and remaining, an account balance shows the
		// remaining top-up, and a spend without a limit shows just the spend.
		{"budget and spend", agent.QuotaWindow{Limit: 100, Spend: 18.56, Label: "Extra usage (monthly)", Currency: "$"},
			"$18.56 used · $81.44 left · Extra usage (monthly)"},
		{"budget only", agent.QuotaWindow{Limit: 50, Label: "Extra usage (monthly)", Currency: "$"},
			"$50.00 budget · Extra usage (monthly)"},
		{"spend only", agent.QuotaWindow{Spend: 4.12, Currency: "$"},
			"$4.12 spent"},
		{"balance", agent.QuotaWindow{Balance: 65.85, Label: "DeepSeek balance", Currency: "$"},
			"$65.85 remaining · DeepSeek balance"},
		{"balance yen", agent.QuotaWindow{Balance: 110, Label: "DeepSeek balance", Currency: "¥"},
			"¥110.00 remaining · DeepSeek balance"},
		{"over budget clamps left", agent.QuotaWindow{Limit: 100, Spend: 120, Label: "Extra usage (monthly)", Currency: "$"},
			"$120.00 used · $0.00 left · Extra usage (monthly)"},
		{"default currency", agent.QuotaWindow{Balance: 9.5, Label: "DeepSeek balance"},
			"$9.50 remaining · DeepSeek balance"},
	}
	for _, tc := range cases {
		if got := ansi.Strip(quotaDetail(m.st, m.contextWarn, tc.w, 200)); got != tc.want {
			t.Errorf("%s: quotaDetail(%+v) = %q, want %q", tc.name, tc.w, got, tc.want)
		}
	}
}

func TestFormatResetAt(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.Local)
	same := time.Date(2026, 8, 6, 19, 40, 0, 0, time.Local)
	other := time.Date(2026, 8, 9, 0, 59, 0, 0, time.Local)
	if got, want := formatResetAt(same, now), "19:40 "+same.Format("MST"); got != want {
		t.Errorf("same-day reset = %q, want %q", got, want)
	}
	if got, want := formatResetAt(other, now), "Aug 9, 00:59 "+other.Format("MST"); got != want {
		t.Errorf("cross-day reset = %q, want %q", got, want)
	}
}

func TestUsageBarColor(t *testing.T) {
	m := Model{st: defaultStyles, theme: theme.Default, contextWarn: defaultContextWarn}
	green := styleHex(m.st.green)
	warn := styleHex(m.st.warn)
	if green == "" || warn == "" || green == warn {
		t.Fatalf("need distinct hex colors for the bar, green=%q warn=%q", green, warn)
	}
	if got := usageBarColor(26, m.contextWarn, m.st.green, m.st.warn); got != green {
		t.Fatalf("26%% bar color = %q, want green %q", got, green)
	}
	// At the warn threshold (85%) and above, the bar switches to warn.
	if got := usageBarColor(85, m.contextWarn, m.st.green, m.st.warn); got != warn {
		t.Fatalf("85%% bar color = %q, want warn %q", got, warn)
	}
	if got := usageBarColor(90, m.contextWarn, m.st.green, m.st.warn); got != warn {
		t.Fatalf("90%% bar color = %q, want warn %q", got, warn)
	}

	// A custom threshold (@tmon-context-warn) moves the warn switch point.
	m2 := m.WithContextWarn(70)
	if got := usageBarColor(75, m2.contextWarn, m2.st.green, m2.st.warn); got != warn {
		t.Fatalf("75%% bar with 70%% threshold = %q, want warn %q", got, warn)
	}
	// A threshold of 0 disables the warn color entirely.
	m3 := m.WithContextWarn(0)
	if got := usageBarColor(90, m3.contextWarn, m3.st.green, m3.st.warn); got != green {
		t.Fatalf("90%% bar with 0 threshold = %q, want green %q", got, green)
	}

	// Fill math: 26% of a 10-cell bar fills 3 cells (rounded); over 100%
	// clamps to a full bar.
	if got := ansi.Strip(usageBar(26, 10, m.contextWarn, m.st.green, m.st.warn, m.st.dim)); got != "███░░░░░░░" {
		t.Fatalf("26%% 10-cell bar = %q, want 3 filled cells", got)
	}
	if got := ansi.Strip(usageBar(250, 10, m.contextWarn, m.st.green, m.st.warn, m.st.dim)); got != "██████████" {
		t.Fatalf("250%% 10-cell bar = %q, want full bar", got)
	}
}

// TestUsageBarTrackBackground pins the progress-bar track to a solid dim
// background. The track used to render as dim glyphs on the selection
// background, which made the bar unreadable on the selected agent row; the
// empty cells must now carry the dim color as their own background, never
// the selection color.
func TestUsageBarTrackBackground(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })

	m := Model{st: defaultStyles, theme: theme.Default, contextWarn: defaultContextWarn}
	dim := styleHex(m.st.dim)
	selBg := string(m.st.selBgColor)
	if dim == "" || dim == selBg {
		t.Fatalf("need distinct dim and selection colors, dim=%q selBg=%q", dim, selBg)
	}

	// Reference SGRs: how lipgloss paints a solid dim background and the
	// selection background under the ANSI256 test profile.
	dimSGR := sgrOf(lipgloss.NewStyle().Background(lipgloss.Color(dim)).Render("░"))
	selSGR := sgrOf(lipgloss.NewStyle().Background(lipgloss.Color(selBg)).Render("░"))
	if dimSGR == "" || dimSGR == selSGR {
		t.Fatalf("need distinct painted track colors, dim=%q sel=%q", dimSGR, selSGR)
	}

	sel := styles{
		dim:   m.st.dim.Background(m.st.selBgColor),
		green: m.st.green.Background(m.st.selBgColor),
		warn:  m.st.warn.Background(m.st.selBgColor),
	}
	for name, st := range map[string]styles{"plain": defaultStyles, "selected": sel} {
		bar := usageBar(40, 10, m.contextWarn, st.green, st.warn, st.dim)
		got := trackSGR(bar)
		if got == "" {
			t.Fatalf("%s bar = %q, want empty track cells", name, bar)
		}
		if !strings.Contains(got, strings.TrimPrefix(dimSGR, "\x1b[")) {
			t.Fatalf("%s track SGR = %q, want the dim background (%q)", name, got, dimSGR)
		}
		if strings.Contains(got, strings.TrimPrefix(selSGR, "\x1b[")) {
			t.Fatalf("%s track SGR = %q, must not use the selection background (%q)", name, got, selSGR)
		}
	}
}

// sgrOf returns the leading SGR sequence of a rendered string ("\x1b[...m"),
// or "" when the string carries no SGR prefix.
func sgrOf(s string) string {
	if !strings.HasPrefix(s, "\x1b[") {
		return ""
	}
	if i := strings.IndexByte(s, 'm'); i >= 0 {
		return s[:i+1]
	}
	return ""
}

// trackSGR returns the SGR sequence styling the first empty (░) cell of a
// rendered usage bar, or "" when the bar has no empty cells.
func trackSGR(bar string) string {
	i := strings.Index(bar, "░")
	if i < 0 {
		return ""
	}
	j := strings.LastIndex(bar[:i], "\x1b[")
	if j < 0 {
		return ""
	}
	if k := strings.IndexByte(bar[j:], 'm'); k >= 0 {
		return bar[j : j+k+1]
	}
	return ""
}

func TestHeaderOmitsFleetCounts(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 80, 24

	raw := m.View()
	rawLines := strings.Split(ansi.Strip(raw), "\n")
	// Rows 1..mainHeaderHeight sit below the top border and carry the ascii
	// wordmark; each must match one row of the logo.
	for i, glyph := range asciiLogo {
		if !strings.Contains(rawLines[1+i], glyph) {
			t.Fatalf("header row %d missing wordmark %q:\n%s", i, glyph, raw)
		}
	}
	// Fleet status counts live on the status bar and per-agent rows, not
	// the popup title bar.
	for _, headerRow := range rawLines[1 : 1+mainHeaderHeight] {
		for _, bad := range []string{"B1", "W1", "I1"} {
			if strings.Contains(headerRow, bad) {
				t.Fatalf("header should not show fleet count %q: %q", bad, headerRow)
			}
		}
	}

	m3 := m.WithTheme(theme.Default)
	header3Lines := strings.Split(ansi.Strip(m3.View()), "\n")[1 : 1+mainHeaderHeight]
	for _, headerRow := range header3Lines {
		for _, bad := range []string{"🚨1", "⚡️1", "💤1"} {
			if strings.Contains(headerRow, bad) {
				t.Fatalf("emoji header should not show fleet count %q: %q", bad, headerRow)
			}
		}
	}
}

func TestProjectLineHasNoStatusAge(t *testing.T) {
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
	m.width, m.height = 120, 24

	v := ansi.Strip(m.View())
	lines := strings.Split(v, "\n")
	for i, ln := range lines {
		parts := strings.SplitN(ln, "│", 3)
		if len(parts) < 3 {
			continue // top/bottom border
		}
		list := parts[1]
		switch {
		case strings.Contains(list, "Claude Code"):
			if !strings.Contains(list, "🚨 Claude Code") {
				t.Fatalf("blocked name line = %q, want 🚨 before name", list)
			}
			if !strings.Contains(lines[i+1], "project: site") {
				t.Fatalf("blocked project line = %q, want project: site", lines[i+1])
			}
			if strings.Contains(lines[i+1], "now") || strings.Contains(lines[i+1], "1m") {
				t.Fatalf("project line should not show status age: %q", lines[i+1])
			}
		case strings.Contains(list, "Codex CLI"):
			if !strings.Contains(list, "💤 Codex CLI") {
				t.Fatalf("idle name line = %q, want 💤 before name", list)
			}
			if !strings.Contains(lines[i+1], "project: blog") {
				t.Fatalf("idle project line = %q, want project: blog", lines[i+1])
			}
			if strings.Contains(lines[i+1], "now") || strings.Contains(lines[i+1], "1m") {
				t.Fatalf("project line should not show status age: %q", lines[i+1])
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
