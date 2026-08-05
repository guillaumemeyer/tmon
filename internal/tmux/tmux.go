// Package tmux is a thin wrapper around the tmux CLI. All tmux interaction
// in tmon goes through here so tests can inject fake output.
package tmux

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"time"
)

// DefaultTimeout bounds every tmux CLI call so a wedged server cannot hang
// the status-bar #() path forever.
const DefaultTimeout = 2 * time.Second

// execCommand is the CommandContext factory; tests replace it to inject hangs.
var execCommand = exec.CommandContext

// Run executes tmux with the given arguments and returns stdout. Errors from
// tmux (e.g. capture-pane on a vanished pane) are returned but callers
// generally degrade gracefully. Every call has a DefaultTimeout deadline.
func Run(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
	defer cancel()
	cmd := execCommand(ctx, "tmux", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = nil // tmux prints noise to stderr; ignore it
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return out.String(), nil
}

// Available reports whether we're running inside a tmux session.
func Available() bool {
	return os.Getenv("TMUX") != ""
}
