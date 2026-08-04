package main

import (
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/guillaumemeyer/tmon/internal/config"
	"github.com/guillaumemeyer/tmon/internal/dashboard"
)

// cmdDashboard opens the interactive agent popup. tmux launches it via
// display-popup; run standalone it still works, just without pane mapping.
// The alt screen gives the popup its own full-surface canvas; if a tmux
// popup ever misbehaves, tea.WithAltScreen(false) is the fallback.
func cmdDashboard(args []string) int {
	if len(args) > 0 {
		fmt.Fprintf(os.Stderr, "tmon: dashboard takes no arguments\n")
		return 2
	}

	cfg := config.FromEnv()
	m := dashboard.New(dashboard.DefaultLoader(cfg), cfg.ASCII).
		WithSettingsPath(filepath.Join(cfg.StateDir, "dashboard.json")).
		WithVersion("v" + version)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "tmon: dashboard:", err)
		return 1
	}
	return 0
}
