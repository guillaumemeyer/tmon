// Package config loads tmon's runtime configuration from the TMON_*
// environment variables. tmon.tmux reads the @tmon-* tmux options and exports
// these variables, so the Go binary itself never needs to talk to tmux for
// configuration.
package config

import (
	"os"
	"path/filepath"
	"strconv"
)

// Config holds all runtime settings for a single tmon invocation.
type Config struct {
	StateDir            string // directory holding state.json
	BinDir              string // directory holding the tmon binary
	PollIntervalMs      int    // milliseconds between full scans
	ActivityThresholdMs int    // CPU ms/s to consider "active"
	IOThreshold         int64  // min IO bytes/poll to consider "active"
	IdleDecayPolls      int    // consecutive idle polls before "idle"
	CLKTicks            int    // kernel clock ticks per second (default 100)
}

// Defaults returns the configuration used when no TMON_* variables are set.
// tmon.tmux always exports the real values (pointing inside the plugin dir);
// these defaults only apply to standalone/debug invocations and deliberately
// avoid the system cache and temp dirs.
func Defaults() Config {
	return Config{
		StateDir:            filepath.Join(os.Getenv("HOME"), ".tmon", "state"),
		BinDir:              filepath.Join(os.Getenv("HOME"), ".tmon", "bin"),
		PollIntervalMs:      3000,
		ActivityThresholdMs: 500,
		IOThreshold:         102400,
		IdleDecayPolls:      3,
		CLKTicks:            100,
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
	return c
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
