package tmux

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestRunKillsHangingCommand(t *testing.T) {
	old := execCommand
	// Replace tmux with a sleep that would hang without the deadline.
	execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sleep", "30")
	}
	t.Cleanup(func() { execCommand = old })

	start := time.Now()
	_, err := Run("any")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error from cancelled hanging command")
	}
	// DefaultTimeout is 2s; allow generous CI slop but fail if we slept ~30s.
	if elapsed > 5*time.Second {
		t.Fatalf("Run took %v, want cancel near DefaultTimeout (%v)", elapsed, DefaultTimeout)
	}
}
