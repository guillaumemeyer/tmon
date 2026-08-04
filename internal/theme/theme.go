// Package theme resolves tmon's visual themes: a named preset (default,
// catppuccin, nord, …) plus per-color and per-icon overrides from the
// @tmon-color-* / @tmon-icon-* tmux options. A resolved Theme carries the
// palette and glyphs used by both the status bar and the dashboard, so the
// two always agree.
package theme

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/guillaumemeyer/tmon/internal/agent"
)

// Palette is the set of colors a theme defines. Values are tmux-style color
// strings — names like "cyan", "colour208", or "#rrggbb" hex — which the
// status bar emits verbatim and the dashboard converts with Lipgloss.
type Palette struct {
	App     string // app mark color
	Blocked string
	Working string
	Idle    string
	Dim     string // dimmed secondary text
	Accent  string // bold title/name text
	Warn    string // usage/quota warnings
	SelBg   string // selection highlight background
}

// Icons holds the glyphs for the app mark, the three statuses, and the
// context/usage warning flag.
type Icons struct {
	App     string
	Blocked string
	Working string
	Idle    string
	Warn    string // context-window warning marker (⚠️ / !)
}

// ForStatus returns the icon for an agent status — blocked, working, or
// idle — falling back to the app mark for anything else.
func (i Icons) ForStatus(s agent.Status) string {
	switch s {
	case agent.StatusBlocked:
		return i.Blocked
	case agent.StatusWorking:
		return i.Working
	case agent.StatusIdle:
		return i.Idle
	}
	return i.App
}

// Theme is a fully resolved theme: name, palette, and icons.
type Theme struct {
	Name    string
	Palette Palette
	Icons   Icons
}

// Default is the built-in theme, matching tmon's classic colors exactly.
var Default = Theme{
	Name: "default",
	Palette: Palette{
		App:     "cyan",
		Blocked: "colour208",
		Working: "green",
		Idle:    "blue",
		Dim:     "colour240",
		Accent:  "colour15",
		Warn:    "yellow",
		SelBg:   "colour236",
	},
	Icons: emojiIcons,
}

// presets maps theme names to their palettes.
var presets = map[string]Theme{
	"default": Default,
	"catppuccin": {
		Name: "catppuccin",
		Palette: Palette{
			App: "#cba6f7", Blocked: "#f38ba8", Working: "#a6e3a1", Idle: "#89b4fa",
			Dim: "#6c7086", Accent: "#cdd6f4", Warn: "#f9e2af", SelBg: "#313244",
		},
	},
	"nord": {
		Name: "nord",
		Palette: Palette{
			App: "#88c0d0", Blocked: "#bf616a", Working: "#a3be8c", Idle: "#81a1c1",
			Dim: "#4c566a", Accent: "#eceff4", Warn: "#ebcb8b", SelBg: "#3b4252",
		},
	},
	"dracula": {
		Name: "dracula",
		Palette: Palette{
			App: "#bd93f9", Blocked: "#ff5555", Working: "#50fa7b", Idle: "#8be9fd",
			Dim: "#6272a4", Accent: "#f8f8f2", Warn: "#f1fa8c", SelBg: "#44475a",
		},
	},
	"tokyonight": {
		Name: "tokyonight",
		Palette: Palette{
			App: "#7aa2f7", Blocked: "#f7768e", Working: "#9ece6a", Idle: "#7dcfff",
			Dim: "#565f89", Accent: "#c0caf5", Warn: "#e0af68", SelBg: "#292e42",
		},
	},
	"gruvbox": {
		Name: "gruvbox",
		Palette: Palette{
			App: "#83a598", Blocked: "#fb4934", Working: "#b8bb26", Idle: "#83a598",
			Dim: "#928374", Accent: "#ebdbb2", Warn: "#fabd2f", SelBg: "#3c3836",
		},
	},
	"solarized": {
		Name: "solarized",
		Palette: Palette{
			App: "#268bd2", Blocked: "#dc322f", Working: "#859900", Idle: "#2aa198",
			Dim: "#586e75", Accent: "#93a1a1", Warn: "#b58900", SelBg: "#073642",
		},
	},
	"onedark": {
		Name: "onedark",
		Palette: Palette{
			App: "#61afef", Blocked: "#e06c75", Working: "#98c379", Idle: "#56b6c2",
			Dim: "#5c6370", Accent: "#abb2bf", Warn: "#e5c07b", SelBg: "#2c323c",
		},
	},
}

var (
	emojiIcons = Icons{App: "🤖", Blocked: "🛑", Working: "⚡️", Idle: "💤", Warn: "⚠️"}
	asciiIcons = Icons{App: "[@]", Blocked: "B", Working: "W", Idle: "I", Warn: "!"}
)

// Options collects the inputs that affect theme resolution.
type Options struct {
	Name           string            // preset name; unknown names fall back to default
	ColorOverrides map[string]string // lower-case slot → color ("blocked", "selbg", …)
	IconOverrides  map[string]string // lower-case slot → glyph ("app", "working", …)
	ASCII          bool              // use ASCII glyphs (B/W/I) instead of emoji
}

// Resolve picks the named preset and applies overrides: icons first (ASCII
// base, then @tmon-icon-*), then colors (@tmon-color-*). Unknown preset
// names silently fall back to the default theme.
func Resolve(o Options) Theme {
	t, ok := presets[o.Name]
	if !ok {
		t = Default
	}
	t.Icons = emojiIcons
	if o.ASCII {
		t.Icons = asciiIcons
	}
	applyIcons(&t.Icons, o.IconOverrides)
	applyColors(&t.Palette, o.ColorOverrides)
	return t
}

func applyIcons(ic *Icons, ov map[string]string) {
	if v, ok := ov["app"]; ok {
		ic.App = v
	}
	if v, ok := ov["blocked"]; ok {
		ic.Blocked = v
	}
	if v, ok := ov["working"]; ok {
		ic.Working = v
	}
	if v, ok := ov["idle"]; ok {
		ic.Idle = v
	}
	if v, ok := ov["warn"]; ok {
		ic.Warn = v
	}
}

func applyColors(p *Palette, ov map[string]string) {
	if v, ok := ov["app"]; ok {
		p.App = v
	}
	if v, ok := ov["blocked"]; ok {
		p.Blocked = v
	}
	if v, ok := ov["working"]; ok {
		p.Working = v
	}
	if v, ok := ov["idle"]; ok {
		p.Idle = v
	}
	if v, ok := ov["dim"]; ok {
		p.Dim = v
	}
	if v, ok := ov["accent"]; ok {
		p.Accent = v
	}
	if v, ok := ov["warn"]; ok {
		p.Warn = v
	}
	if v, ok := ov["selbg"]; ok {
		p.SelBg = v
	}
}

// Names returns the preset names, sorted.
func Names() []string {
	out := make([]string, 0, len(presets))
	for n := range presets {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Lipgloss converts a tmux-style color string to the form lipgloss expects:
// "colourNNN" becomes the bare NNN; everything else (names, hex) passes
// through unchanged.
func Lipgloss(c string) string {
	rest, ok := strings.CutPrefix(c, "colour")
	if ok && rest != "" {
		digits := true
		for _, r := range rest {
			if r < '0' || r > '9' {
				digits = false
				break
			}
		}
		if digits {
			return rest
		}
	}
	return c
}

// Tint darkens a tmux color string for use as a subtle pane background,
// keeping the palette's hue but dropping it to ~tintFactor luminance so
// default-foreground text stays readable over it. It understands "#rrggbb"
// (and 3-digit "#rgb") hex, "colourNNN" xterm-256 indices, and the standard
// ANSI color names. Anything unrecognized passes through unchanged, so a
// user color override that isn't a color still degrades gracefully.
func Tint(c string) string {
	hex, ok := toHex(c)
	if !ok {
		return c
	}
	r, g, b := hexRGB(hex)
	return fmt.Sprintf("#%02x%02x%02x",
		int(float64(r)*tintFactor), int(float64(g)*tintFactor), int(float64(b)*tintFactor))
}

// tintFactor scales each channel toward black; ~35% keeps the tint visible
// but clearly subordinate to the pane's own text.
const tintFactor = 0.35

// ansiColors maps the 16 base color names (and common aliases) to their
// xterm-256 indices, so Tint can resolve "green" and "brightred" like tmux
// does before darkening.
var ansiColors = map[string]int{
	"black": 0, "red": 1, "green": 2, "yellow": 3,
	"blue": 4, "magenta": 5, "cyan": 6, "white": 7,
	"brightblack": 8, "brightred": 9, "brightgreen": 10, "brightyellow": 11,
	"brightblue": 12, "brightmagenta": 13, "brightcyan": 14, "brightwhite": 15,
	"grey": 8, "gray": 8,
}

// toHex resolves a tmux color string to "#rrggbb". The second return is
// false when the string isn't a color Tint understands.
func toHex(c string) (string, bool) {
	if len(c) == 7 && c[0] == '#' && isHex(c[1:]) {
		return c, true
	}
	// tmux also accepts 3-digit shorthand: "#rgb" doubles each digit.
	if len(c) == 4 && c[0] == '#' && isHex(c[1:]) {
		return "#" + string([]byte{c[1], c[1], c[2], c[2], c[3], c[3]}), true
	}
	if rest, ok := strings.CutPrefix(c, "colour"); ok && rest != "" && allDigits(rest) {
		if n, err := strconv.Atoi(rest); err == nil && n >= 0 && n <= 255 {
			return xterm256Hex(n), true
		}
	}
	if n, ok := ansiColors[strings.ToLower(c)]; ok {
		return xterm256Hex(n), true
	}
	return "", false
}

func isHex(s string) bool {
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// xterm256Hex converts an xterm-256 palette index to "#rrggbb": the 16
// system colors, the 6×6×6 color cube, and the 24-step gray ramp.
func xterm256Hex(n int) string {
	var r, g, b int
	switch {
	case n < 16:
		r, g, b = systemRGB(n)
	case n <= 231:
		v := n - 16
		r, g, b = cube[v/36], cube[(v%36)/6], cube[v%6]
	default:
		gray := 8 + (n-232)*10
		r, g, b = gray, gray, gray
	}
	return fmt.Sprintf("#%02x%02x%02x", r, g, b)
}

// cube is the 6×6×6 color-cube channel values (0, 95, 135, 175, 215, 255).
var cube = [6]int{0, 95, 135, 175, 215, 255}

// systemRGB maps xterm indices 0–15 to their canonical RGB values.
func systemRGB(n int) (r, g, b int) {
	// Standard xterm system colors: base 8 + bright 8.
	system := [16][3]int{
		{0, 0, 0}, {128, 0, 0}, {0, 128, 0}, {128, 128, 0},
		{0, 0, 128}, {128, 0, 128}, {0, 128, 128}, {192, 192, 192},
		{128, 128, 128}, {255, 0, 0}, {0, 255, 0}, {255, 255, 0},
		{0, 0, 255}, {255, 0, 255}, {0, 255, 255}, {255, 255, 255},
	}
	return system[n][0], system[n][1], system[n][2]
}

// hexRGB parses a validated "#rrggbb" string into its channels.
func hexRGB(h string) (r, g, b int) {
	r = hexPair(h[1:3])
	g = hexPair(h[3:5])
	b = hexPair(h[5:7])
	return
}

func hexPair(s string) int {
	v := 0
	for _, c := range s {
		v *= 16
		switch {
		case c >= '0' && c <= '9':
			v += int(c - '0')
		case c >= 'a' && c <= 'f':
			v += int(c-'a') + 10
		default:
			v += int(c-'A') + 10
		}
	}
	return v
}
