package config

import (
	"path/filepath"
	"testing"
	"time"
)

func TestFromEnvDefaults(t *testing.T) {
	t.Setenv("HOME", "/home/tester")
	for _, k := range []string{
		"TMON_STATE_DIR", "TMON_BIN_DIR",
		"TMON_POLL_INTERVAL_MS", "TMON_ACTIVITY_THRESHOLD_MS",
		"TMON_IO_ACTIVITY_THRESHOLD", "TMON_IDLE_DECAY_POLLS",
	} {
		t.Setenv(k, "")
	}

	c := FromEnv()
	if c.StateDir != "/home/tester/.tmon/state" {
		t.Errorf("StateDir = %q, want %q", c.StateDir, filepath.Join("/home/tester", ".tmon", "state"))
	}
	if c.BinDir != "/home/tester/.tmon/bin" {
		t.Errorf("BinDir = %q, want %q", c.BinDir, filepath.Join("/home/tester", ".tmon", "bin"))
	}
	if c.PollIntervalMs != 3000 || c.ActivityThresholdMs != 500 || c.IOThreshold != 102400 || c.IdleDecayPolls != 3 {
		t.Errorf("unexpected defaults: %+v", c)
	}
}

func TestFromEnvOverrides(t *testing.T) {
	t.Setenv("TMON_STATE_DIR", "/tmp/state") // arbitrary test value
	t.Setenv("TMON_BIN_DIR", "/opt/bin")
	t.Setenv("TMON_POLL_INTERVAL_MS", "5000")
	t.Setenv("TMON_ACTIVITY_THRESHOLD_MS", "200")
	t.Setenv("TMON_IO_ACTIVITY_THRESHOLD", "51200")
	t.Setenv("TMON_IDLE_DECAY_POLLS", "5")

	c := FromEnv()
	if c.StateDir != "/tmp/state" || c.BinDir != "/opt/bin" {
		t.Errorf("dirs not honored: %+v", c)
	}
	if c.PollIntervalMs != 5000 || c.ActivityThresholdMs != 200 || c.IOThreshold != 51200 || c.IdleDecayPolls != 5 {
		t.Errorf("values not honored: %+v", c)
	}
}

func TestFromEnvIgnoresGarbage(t *testing.T) {
	t.Setenv("TMON_POLL_INTERVAL_MS", "not-a-number")
	c := FromEnv()
	if c.PollIntervalMs != 3000 {
		t.Errorf("garbage env should fall back to default, got %d", c.PollIntervalMs)
	}
}

func TestPollIntervalSec(t *testing.T) {
	cases := []struct {
		ms  int
		sec int
	}{
		{3000, 3},
		{500, 1}, // rounded up to a minimum of 1
		{1500, 1},
	}
	for _, tc := range cases {
		c := Config{PollIntervalMs: tc.ms}
		if got := c.PollIntervalSec(); got != tc.sec {
			t.Errorf("PollIntervalSec(%d) = %d, want %d", tc.ms, got, tc.sec)
		}
	}
}

func TestStateFilePath(t *testing.T) {
	c := Config{StateDir: "/x/y"}
	if got := c.StateFilePath(); got != "/x/y/state.json" {
		t.Errorf("StateFilePath = %q, want /x/y/state.json", got)
	}
}

func TestConnectorDefaults(t *testing.T) {
	t.Setenv("HOME", "/home/tester")
	for _, k := range []string{"TMON_CONNECTORS", "TMON_CONNECTOR_FRESHNESS", "TMON_HOOK_STATE_DIR"} {
		t.Setenv(k, "")
	}
	c := FromEnv()
	if c.Connectors != "auto" {
		t.Errorf("Connectors = %q, want auto", c.Connectors)
	}
	if c.ConnectorFreshness != 30*time.Second {
		t.Errorf("ConnectorFreshness = %v, want 30s", c.ConnectorFreshness)
	}
	if c.HookStateDir != "/home/tester/.tmon/state/hooks" {
		t.Errorf("HookStateDir = %q, want state/hooks", c.HookStateDir)
	}
}

func TestConnectorOverrides(t *testing.T) {
	t.Setenv("TMON_CONNECTORS", "grok,hermes")
	t.Setenv("TMON_CONNECTOR_FRESHNESS", "45")
	t.Setenv("TMON_HOOK_STATE_DIR", "/tmp/hooks")
	c := FromEnv()
	if c.Connectors != "grok,hermes" {
		t.Errorf("Connectors = %q, want grok,hermes", c.Connectors)
	}
	if c.ConnectorFreshness != 45*time.Second {
		t.Errorf("ConnectorFreshness = %v, want 45s", c.ConnectorFreshness)
	}
	if c.HookStateDir != "/tmp/hooks" {
		t.Errorf("HookStateDir = %q, want /tmp/hooks", c.HookStateDir)
	}
}

func TestConnectorFreshnessIgnoresGarbage(t *testing.T) {
	t.Setenv("TMON_CONNECTOR_FRESHNESS", "soon")
	c := FromEnv()
	if c.ConnectorFreshness != 30*time.Second {
		t.Errorf("garbage freshness should fall back, got %v", c.ConnectorFreshness)
	}
}
