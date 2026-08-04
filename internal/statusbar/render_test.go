package statusbar

import (
	"strings"
	"testing"

	"github.com/guillaumemeyer/tmon/internal/agent"
)

func TestRenderEmptyAscii(t *testing.T) {
	got := Render(nil, true, false)
	want := "#[fg=cyan][@]#[default] "
	if got != want {
		t.Errorf("Render(empty, ascii) =\n  %q\nwant:\n  %q", got, want)
	}
}

func TestRenderEmptyEmoji(t *testing.T) {
	got := Render(nil, false, false)
	want := "#[fg=cyan]🤖#[default] "
	if got != want {
		t.Errorf("Render(empty, emoji) =\n  %q\nwant:\n  %q", got, want)
	}
}

func TestRenderCountsAscii(t *testing.T) {
	statuses := []agent.Status{
		agent.StatusBlocked,
		agent.StatusBlocked,
		agent.StatusWorking,
		agent.StatusWorking,
		agent.StatusIdle,
	}
	// 2 blocked, 2 working, 1 idle.
	got := Render(statuses, true, false)
	want := "#[fg=cyan][@]#[default]-#[fg=colour208]B2#[default]-#[fg=green]W2#[default]-#[fg=blue]I1#[default] "
	if got != want {
		t.Errorf("Render(ascii) =\n  %q\nwant:\n  %q", got, want)
	}
}

func TestRenderCountsEmoji(t *testing.T) {
	statuses := []agent.Status{
		agent.StatusBlocked,
		agent.StatusBlocked,
		agent.StatusWorking,
		agent.StatusWorking,
		agent.StatusIdle,
	}
	// 2 blocked, 2 working, 1 idle.
	got := Render(statuses, false, false)
	want := "#[fg=cyan]🤖#[default]-#[fg=colour208]🚨2#[default]-#[fg=green]⚡️2#[default]-#[fg=blue]💤1#[default] "
	if got != want {
		t.Errorf("Render(emoji) =\n  %q\nwant:\n  %q", got, want)
	}
}

func TestRenderVisibilityHidesZeroSegments(t *testing.T) {
	// Only working: the blocked and idle segments are not rendered at all.
	onlyWorking := []agent.Status{agent.StatusWorking, agent.StatusWorking}
	got := Render(onlyWorking, true, false)
	want := "#[fg=cyan][@]#[default]-#[fg=green]W2#[default] "
	if got != want {
		t.Errorf("Render(only working, ascii) =\n  %q\nwant:\n  %q", got, want)
	}

	// Same rule in emoji mode: only idle visible.
	onlyIdle := []agent.Status{agent.StatusIdle, agent.StatusIdle, agent.StatusIdle}
	got = Render(onlyIdle, false, false)
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
	got := Render(ten, true, false)
	want := "#[fg=cyan][@]#[default]-#[fg=colour208]B10#[default] "
	if got != want {
		t.Errorf("Render(10 blocked, ascii) =\n  %q\nwant:\n  %q", got, want)
	}
}

func TestRenderBoldCounts(t *testing.T) {
	statuses := []agent.Status{agent.StatusBlocked, agent.StatusWorking, agent.StatusIdle}

	plain := Render(statuses, true, false)
	if strings.Contains(plain, "#[bold]") {
		t.Errorf("bold=false should not emit #[bold]: %q", plain)
	}

	got := Render(statuses, true, true)
	want := "#[fg=cyan][@]#[default]-#[fg=colour208]B#[bold]1#[default]-#[fg=green]W#[bold]1#[default]-#[fg=blue]I#[bold]1#[default] "
	if got != want {
		t.Errorf("Render(bold) =\n  %q\nwant:\n  %q", got, want)
	}
}
