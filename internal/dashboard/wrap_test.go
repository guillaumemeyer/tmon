package dashboard

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestWrapPreviewLinePassesThrough(t *testing.T) {
	// Lines that fit, tiny widths, and empty input come back unchanged.
	cases := []struct {
		in    string
		width int
	}{
		{"", 4},
		{"abc", 4},
		{"abcd", 4},
		{"abc", 0},
		{"abc", -1},
		{"\x1b[31mabc\x1b[0m", 40},
	}
	for _, c := range cases {
		if got := wrapPreviewLine(c.in, c.width); got != c.in {
			t.Errorf("wrapPreviewLine(%q, %d) = %q, want unchanged", c.in, c.width, got)
		}
	}
}

func TestWrapPreviewLineHardBreaks(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		width int
		want  string
	}{
		{"ascii", "abcdefghij", 4, "abcd\nefgh\nij"},
		{"word wider than width", "abcdefghij", 3, "abc\ndef\nghi\nj"},
		{"width one", "ab", 1, "a\nb"},
		{"leading spaces kept", "ab  cd", 3, "ab \n cd"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := wrapPreviewLine(c.in, c.width); got != c.want {
				t.Fatalf("wrapPreviewLine(%q, %d) = %q, want %q", c.in, c.width, got, c.want)
			}
		})
	}
}

func TestWrapPreviewLineWideChars(t *testing.T) {
	// Each CJK char is two cells; the wrap never splits a character.
	if got := wrapPreviewLine("日本語", 4); got != "日本\n語" {
		t.Fatalf("wrapPreviewLine(日本語, 4) = %q, want %q", got, "日本\n語")
	}
	if got := wrapPreviewLine("日本語", 6); got != "日本語" {
		t.Fatalf("wrapPreviewLine(日本語, 6) = %q, want unchanged", got)
	}
}

func TestWrapPreviewLineKeepsGraphemes(t *testing.T) {
	// e + combining acute is one grapheme (width 1) and must not split.
	in := "e\u0301x"
	if got := wrapPreviewLine(in, 1); got != "e\u0301\nx" {
		t.Fatalf("wrapPreviewLine(%q, 1) = %q, want %q", in, got, "e\u0301\nx")
	}
}

func TestWrapPreviewLineReassertsSGR(t *testing.T) {
	// The break inside a red run re-asserts the color on the continuation.
	in := "\x1b[31mabcdef\x1b[0mghij"
	got := wrapPreviewLine(in, 4)
	want := "\x1b[31mabcd\n\x1b[31mef\x1b[0mgh\nij"
	if got != want {
		t.Fatalf("wrapPreviewLine(%q, 4) = %q, want %q", in, got, want)
	}
	if stripped := ansi.Strip(got); stripped != "abcd\nefgh\nij" {
		t.Fatalf("strip = %q, want the full text back", stripped)
	}
}

func TestWrapPreviewLineNoPrefixAfterReset(t *testing.T) {
	// A reset before the break leaves the continuation at the default style.
	in := "\x1b[31mabc\x1b[0mdefghij"
	got := wrapPreviewLine(in, 4)
	want := "\x1b[31mabc\x1b[0md\nefgh\nij"
	if got != want {
		t.Fatalf("wrapPreviewLine(%q, 4) = %q, want %q", in, got, want)
	}
}

func TestWrapPreviewLineCombinedStylePrefix(t *testing.T) {
	// Bold + red compose into one normalized SGR on the continuation.
	in := "\x1b[1m\x1b[31mabcdef"
	got := wrapPreviewLine(in, 4)
	if !strings.Contains(got, "\n\x1b[1;31m") {
		t.Fatalf("continuation missing composed prefix in %q", got)
	}
}

func TestWrapPreviewLineExtendedColors(t *testing.T) {
	// 256-color and truecolor foregrounds survive a break too.
	in := "\x1b[38;5;208mabcdef"
	got := wrapPreviewLine(in, 4)
	if !strings.Contains(got, "\n\x1b[38;5;208m") {
		t.Fatalf("continuation missing extended color prefix in %q", got)
	}
	in2 := "\x1b[38;2;255;85;85mabcdef"
	got2 := wrapPreviewLine(in2, 4)
	if !strings.Contains(got2, "\n\x1b[38;2;255;85;85m") {
		t.Fatalf("continuation missing truecolor prefix in %q", got2)
	}
}

func TestWrapPreviewLineSegmentsFit(t *testing.T) {
	in := "the quick brown fox jumps over the lazy dog 日本語テキスト"
	for _, width := range []int{1, 2, 3, 5, 8, 13, 21} {
		got := wrapPreviewLine(in, width)
		// Every segment must preserve the text, no matter the width.
		if joined := ansi.Strip(strings.ReplaceAll(got, "\n", "")); joined != in {
			t.Fatalf("width %d: wrap lost or added content: %q != %q", width, joined, in)
		}
		// A single wide CJK character (2 cells) cannot fit in one column,
		// so the per-segment fit check only applies from width 2 up.
		if width < 2 {
			continue
		}
		for _, seg := range strings.Split(got, "\n") {
			if w := ansi.StringWidth(seg); w > width {
				t.Fatalf("width %d: segment %q is %d cells", width, seg, w)
			}
		}
	}
}

// markerRows counts preview-column rows whose visible content is entirely
// the marker rune (a truncated or wrapped capture row), ignoring padding and
// ANSI. The framed view is │list│preview│ per row, so only the preview
// segment is inspected — list text on the same row must not hide a capture
// row. The top/bottom border rows (╭─╮/╰─╯) have no preview column and are
// skipped.
func markerRows(t *testing.T, v, marker string) int {
	t.Helper()
	n := 0
	for _, line := range strings.Split(ansi.Strip(v), "\n") {
		cols := strings.Split(line, "│")
		if len(cols) < 3 {
			continue
		}
		s := strings.TrimSpace(cols[2])
		if s != "" && strings.Trim(s, marker) == "" {
			n++
		}
	}
	return n
}

func TestPreviewFitWidthToggleAndRender(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return strings.Repeat("x", 200) + "\n" }
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 100, 24

	// Default (truncation): the long capture is cut to one row at the pane edge.
	if n := markerRows(t, m.View().Content, "x"); n != 1 {
		t.Fatalf("truncation mode shows %d x-rows, want 1 (cut line)", n)
	}

	// f enables fit-to-width; the capture wraps across several rows.
	m = applyMsg(t, m, key('f'))
	if !m.previewWrap {
		t.Fatal("f should enable fit-to-width")
	}
	if n := markerRows(t, m.View().Content, "x"); n <= 1 {
		t.Fatalf("fit mode shows %d x-rows, want a wrapped multi-row capture", n)
	}

	// f again restores truncation: the raw line is cut again.
	m = applyMsg(t, m, key('f'))
	if m.previewWrap {
		t.Fatal("f should disable fit-to-width")
	}
	if n := markerRows(t, m.View().Content, "x"); n != 1 {
		t.Fatalf("after toggle off shows %d x-rows, want 1", n)
	}
}

func TestPreviewFitWidthRewrapsOnResize(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return strings.Repeat("y", 300) + "\n" }
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 100, 24
	m = applyMsg(t, m, key('f'))

	// Fit mode wraps the capture across several rows of the preview.
	rows0 := markerRows(t, m.View().Content, "y")
	if rows0 <= 1 {
		t.Fatalf("fit mode should wrap the capture across rows, got %d y-rows", rows0)
	}

	// Widen the preview (h moves the divider left); the next render
	// re-wraps at the wider panel, so fewer rows are needed.
	m = applyMsg(t, m, key('h'))
	if rows1 := markerRows(t, m.View().Content, "y"); rows1 >= rows0 {
		t.Fatalf("wider preview wrapped into %d rows, want < %d", rows1, rows0)
	}
}

func TestPreviewFitWidthPersists(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/dashboard.json"

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true).WithSettingsPath(path)
	m = applyMsg(t, m, initMsg{})
	m = applyMsg(t, m, key('f'))
	if !m.previewWrap {
		t.Fatal("f should enable fit-to-width")
	}

	// A new model with the same settings path reloads the saved choice.
	m2 := New(f.load, true).WithSettingsPath(path)
	if !m2.previewWrap {
		t.Fatal("fit-to-width should reload from settings")
	}
}

func TestPreviewFitWidthTip(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	// A wide popup so the preview column keeps all three hints (scroll,
	// resize, fit) on the tips row.
	m.width, m.height = 160, 24

	if v := ansi.Strip(m.View().Content); !strings.Contains(v, "[f] fit") {
		t.Fatalf("expected the fit hint in the preview tips:\n%s", v)
	}
	m = applyMsg(t, m, key('f'))
	if v := ansi.Strip(m.View().Content); !strings.Contains(v, "[f] fit (on)") {
		t.Fatalf("expected the fit-on hint in the preview tips:\n%s", v)
	}
}
