package statusbar

import (
	"slices"
	"strings"
	"testing"

	"charm.land/bubbles/v2/spinner"
	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/theme"
)

// agentsOf converts statuses into the AgentState slice Render consumes.
func agentsOf(statuses ...agent.Status) []agent.AgentState {
	out := make([]agent.AgentState, len(statuses))
	for i, s := range statuses {
		out[i].Status = s
	}
	return out
}

// pinSpinnerFrame fixes the working-spinner frame to "|" so Render output
// is deterministic; tests that don't pin it assert membership in the
// bubbles Line frames instead.
func pinSpinnerFrame(t *testing.T) {
	t.Helper()
	old := theme.SpinnerFrame
	theme.SpinnerFrame = func() string { return "|" }
	t.Cleanup(func() { theme.SpinnerFrame = old })
}

func TestRenderEmptyAscii(t *testing.T) {
	got := Render(nil, false, theme.Resolve(theme.Options{ASCII: true}), 0)
	want := "#[fg=cyan][@]#[default] "
	if got != want {
		t.Errorf("Render(empty, ascii) =\n  %q\nwant:\n  %q", got, want)
	}
}

func TestRenderEmptyEmoji(t *testing.T) {
	got := Render(nil, false, theme.Resolve(theme.Options{}), 0)
	want := "#[fg=cyan]🤖#[default] "
	if got != want {
		t.Errorf("Render(empty, emoji) =\n  %q\nwant:\n  %q", got, want)
	}
}

func TestRenderCountsAscii(t *testing.T) {
	pinSpinnerFrame(t)
	statuses := []agent.Status{
		agent.StatusBlocked,
		agent.StatusBlocked,
		agent.StatusWorking,
		agent.StatusWorking,
		agent.StatusIdle,
	}
	// 2 blocked, 2 working, 1 idle.
	got := Render(agentsOf(statuses...), false, theme.Resolve(theme.Options{ASCII: true}), 0)
	want := "#[fg=cyan][@]#[default]-#[fg=colour208]B2#[default]-#[fg=green]|2#[default]-#[fg=blue]I1#[default] "
	if got != want {
		t.Errorf("Render(ascii) =\n  %q\nwant:\n  %q", got, want)
	}
}

func TestRenderCountsEmoji(t *testing.T) {
	pinSpinnerFrame(t)
	statuses := []agent.Status{
		agent.StatusBlocked,
		agent.StatusBlocked,
		agent.StatusWorking,
		agent.StatusWorking,
		agent.StatusIdle,
	}
	// 2 blocked, 2 working, 1 idle.
	got := Render(agentsOf(statuses...), false, theme.Resolve(theme.Options{}), 0)
	want := "#[fg=cyan]🤖#[default]-#[fg=colour208]🚨2#[default]-#[fg=green]|2#[default]-#[fg=blue]💤1#[default] "
	if got != want {
		t.Errorf("Render(emoji) =\n  %q\nwant:\n  %q", got, want)
	}
}

func TestRenderVisibilityHidesZeroSegments(t *testing.T) {
	pinSpinnerFrame(t)
	// Only working: the blocked and idle segments are not rendered at all.
	onlyWorking := []agent.Status{agent.StatusWorking, agent.StatusWorking}
	got := Render(agentsOf(onlyWorking...), false, theme.Resolve(theme.Options{ASCII: true}), 0)
	want := "#[fg=cyan][@]#[default]-#[fg=green]|2#[default] "
	if got != want {
		t.Errorf("Render(only working, ascii) =\n  %q\nwant:\n  %q", got, want)
	}

	// Same rule in emoji mode: only idle visible.
	onlyIdle := []agent.Status{agent.StatusIdle, agent.StatusIdle, agent.StatusIdle}
	got = Render(agentsOf(onlyIdle...), false, theme.Resolve(theme.Options{}), 0)
	want = "#[fg=cyan]🤖#[default]-#[fg=blue]💤3#[default] "
	if got != want {
		t.Errorf("Render(only idle, emoji) =\n  %q\nwant:\n  %q", got, want)
	}
}

func TestRenderCountsUnpadded(t *testing.T) {
	// %d, not %2d: a double-digit count has no leading space.
	ten := make([]agent.Status, 10)
	for i := range ten {
		ten[i] = agent.StatusBlocked
	}
	got := Render(agentsOf(ten...), false, theme.Resolve(theme.Options{ASCII: true}), 0)
	want := "#[fg=cyan][@]#[default]-#[fg=colour208]B10#[default] "
	if got != want {
		t.Errorf("Render(10 blocked, ascii) =\n  %q\nwant:\n  %q", got, want)
	}
}

func TestRenderBoldCounts(t *testing.T) {
	pinSpinnerFrame(t)
	statuses := []agent.Status{agent.StatusBlocked, agent.StatusWorking, agent.StatusIdle}

	plain := Render(agentsOf(statuses...), false, theme.Resolve(theme.Options{ASCII: true}), 0)
	if strings.Contains(plain, "#[bold]") {
		t.Errorf("bold=false should not emit #[bold]: %q", plain)
	}

	got := Render(agentsOf(statuses...), true, theme.Resolve(theme.Options{ASCII: true}), 0)
	want := "#[fg=cyan][@]#[default]-#[fg=colour208]B#[bold]1#[default]-#[fg=green]|#[bold]1#[default]-#[fg=blue]I#[bold]1#[default] "
	if got != want {
		t.Errorf("Render(bold) =\n  %q\nwant:\n  %q", got, want)
	}
}

func TestRenderThemedColors(t *testing.T) {
	statuses := []agent.Status{agent.StatusBlocked}
	got := Render(agentsOf(statuses...), false, theme.Resolve(theme.Options{Name: "nord", ASCII: true}), 0)
	want := "#[fg=#88c0d0][@]#[default]-#[fg=#bf616a]B1#[default] "
	if got != want {
		t.Errorf("Render(nord) =\n  %q\nwant:\n  %q", got, want)
	}
}

func TestRenderIconOverrides(t *testing.T) {
	pinSpinnerFrame(t)
	// Non-working slots honor @tmon-icon-* overrides…
	statuses := []agent.Status{agent.StatusBlocked, agent.StatusWorking, agent.StatusIdle}
	tm := theme.Resolve(theme.Options{
		ASCII:         true,
		IconOverrides: map[string]string{"idle": "⚙️"},
		ColorOverrides: map[string]string{
			"app": "magenta", "working": "yellow",
		},
	})
	got := Render(agentsOf(statuses...), false, tm, 0)
	want := "#[fg=magenta][@]#[default]-#[fg=colour208]B1#[default]-#[fg=yellow]|1#[default]-#[fg=blue]⚙️1#[default] "
	if got != want {
		t.Errorf("Render(overrides) =\n  %q\nwant:\n  %q", got, want)
	}

	// …but a working override is superseded by the spinner, like the popup.
	tm2 := theme.Resolve(theme.Options{
		ASCII:         true,
		IconOverrides: map[string]string{"working": "⚙️"},
	})
	got2 := Render(agentsOf(agent.StatusWorking), false, tm2, 0)
	want2 := "#[fg=cyan][@]#[default]-#[fg=green]|1#[default] "
	if got2 != want2 {
		t.Errorf("Render(working override) =\n  %q\nwant spinner instead:\n  %q", got2, want2)
	}
}

// TestRenderWorkingUsesSpinnerFrame locks the working segment to the bubbles
// Line frames — the same spinner the dashboard animates — so the status bar
// never regresses to a static ⚡️ emoji or an ASCII "W".
func TestRenderWorkingUsesSpinnerFrame(t *testing.T) {
	got := Render(agentsOf(agent.StatusWorking), false, theme.Resolve(theme.Options{}), 0)
	prefix := "#[fg=cyan]🤖#[default]-#[fg=green]"
	if !strings.HasPrefix(got, prefix) {
		t.Fatalf("working segment missing:\n  %q", got)
	}
	frame := strings.TrimSuffix(strings.TrimPrefix(got, prefix), "1#[default] ")
	if !slices.Contains(spinner.Line.Frames, frame) {
		t.Fatalf("working glyph %q is not a bubbles Line frame %v", frame, spinner.Line.Frames)
	}
}

func TestRenderWarnSegment(t *testing.T) {
	pinSpinnerFrame(t)
	// At/over the threshold a ⚠️ segment is appended in the warn color.
	agents := []agent.AgentState{
		{Status: agent.StatusWorking, Usage: &agent.Usage{TokensUsed: 180000, WindowTokens: 200000}}, // 90%
	}
	got := Render(agents, false, theme.Resolve(theme.Options{}), 85)
	want := "#[fg=cyan]🤖#[default]-#[fg=green]|1#[default]-#[fg=yellow]⚠️#[default] "
	if got != want {
		t.Errorf("Render(90%% warn) =\n  %q\nwant:\n  %q", got, want)
	}

	// ASCII mode uses "!" for the warning marker.
	got = Render(agents, false, theme.Resolve(theme.Options{ASCII: true}), 85)
	want = "#[fg=cyan][@]#[default]-#[fg=green]|1#[default]-#[fg=yellow]!#[default] "
	if got != want {
		t.Errorf("Render(90%% warn, ascii) =\n  %q\nwant:\n  %q", got, want)
	}

	// The warn color comes from the theme palette (nord → #ebcb8b).
	got = Render(agents, false, theme.Resolve(theme.Options{Name: "nord"}), 85)
	want = "#[fg=#88c0d0]🤖#[default]-#[fg=#a3be8c]|1#[default]-#[fg=#ebcb8b]⚠️#[default] "
	if got != want {
		t.Errorf("Render(90%% warn, nord) =\n  %q\nwant:\n  %q", got, want)
	}

	// Below the threshold: no warning segment.
	agents[0].Usage = &agent.Usage{TokensUsed: 52367, WindowTokens: 200000} // 26%
	got = Render(agents, false, theme.Resolve(theme.Options{}), 85)
	want = "#[fg=cyan]🤖#[default]-#[fg=green]|1#[default] "
	if got != want {
		t.Errorf("Render(26%%) =\n  %q\nwant:\n  %q", got, want)
	}

	// A nil usage pointer never warns.
	agents[0].Usage = nil
	got = Render(agents, false, theme.Resolve(theme.Options{}), 85)
	want = "#[fg=cyan]🤖#[default]-#[fg=green]|1#[default] "
	if got != want {
		t.Errorf("Render(nil usage) =\n  %q\nwant:\n  %q", got, want)
	}

	// warnPct 0 disables the warning even at 100% usage.
	agents[0].Usage = &agent.Usage{TokensUsed: 200000, WindowTokens: 200000}
	got = Render(agents, false, theme.Resolve(theme.Options{}), 0)
	want = "#[fg=cyan]🤖#[default]-#[fg=green]|1#[default] "
	if got != want {
		t.Errorf("Render(0 threshold) =\n  %q\nwant:\n  %q", got, want)
	}

	// Exactly at the threshold counts as warned.
	agents[0].Usage = &agent.Usage{TokensUsed: 170000, WindowTokens: 200000} // 85%
	got = Render(agents, false, theme.Resolve(theme.Options{}), 85)
	want = "#[fg=cyan]🤖#[default]-#[fg=green]|1#[default]-#[fg=yellow]⚠️#[default] "
	if got != want {
		t.Errorf("Render(85%% exactly) =\n  %q\nwant:\n  %q", got, want)
	}
}
