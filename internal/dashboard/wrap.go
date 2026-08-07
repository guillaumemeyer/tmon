package dashboard

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"
)

// wrapPreviewLine wraps one captured line to at most width display cells,
// breaking mid-word like a terminal. ANSI escape sequences pass through
// unchanged and do not count toward the width. When a break splits a styled
// run, the continuation segment re-asserts the SGR state that was active at
// the break, so colors survive the wrap. Grapheme clusters (combining marks,
// wide CJK characters, emoji) are never split. Lines that already fit are
// returned unchanged.
func wrapPreviewLine(s string, width int) string {
	if width < 1 {
		return s
	}
	if ansi.StringWidth(s) <= width {
		return s
	}
	atoms := wrapAtoms(s)
	var out strings.Builder
	out.Grow(len(s) + 8)
	var st sgrState
	lineW := 0
	for _, a := range atoms {
		if a.isSGR {
			st.apply(a.text)
			out.WriteString(a.text)
			continue
		}
		if a.width == 0 {
			out.WriteString(a.text)
			continue
		}
		if lineW+a.width > width {
			out.WriteByte('\n')
			if p := st.prefix(); p != "" {
				out.WriteString(p)
			}
			lineW = 0
		}
		out.WriteString(a.text)
		lineW += a.width
	}
	return out.String()
}

// wrapPreviewLines applies wrapPreviewLine to every captured line.
func wrapPreviewLines(lines []string, width int) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, wrapPreviewLine(l, width))
	}
	return out
}

// wrapAtom is one indivisible unit of a captured line: an escape sequence
// (zero width; SGR sequences also update the tracked style) or a grapheme
// cluster with its display width.
type wrapAtom struct {
	text  string
	width int
	isSGR bool
}

// wrapAtoms tokenizes a line into grapheme clusters and escape sequences.
// Clusters are kept whole so the wrap never splits a character or its
// combining marks; CSI sequences are copied through and count zero cells.
func wrapAtoms(s string) []wrapAtom {
	atoms := make([]wrapAtom, 0, 16)
	for i := 0; i < len(s); {
		if s[i] != 0x1b {
			cluster, _, width, _ := uniseg.FirstGraphemeClusterInString(s[i:], -1)
			atoms = append(atoms, wrapAtom{text: cluster, width: width})
			i += len(cluster)
			continue
		}
		// A CSI sequence: ESC [ parameter bytes, intermediate bytes, final.
		if i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && s[j] >= 0x20 && s[j] <= 0x3f {
				j++
			}
			if j < len(s) && s[j] >= 0x40 && s[j] <= 0x7e {
				atoms = append(atoms, wrapAtom{text: s[i : j+1], isSGR: s[j] == 'm'})
				i = j + 1
				continue
			}
		}
		// Lone ESC or an unrecognized sequence: copy the byte through.
		atoms = append(atoms, wrapAtom{text: s[i : i+1]})
		i++
	}
	return atoms
}

// sgrState tracks the active SGR attributes of a captured line so a wrap
// break can re-assert the style on the continuation segment. Attributes are
// stored as their numeric codes (bold=1, red fg=31, ...) plus the raw
// parameter strings for extended colors, mirroring how SGR parameters
// compose cumulatively.
type sgrState struct {
	attrs []int  // active attribute codes, e.g. 1 for bold, 7 for reverse
	fg    string // foreground parameter: "" (default), "31", "38;5;208", ...
	bg    string // background parameter: "" (default), "44", "48;2;r;g;b", ...
	ul    string // underline color parameter: "" (default) or "58;..."
}

// sgrClear maps a "no-*" attribute code to the attribute codes it clears:
// 22 clears 1 (bold) and 2 (faint), 23 clears 3 (italic), etc.
var sgrClear = map[int][]int{
	22: {1, 2},
	23: {3},
	24: {4},
	25: {5, 6},
	27: {7},
	28: {8},
	29: {9},
}

// apply folds one SGR sequence ("\x1b[31;1m") into the tracked state.
func (s *sgrState) apply(esc string) {
	body := esc[2 : len(esc)-1] // drop ESC [ and the trailing m
	fields := strings.Split(body, ";")
	for i := 0; i < len(fields); i++ {
		code, err := strconv.Atoi(fields[i])
		if err != nil {
			continue
		}
		switch {
		case code == 0:
			s.attrs = nil
			s.fg, s.bg, s.ul = "", "", ""
		case code >= 1 && code <= 9:
			s.addAttr(code)
		case code == 22 || code == 23 || code == 24 || code == 25 ||
			code == 27 || code == 28 || code == 29:
			for _, c := range sgrClear[code] {
				s.delAttr(c)
			}
		case code >= 30 && code <= 37 || code >= 90 && code <= 97:
			s.fg = fields[i]
		case code == 39:
			s.fg = ""
		case code >= 40 && code <= 47 || code >= 100 && code <= 107:
			s.bg = fields[i]
		case code == 49:
			s.bg = ""
		case code == 59:
			s.ul = ""
		case code == 38 || code == 48 || code == 58:
			// Extended color: 38;5;N (indexed) or 38;2;r;g;b (truecolor).
			if n := colorSpecLen(fields, i); n > 0 {
				spec := strings.Join(fields[i:i+1+n], ";")
				switch code {
				case 38:
					s.fg = spec
				case 48:
					s.bg = spec
				default:
					s.ul = spec
				}
				i += n
			}
		}
	}
}

func (s *sgrState) addAttr(code int) {
	for _, a := range s.attrs {
		if a == code {
			return
		}
	}
	s.attrs = append(s.attrs, code)
}

func (s *sgrState) delAttr(code int) {
	for i, a := range s.attrs {
		if a == code {
			s.attrs = append(s.attrs[:i], s.attrs[i+1:]...)
			return
		}
	}
}

// prefix serializes the tracked state as the SGR sequence that re-applies
// it, or "" when the state is the terminal default.
func (s sgrState) prefix() string {
	parts := make([]string, 0, 1+len(s.attrs))
	for _, a := range s.attrs {
		parts = append(parts, strconv.Itoa(a))
	}
	if s.fg != "" {
		parts = append(parts, s.fg)
	}
	if s.bg != "" {
		parts = append(parts, s.bg)
	}
	if s.ul != "" {
		parts = append(parts, s.ul)
	}
	if len(parts) == 0 {
		return ""
	}
	return "\x1b[" + strings.Join(parts, ";") + "m"
}

// applyPreviewWrap keeps the preview viewport content consistent with the
// fit-to-width toggle and the current panel width. In wrap mode the raw
// capture is hard-wrapped to w cells (see wrapPreviewLine); in truncation
// mode the raw capture is restored. Content is only rewritten when the mode
// or the width changed since it was last set, so the viewport scroll
// position survives renders.
func (m *Model) applyPreviewWrap(w int) {
	if m.previewWrap && w > 1 {
		if m.previewWrapWidth == w {
			return
		}
		m.previewWrapWidth = w
		lines := wrapPreviewLines(strings.Split(m.previewText, "\n"), w)
		m.preview.SetContent(strings.Join(trimEmptyEdges(lines), "\n"))
		return
	}
	if m.previewWrapWidth == 0 {
		return
	}
	m.previewWrapWidth = 0
	m.preview.SetContent(strings.Join(trimEmptyEdges(strings.Split(m.previewText, "\n")), "\n"))
}
