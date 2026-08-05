// Package config loads tmon's runtime configuration from the TMON_*
// environment variables. tmon.tmux reads the @tmon-* tmux options and exports
// these variables, so the Go binary itself never needs to talk to tmux for
// configuration.
package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime settings for a single tmon invocation.
type Config struct {
	StateDir            string // directory holding state.json
	BinDir              string // directory holding the tmon binary
	PollIntervalMs      int    // milliseconds between full scans
	ActivityThresholdMs int    // CPU ms/s to consider "working"
	IOThreshold         int64  // min IO bytes/poll to consider "working"
	IdleDecayPolls      int    // consecutive quiet polls before "idle"
	CLKTicks            int    // kernel clock ticks per second (default 100)
	Connectors          string // comma list or "auto" (enable every connector whose paths exist)
	ConnectorFreshness  time.Duration
	HookStateDir        string            // where installed agent hooks write session state
	ASCII               bool              // render status icons as ASCII (B/W/I) instead of emoji
	BoldCounts          bool              // render the per-status counts in the status bar in bold
	Theme               string            // theme preset name (default, catppuccin, nord, …)
	ColorOverrides      map[string]string // @tmon-color-* overrides: slot → color string
	IconOverrides       map[string]string // @tmon-icon-* overrides: slot → glyph
	ContextWarn        int               // context-usage % at which the ⚠️ warning appears (0 disables)
	PaneBorder         bool              // show a status-colored border strip on agent panes
	PaneBorderPosition string            // "top" or "bottom" for pane-border-status
}

// DefaultStateDir is the durable runtime state directory used when
// TMON_STATE_DIR is unset: $XDG_STATE_HOME/tmon, or ~/.local/state/tmon.
//
// Theme, dashboard prefs, state.json, and hook crumbs live here so they
// survive rebuilds, tmux reloads, and reboots. A path under $TMPDIR/tmon is
// intentionally avoided: that name collides with a common scratch binary
// (/tmp/tmon as a file), which made MkdirAll fail and wiped theme restore.
func DefaultStateDir() string {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "tmon")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.Getenv("HOME")
	}
	if home == "" {
		// Last resort: still prefer a directory name that cannot collide
		// with a "tmon" binary dropped in the temp root.
		return filepath.Join(os.TempDir(), "tmon-state")
	}
	return filepath.Join(home, ".local", "state", "tmon")
}

// Defaults returns the configuration used when no TMON_* variables are set.
// tmon.tmux exports the plugin values into the tmux environment; these
// defaults apply to standalone/debug invocations. Runtime state lives under
// DefaultStateDir so the plugin tree stays read-mostly (binary + scripts).
func Defaults() Config {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.Getenv("HOME")
	}
	stateDir := DefaultStateDir()
	return Config{
		StateDir:            stateDir,
		BinDir:              filepath.Join(home, ".tmon", "bin"),
		PollIntervalMs:      3000,
		ActivityThresholdMs: 500,
		IOThreshold:         102400,
		IdleDecayPolls:      3,
		CLKTicks:            100,
		Connectors:          "auto",
		ConnectorFreshness:  30 * time.Second,
		HookStateDir:        filepath.Join(stateDir, "hooks"),
		BoldCounts:          true,
		Theme:               "default",
		ContextWarn:        85,
		PaneBorder:         true,
		PaneBorderPosition: "top",
	}
}

func envInt(name string, def int) int {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envInt64(name string, def int64) int64 {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}

func envBool(name string, def bool) bool {
	if v := os.Getenv(name); v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "on", "yes":
			return true
		case "0", "false", "off", "no":
			return false
		}
	}
	return def
}

// FromEnv builds a Config from the TMON_* environment variables, falling back
// to Defaults for anything unset.
func FromEnv() Config {
	c := Defaults()
	if v := os.Getenv("TMON_STATE_DIR"); v != "" {
		c.StateDir = v
	}
	if v := os.Getenv("TMON_BIN_DIR"); v != "" {
		c.BinDir = v
	}
	c.PollIntervalMs = envInt("TMON_POLL_INTERVAL_MS", c.PollIntervalMs)
	c.ActivityThresholdMs = envInt("TMON_ACTIVITY_THRESHOLD_MS", c.ActivityThresholdMs)
	c.IOThreshold = envInt64("TMON_IO_ACTIVITY_THRESHOLD", c.IOThreshold)
	c.IdleDecayPolls = envInt("TMON_IDLE_DECAY_POLLS", c.IdleDecayPolls)
	c.CLKTicks = envInt("TMON_CLK_TCK", c.CLKTicks)
	if v := os.Getenv("TMON_CONNECTORS"); v != "" {
		c.Connectors = v
	}
	if v := os.Getenv("TMON_CONNECTOR_FRESHNESS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.ConnectorFreshness = time.Duration(n) * time.Second
		}
	}
	if v := os.Getenv("TMON_HOOK_STATE_DIR"); v != "" {
		c.HookStateDir = v
	}
	c.ASCII = envBool("TMON_ASCII_ICONS", c.ASCII)
	c.BoldCounts = envBool("TMON_BOLD_COUNTS", c.BoldCounts)
	c.ContextWarn = envInt("TMON_CONTEXT_WARN", c.ContextWarn)
	c.PaneBorder = envBool("TMON_PANE_BORDER", c.PaneBorder)
	if v := os.Getenv("TMON_PANE_BORDER_POSITION"); v != "" {
		switch strings.ToLower(v) {
		case "top", "bottom":
			c.PaneBorderPosition = strings.ToLower(v)
		default:
			c.PaneBorderPosition = "top"
		}
	}
	if c.PaneBorderPosition == "" {
		c.PaneBorderPosition = "top"
	}
	if v := os.Getenv("TMON_THEME"); v != "" {
		c.Theme = v
	}
	// state/theme is the durable source of truth. TMON_THEME can be stale:
	// tmux copies the global environment into each session at creation time,
	// so a later set-environment -g (or a plugin reload that restores from
	// this file) is shadowed by the session's old value. Prefer the file
	// whenever it is present so status, dashboard, and borders agree after
	// a restart.
	if data, err := os.ReadFile(filepath.Join(c.StateDir, "theme")); err == nil {
		if name := strings.TrimSpace(string(data)); name != "" {
			c.Theme = name
		}
	}
	c.ColorOverrides = envMap("TMON_COLOR_")
	c.IconOverrides = envMap("TMON_ICON_")
	// Clamp invalid values so a bad env never produces a 0ms sleep or
	// nonsense threshold math on the status-bar path.
	c.clamp()
	return c
}

// clamp rewrites nonsensical config values to safe defaults. The status-bar
// #() path must never fail closed on a bad TMON_* value.
func (c *Config) clamp() {
	if c.PollIntervalMs <= 0 {
		c.PollIntervalMs = 3000
	}
	if c.IdleDecayPolls < 1 {
		c.IdleDecayPolls = 1
	}
	if c.ActivityThresholdMs < 0 {
		c.ActivityThresholdMs = 500
	}
	if c.IOThreshold < 0 {
		c.IOThreshold = 102400
	}
	if c.ContextWarn < 0 {
		c.ContextWarn = 0
	}
	if c.ContextWarn > 100 {
		c.ContextWarn = 100
	}
	if c.CLKTicks <= 0 {
		c.CLKTicks = 100
	}
}

// envMap collects every environment variable with the given prefix into a
// map keyed by the lower-cased remainder ("TMON_COLOR_BLOCKED" → "blocked").
// Empty values are ignored, and a nil map is returned when nothing is set.
func envMap(prefix string) map[string]string {
	var out map[string]string
	for _, kv := range os.Environ() {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || v == "" || !strings.HasPrefix(k, prefix) {
			continue
		}
		if out == nil {
			out = make(map[string]string)
		}
		out[strings.ToLower(strings.TrimPrefix(k, prefix))] = v
	}
	return out
}

// PollIntervalSec returns the poll interval in whole seconds (minimum 1),
// used when converting per-second thresholds to per-poll deltas.
func (c Config) PollIntervalSec() int {
	s := c.PollIntervalMs / 1000
	if s < 1 {
		return 1
	}
	return s
}

// StateFilePath is the JSON state file path.
func (c Config) StateFilePath() string { return filepath.Join(c.StateDir, "state.json") }
