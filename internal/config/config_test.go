package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFromEnvDefaults(t *testing.T) {
	t.Setenv("HOME", "/home/tester")
	t.Setenv("XDG_STATE_HOME", "")
	for _, k := range []string{
		"TMON_STATE_DIR", "TMON_BIN_DIR",
		"TMON_POLL_INTERVAL_MS", "TMON_ACTIVITY_THRESHOLD_MS",
		"TMON_IO_ACTIVITY_THRESHOLD", "TMON_IDLE_DECAY_POLLS",
		"TMON_ASCII_ICONS", "TMON_BOLD_COUNTS",
	} {
		t.Setenv(k, "")
	}

	c := FromEnv()
	// Prefer DefaultStateDir() so UserHomeDir vs HOME stay consistent.
	wantState := DefaultStateDir()
	if c.StateDir != wantState {
		t.Errorf("StateDir = %q, want %q", c.StateDir, wantState)
	}
	if c.BinDir != "/home/tester/.tmon/bin" {
		t.Errorf("BinDir = %q, want %q", c.BinDir, filepath.Join("/home/tester", ".tmon", "bin"))
	}
	if c.PollIntervalMs != 3000 || c.ActivityThresholdMs != 500 || c.IOThreshold != 102400 || c.IdleDecayPolls != 3 {
		t.Errorf("unexpected defaults: %+v", c)
	}
}

func TestDefaultStateDirXDG(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/xdg/state")
	if got := DefaultStateDir(); got != "/xdg/state/tmon" {
		t.Fatalf("DefaultStateDir = %q, want /xdg/state/tmon", got)
	}
}

func TestDefaultStateDirHomeFallback(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/home/tester")
	// UserHomeDir may still win; force HOME-based path via empty XDG.
	// When HOME is set, path is ~/.local/state/tmon.
	got := DefaultStateDir()
	// Accept either UserHomeDir() result or HOME-based when they match.
	if !filepath.IsAbs(got) || filepath.Base(got) != "tmon" {
		t.Fatalf("DefaultStateDir = %q, want absolute …/tmon", got)
	}
	if filepath.Base(filepath.Dir(got)) != "state" {
		t.Fatalf("DefaultStateDir = %q, want …/state/tmon", got)
	}
}

func TestDefaultsStateDirIsDurable(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	c := Defaults()
	// Must not use $TMPDIR/tmon — that collides with a binary named tmon.
	if c.StateDir == filepath.Join(os.TempDir(), "tmon") {
		t.Fatalf("StateDir must not be %q (file collision risk)", c.StateDir)
	}
	if filepath.Base(c.StateDir) != "tmon" {
		t.Fatalf("StateDir = %q, want basename tmon", c.StateDir)
	}
	if c.HookStateDir != filepath.Join(c.StateDir, "hooks") {
		t.Fatalf("HookStateDir = %q, want under StateDir/hooks", c.HookStateDir)
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
	wantHooks := filepath.Join(DefaultStateDir(), "hooks")
	if c.HookStateDir != wantHooks {
		t.Errorf("HookStateDir = %q, want %q", c.HookStateDir, wantHooks)
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

func TestASCIIIconsDefaultsToEmoji(t *testing.T) {
	t.Setenv("TMON_ASCII_ICONS", "")
	c := FromEnv()
	if c.ASCII {
		t.Errorf("ASCII = true, want false (emoji default)")
	}
}

func TestASCIIIconsOverride(t *testing.T) {
	for _, v := range []string{"1", "on", "true", "yes"} {
		t.Setenv("TMON_ASCII_ICONS", v)
		if !FromEnv().ASCII {
			t.Errorf("ASCII = false with %q, want true", v)
		}
	}
	t.Setenv("TMON_ASCII_ICONS", "0")
	if FromEnv().ASCII {
		t.Error("ASCII = true with \"0\", want false")
	}
}

func TestASCIIIconsIgnoresGarbage(t *testing.T) {
	t.Setenv("TMON_ASCII_ICONS", "soon")
	if FromEnv().ASCII {
		t.Error("garbage ASCII env should fall back to the default (false)")
	}
}

func TestBoldCountsDefaultsToOn(t *testing.T) {
	t.Setenv("TMON_BOLD_COUNTS", "")
	if !FromEnv().BoldCounts {
		t.Error("BoldCounts = false, want true (default)")
	}
}

func TestBoldCountsOverride(t *testing.T) {
	for _, v := range []string{"1", "on", "true", "yes"} {
		t.Setenv("TMON_BOLD_COUNTS", v)
		if !FromEnv().BoldCounts {
			t.Errorf("BoldCounts = false with %q, want true", v)
		}
	}
	t.Setenv("TMON_BOLD_COUNTS", "0")
	if FromEnv().BoldCounts {
		t.Error("BoldCounts = true with \"0\", want false")
	}
}

func TestBoldCountsIgnoresGarbage(t *testing.T) {
	t.Setenv("TMON_BOLD_COUNTS", "soon")
	if !FromEnv().BoldCounts {
		t.Error("garbage BoldCounts env should fall back to the default (true)")
	}
}

func TestThemeDefaultsToDefault(t *testing.T) {
	// Empty state dir so a developer's state/theme cannot leak into the test.
	t.Setenv("TMON_STATE_DIR", t.TempDir())
	t.Setenv("TMON_THEME", "")
	c := FromEnv()
	if c.Theme != "default" {
		t.Errorf("Theme = %q, want default", c.Theme)
	}
	if c.ColorOverrides != nil || c.IconOverrides != nil {
		t.Errorf("overrides = %v / %v, want nil with no env", c.ColorOverrides, c.IconOverrides)
	}
}

func TestContextWarnDefault(t *testing.T) {
	if c := Defaults(); c.ContextWarn != 85 {
		t.Fatalf("default ContextWarn = %d, want 85", c.ContextWarn)
	}
}

func TestContextWarnFromEnv(t *testing.T) {
	t.Setenv("TMON_CONTEXT_WARN", "70")
	if c := FromEnv(); c.ContextWarn != 70 {
		t.Fatalf("ContextWarn = %d, want 70", c.ContextWarn)
	}
	t.Setenv("TMON_CONTEXT_WARN", "0") // 0 disables the warning
	if c := FromEnv(); c.ContextWarn != 0 {
		t.Fatalf("ContextWarn = %d, want 0", c.ContextWarn)
	}
	t.Setenv("TMON_CONTEXT_WARN", "junk") // unparsable falls back to default
	if c := FromEnv(); c.ContextWarn != 85 {
		t.Fatalf("ContextWarn = %d, want 85 fallback", c.ContextWarn)
	}
}

func TestClampInvalidValues(t *testing.T) {
	t.Setenv("TMON_POLL_INTERVAL_MS", "0")
	t.Setenv("TMON_IDLE_DECAY_POLLS", "0")
	t.Setenv("TMON_ACTIVITY_THRESHOLD_MS", "-1")
	t.Setenv("TMON_IO_ACTIVITY_THRESHOLD", "-5")
	t.Setenv("TMON_CONTEXT_WARN", "150")
	t.Setenv("TMON_CLK_TCK", "0")
	c := FromEnv()
	if c.PollIntervalMs != 3000 {
		t.Errorf("PollIntervalMs = %d, want 3000 clamp", c.PollIntervalMs)
	}
	if c.IdleDecayPolls != 1 {
		t.Errorf("IdleDecayPolls = %d, want 1 clamp", c.IdleDecayPolls)
	}
	if c.ActivityThresholdMs != 500 {
		t.Errorf("ActivityThresholdMs = %d, want 500 clamp", c.ActivityThresholdMs)
	}
	if c.IOThreshold != 102400 {
		t.Errorf("IOThreshold = %d, want 102400 clamp", c.IOThreshold)
	}
	if c.ContextWarn != 100 {
		t.Errorf("ContextWarn = %d, want 100 clamp", c.ContextWarn)
	}
	if c.CLKTicks != 100 {
		t.Errorf("CLKTicks = %d, want 100 clamp", c.CLKTicks)
	}
}

func TestPaneBorderFromEnv(t *testing.T) {
	if c := Defaults(); !c.PaneBorder {
		t.Fatal("default PaneBorder should be on")
	}
	if c := Defaults(); c.PaneBorderPosition != "top" {
		t.Fatalf("default PaneBorderPosition = %q, want top", c.PaneBorderPosition)
	}
	// Explicit off wins over the default.
	t.Setenv("TMON_PANE_BORDER", "off")
	if c := FromEnv(); c.PaneBorder {
		t.Fatal("PaneBorder should be off with TMON_PANE_BORDER=off")
	}
	t.Setenv("TMON_PANE_BORDER", "on")
	c := FromEnv()
	if !c.PaneBorder {
		t.Fatal("PaneBorder should be on with TMON_PANE_BORDER=on")
	}
	if c.PaneBorderPosition != "top" {
		t.Fatalf("PaneBorderPosition = %q, want top", c.PaneBorderPosition)
	}
	t.Setenv("TMON_PANE_BORDER_POSITION", "bottom")
	if c := FromEnv(); c.PaneBorderPosition != "bottom" {
		t.Fatalf("PaneBorderPosition = %q, want bottom", c.PaneBorderPosition)
	}
	t.Setenv("TMON_PANE_BORDER_POSITION", "sideways") // invalid → top
	if c := FromEnv(); c.PaneBorderPosition != "top" {
		t.Fatalf("invalid position = %q, want top", c.PaneBorderPosition)
	}
}

func TestThemeOverrides(t *testing.T) {
	// Isolate from any durable state/theme the developer may have on disk.
	t.Setenv("TMON_STATE_DIR", t.TempDir())
	t.Setenv("TMON_THEME", "nord")
	t.Setenv("TMON_COLOR_BLOCKED", "#ff0000")
	t.Setenv("TMON_COLOR_SELBG", "colour237")
	t.Setenv("TMON_ICON_WORKING", "⚙️")
	t.Setenv("TMON_ICON_APP", "")
	c := FromEnv()
	if c.Theme != "nord" {
		t.Errorf("Theme = %q, want nord", c.Theme)
	}
	wantColors := map[string]string{"blocked": "#ff0000", "selbg": "colour237"}
	if len(c.ColorOverrides) != len(wantColors) {
		t.Fatalf("ColorOverrides = %v, want %v", c.ColorOverrides, wantColors)
	}
	for k, v := range wantColors {
		if c.ColorOverrides[k] != v {
			t.Errorf("ColorOverrides[%q] = %q, want %q", k, c.ColorOverrides[k], v)
		}
	}
	// Empty values are ignored, so the icon-app override is dropped.
	if len(c.IconOverrides) != 1 || c.IconOverrides["working"] != "⚙️" {
		t.Errorf("IconOverrides = %v, want {working: ⚙️} only", c.IconOverrides)
	}
}

func TestThemeFileWinsOverEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMON_STATE_DIR", dir)
	t.Setenv("TMON_THEME", "nord")
	// No file yet: env wins.
	if c := FromEnv(); c.Theme != "nord" {
		t.Fatalf("without file, Theme = %q, want nord", c.Theme)
	}
	// Durable state/theme overrides a stale TMON_THEME (session env often
	// keeps the value from session creation and shadows a later global update).
	if err := os.WriteFile(filepath.Join(dir, "theme"), []byte("tokyonight\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := FromEnv(); c.Theme != "tokyonight" {
		t.Fatalf("with file, Theme = %q, want tokyonight", c.Theme)
	}
	// Whitespace-only file is ignored; fall back to env.
	if err := os.WriteFile(filepath.Join(dir, "theme"), []byte("  \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := FromEnv(); c.Theme != "nord" {
		t.Fatalf("whitespace file, Theme = %q, want nord", c.Theme)
	}
}
