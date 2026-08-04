package statusbar

import (
	"testing"

	"github.com/guillaumemeyer/tmon/internal/agent"
)

func TestRenderEmpty(t *testing.T) {
	got := Render(nil, 0)
	want := "#[fg=cyan][@]#[default]#[fg=colour208]? 0#[default]-#[fg=green]● 0#[default]-#[fg=blue]‖ 0#[default] "
	if got != want {
		t.Errorf("Render(empty) =\n  %q\nwant:\n  %q", got, want)
	}
}

func TestRenderCounts(t *testing.T) {
	statuses := []agent.Status{
		agent.StatusBlocked,
		agent.StatusBlocked,
		agent.StatusActive,
		agent.StatusRunning,
		agent.StatusPaused,
	}
	// 2 blocked, 2 active (active + running), 1 paused.
	got := Render(statuses, 0)
	want := "#[fg=cyan][@]#[default]#[fg=colour208]? 2#[default]-#[fg=green]● 2#[default]-#[fg=blue]‖ 1#[default] "
	if got != want {
		t.Errorf("Render =\n  %q\nwant:\n  %q", got, want)
	}
}

func TestRenderAnimationToggles(t *testing.T) {
	statuses := []agent.Status{agent.StatusBlocked, agent.StatusActive}

	even := Render(statuses, 0)
	if !contains(even, "? 1") || !contains(even, "● 1") {
		t.Errorf("even frame should show static chars: %q", even)
	}

	odd := Render(statuses, 1)
	if !contains(odd, "! 1") || !contains(odd, "! 1") {
		t.Errorf("odd frame should show animated chars: %q", odd)
	}
}

func TestRenderNoAnimationWhenSingleStateEmpty(t *testing.T) {
	// Animation only toggles when there are agents in that bucket.
	onlyPaused := []agent.Status{agent.StatusPaused}
	if got := Render(onlyPaused, 1); !contains(got, "‖ 1") || contains(got, "! 1") {
		t.Errorf("paused-only render wrong: %q", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
