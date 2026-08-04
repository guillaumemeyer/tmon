package theme

import (
	"reflect"
	"testing"

	"github.com/guillaumemeyer/tmon/internal/agent"
)

func TestResolveDefault(t *testing.T) {
	tm := Resolve(Options{})
	if tm.Name != "default" {
		t.Fatalf("Name = %q, want default", tm.Name)
	}
	if tm.Palette.App != "cyan" || tm.Palette.Blocked != "colour208" {
		t.Fatalf("default palette changed: %+v", tm.Palette)
	}
	// Emoji icons by default.
	if tm.Icons.App != "🤖" || tm.Icons.Working != "⚡️" || tm.Icons.Idle != "💤" || tm.Icons.Blocked != "🛑" || tm.Icons.Warn != "⚠️" {
		t.Fatalf("default icons = %+v, want emoji", tm.Icons)
	}
}

func TestResolveUnknownFallsBackToDefault(t *testing.T) {
	tm := Resolve(Options{Name: "definitely-not-a-theme"})
	if tm.Name != "default" {
		t.Fatalf("Name = %q, want default fallback", tm.Name)
	}
	if tm.Palette.App != "cyan" {
		t.Fatalf("palette = %+v, want default palette", tm.Palette)
	}
}

func TestResolvePreset(t *testing.T) {
	tm := Resolve(Options{Name: "nord"})
	if tm.Name != "nord" {
		t.Fatalf("Name = %q, want nord", tm.Name)
	}
	if tm.Palette.App != "#88c0d0" || tm.Palette.Blocked != "#bf616a" {
		t.Fatalf("nord palette = %+v", tm.Palette)
	}
	// Presets do not leak into each other.
	if Resolve(Options{Name: "dracula"}).Palette.App != "#bd93f9" {
		t.Fatalf("dracula palette wrong: %+v", Resolve(Options{Name: "dracula"}).Palette)
	}
}

func TestResolveASCII(t *testing.T) {
	tm := Resolve(Options{ASCII: true})
	if tm.Icons.App != "[@]" || tm.Icons.Blocked != "B" || tm.Icons.Working != "W" || tm.Icons.Idle != "I" || tm.Icons.Warn != "!" {
		t.Fatalf("ascii icons = %+v, want [@] B W I !", tm.Icons)
	}
	// ASCII only affects icons, not the palette.
	if tm.Palette.App != "cyan" {
		t.Fatalf("ascii changed the palette: %+v", tm.Palette)
	}
}

func TestResolveOverrides(t *testing.T) {
	tm := Resolve(Options{
		Name:           "nord",
		ColorOverrides: map[string]string{"blocked": "#ff0000", "selbg": "#111111"},
		IconOverrides:  map[string]string{"working": "⚙️"},
		ASCII:          true,
	})
	if tm.Palette.Blocked != "#ff0000" {
		t.Fatalf("blocked override = %q, want #ff0000", tm.Palette.Blocked)
	}
	if tm.Palette.SelBg != "#111111" {
		t.Fatalf("selbg override = %q, want #111111", tm.Palette.SelBg)
	}
	// Untouched slots keep the preset value.
	if tm.Palette.App != "#88c0d0" {
		t.Fatalf("app = %q, want nord preset #88c0d0", tm.Palette.App)
	}
	// Icon overrides beat the ASCII base; other icons keep the base.
	if tm.Icons.Working != "⚙️" {
		t.Fatalf("working icon = %q, want override ⚙️", tm.Icons.Working)
	}
	if tm.Icons.Idle != "I" {
		t.Fatalf("idle icon = %q, want ASCII base I", tm.Icons.Idle)
	}
	if tm.Icons.Warn != "!" {
		t.Fatalf("warn icon = %q, want ASCII base !", tm.Icons.Warn)
	}
}

func TestNamesSorted(t *testing.T) {
	ns := Names()
	if len(ns) < 8 {
		t.Fatalf("Names() = %v, want at least 8 presets", ns)
	}
	for i := 1; i < len(ns); i++ {
		if ns[i-1] > ns[i] {
			t.Fatalf("Names() not sorted: %v", ns)
		}
	}
	found := false
	for _, n := range ns {
		if n == "default" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Names() missing default: %v", ns)
	}
}

func TestLipgloss(t *testing.T) {
	cases := map[string]string{
		"colour208": "208",
		"colour0":   "0",
		"colour15":  "15",
		"cyan":      "cyan",
		"#88c0d0":   "#88c0d0",
		"default":   "default",
		"":          "",
	}
	for in, want := range cases {
		if got := Lipgloss(in); got != want {
			t.Errorf("Lipgloss(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestDefaultMatchesClassicColors locks the default palette to the exact
// tmux style strings tmon has always emitted, so the status bar stays
// byte-identical until a user opts into a theme.
func TestDefaultMatchesClassicColors(t *testing.T) {
	d := Default.Palette
	want := Palette{
		App: "cyan", Blocked: "colour208", Working: "green", Idle: "blue",
		Dim: "colour240", Accent: "colour15", Warn: "yellow", SelBg: "colour236",
	}
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("Default palette drifted:\n got %+v\nwant %+v", d, want)
	}
}

func TestForStatus(t *testing.T) {
	ic := emojiIcons
	if got := ic.ForStatus(agent.StatusBlocked); got != "🛑" {
		t.Fatalf("ForStatus(blocked) = %q, want 🛑", got)
	}
	if got := ic.ForStatus(agent.StatusWorking); got != "⚡️" {
		t.Fatalf("ForStatus(working) = %q, want ⚡️", got)
	}
	if got := ic.ForStatus(agent.StatusIdle); got != "💤" {
		t.Fatalf("ForStatus(idle) = %q, want 💤", got)
	}
	// Unknown statuses fall back to the app mark.
	if got := ic.ForStatus("watching"); got != "🤖" {
		t.Fatalf("ForStatus(unknown) = %q, want app mark", got)
	}
}
