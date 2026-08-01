// Package tmux is a thin wrapper around the tmux CLI. All tmux interaction
// in tmon goes through here so tests can inject fake output.
package tmux

import (
	"bytes"
	"os"
	"os/exec"
)

// Run executes tmux with the given arguments and returns stdout. Errors from
// tmux (e.g. capture-pane on a vanished pane) are returned but callers
// generally degrade gracefully.
func Run(args ...string) (string, error) {
	cmd := exec.Command("tmux", args...)
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
