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
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/lipgloss"
	"github.com/guillaumemeyer/tmon/internal/agent"
)

// Palette is the set of colors a theme defines. Values are tmux-style color
// strings — names like "cyan", "colour208", or "#rrggbb" hex — which the
// status bar emits verbatim and the dashboard converts with Lipgloss.
type Palette struct {
	Background string // dashboard popup background (dark panel; unused by the status bar)
	App        string // app mark color
	Blocked    string
	Working    string
	Idle       string
	Dim        string // dimmed secondary text
	Accent     string // bold title/name text
	Warn       string // usage/quota warnings
	SelBg      string // selection highlight background
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

// SpinnerFrame returns the current frame of the working-agent spinner — the
// same bubbles Line spinner the dashboard animates — advanced by wall-clock
// time. tmux re-renders `tmon status` every status-interval, so each status
// refresh shows the next frame of the spinner. Tests pin it for
// deterministic output.
var SpinnerFrame = func() string {
	frames := spinner.Line.Frames
	return frames[time.Now().Unix()%int64(len(frames))]
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
		Background: "colour235",
		App:        "cyan",
		Blocked:    "colour208",
		Working:    "green",
		Idle:       "blue",
		Dim:        "colour240",
		Accent:     "colour15",
		Warn:       "yellow",
		SelBg:      "colour236",
	},
	Icons: emojiIcons,
}

// presets maps theme names to their palettes.
var presets = map[string]Theme{
	"default": Default,
	"catppuccin": {
		Name: "catppuccin",
		Palette: Palette{
			Background: "#1e1e2e", App: "#cba6f7", Blocked: "#f38ba8", Working: "#a6e3a1", Idle: "#89b4fa",
			Dim: "#6c7086", Accent: "#cdd6f4", Warn: "#f9e2af", SelBg: "#313244",
		},
	},
	"nord": {
		Name: "nord",
		Palette: Palette{
			Background: "#2e3440", App: "#88c0d0", Blocked: "#bf616a", Working: "#a3be8c", Idle: "#81a1c1",
			Dim: "#4c566a", Accent: "#eceff4", Warn: "#ebcb8b", SelBg: "#3b4252",
		},
	},
	"dracula": {
		Name: "dracula",
		Palette: Palette{
			Background: "#282a36", App: "#bd93f9", Blocked: "#ff5555", Working: "#50fa7b", Idle: "#8be9fd",
			Dim: "#6272a4", Accent: "#f8f8f2", Warn: "#f1fa8c", SelBg: "#44475a",
		},
	},
	"tokyonight": {
		Name: "tokyonight",
		Palette: Palette{
			Background: "#1a1b26", App: "#7aa2f7", Blocked: "#f7768e", Working: "#9ece6a", Idle: "#7dcfff",
			Dim: "#565f89", Accent: "#c0caf5", Warn: "#e0af68", SelBg: "#292e42",
		},
	},
	"gruvbox": {
		Name: "gruvbox",
		Palette: Palette{
			Background: "#282828", App: "#83a598", Blocked: "#fb4934", Working: "#b8bb26", Idle: "#83a598",
			Dim: "#928374", Accent: "#ebdbb2", Warn: "#fabd2f", SelBg: "#3c3836",
		},
	},
	"solarized": {
		Name: "solarized",
		Palette: Palette{
			Background: "#002b36", App: "#268bd2", Blocked: "#dc322f", Working: "#859900", Idle: "#2aa198",
			Dim: "#586e75", Accent: "#93a1a1", Warn: "#b58900", SelBg: "#073642",
		},
	},
	"onedark": {
		Name: "onedark",
		Palette: Palette{
			Background: "#282c34", App: "#61afef", Blocked: "#e06c75", Working: "#98c379", Idle: "#56b6c2",
			Dim: "#5c6370", Accent: "#abb2bf", Warn: "#e5c07b", SelBg: "#2c323c",
		},
	},
}

var (
	emojiIcons = Icons{App: "🤖", Blocked: "🚨", Working: "⚡️", Idle: "💤", Warn: "⚠️"}
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
	if v, ok := ov["bg"]; ok {
		p.Background = v
	}
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

// SwatchLines renders the palette preview rows shared by `tmon theme
// preview` and the in-popup theme selector: one row per palette slot with a
// real color chip, the slot name, and the raw tmux-style value. Each row is
// a single display line, already styled. The background slot renders as a
// foreground block so the chip stays visible against any panel background.
func SwatchLines(t Theme) []string {
	swatches := []struct{ label, value string }{
		{"bg", t.Palette.Background},
		{"app", t.Palette.App},
		{"blocked", t.Palette.Blocked},
		{"working", t.Palette.Working},
		{"idle", t.Palette.Idle},
		{"dim", t.Palette.Dim},
		{"accent", t.Palette.Accent},
		{"warn", t.Palette.Warn},
		{"selbg", t.Palette.SelBg},
	}
	out := make([]string, 0, len(swatches))
	for _, s := range swatches {
		var chip string
		if s.label == "bg" {
			chip = lipgloss.NewStyle().Foreground(lipgloss.Color(Lipgloss(s.value))).Render("▮▮")
		} else {
			chip = lipgloss.NewStyle().Background(lipgloss.Color(Lipgloss(s.value))).Render("  ")
		}
		out = append(out, fmt.Sprintf("  %s  %-8s %s", chip, s.label, s.value))
	}
	return out
}

// SampleLine renders one status-bar sample line (e.g. "🤖-🚨1-|2-💤1") in
// the theme's actual colors, so theme previews show real terminal colors
// rather than raw tmux directives. The icon set is passed separately — it
// may come from a differently-resolved theme (ASCII vs emoji). Working
// agents render as the animated spinner frame instead of an icon, matching
// the status bar and the dashboard.
func SampleLine(t Theme, ic Icons) string {
	col := func(c string) lipgloss.Style {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(Lipgloss(c)))
	}
	app := col(t.Palette.App).Render(ic.App)

	var segs []string
	add := func(glyph, color string, n int) {
		if n <= 0 {
			return
		}
		segs = append(segs, col(color).Render(glyph+strconv.Itoa(n)))
	}
	add(ic.Blocked, t.Palette.Blocked, 1)
	add(SpinnerFrame(), t.Palette.Working, 2)
	add(ic.Idle, t.Palette.Idle, 1)

	line := app
	if len(segs) > 0 {
		line += "-" + strings.Join(segs, "-")
	}
	return line
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

