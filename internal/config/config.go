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
	ContextWarn         int               // context-usage % at which the ⚠️ warning appears (0 disables)
	BlockedBell         bool              // ring the terminal bell when an agent transitions to blocked
}

// Defaults returns the configuration used when no TMON_* variables are set.
// tmon.tmux always exports the real values (pointing inside the plugin dir);
// these defaults only apply to standalone/debug invocations and deliberately
// avoid the system cache and temp dirs.
func Defaults() Config {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.Getenv("HOME")
	}
	stateDir := filepath.Join(home, ".tmon", "state")
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
		ContextWarn:         85,
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
	c.BlockedBell = envBool("TMON_BLOCKED_BELL", c.BlockedBell)
	if v := os.Getenv("TMON_THEME"); v != "" {
		c.Theme = v
	}
	c.ColorOverrides = envMap("TMON_COLOR_")
	c.IconOverrides = envMap("TMON_ICON_")
	return c
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
