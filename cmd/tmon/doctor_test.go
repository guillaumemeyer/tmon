package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/guillaumemeyer/tmon/internal/config"
	"github.com/guillaumemeyer/tmon/internal/detect"
	"github.com/guillaumemeyer/tmon/internal/worker"
)

// doctorEnv stubs every external probe the doctor touches — tmux, PATH
// lookups, the process scan, hook status — and restores the real ones on
// cleanup, so the checks run against a deterministic machine.
func doctorEnv(t *testing.T) {
	t.Helper()
	oldLook := doctorLookPath
	doctorLookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	t.Cleanup(func() { doctorLookPath = oldLook })

	oldTmux := doctorTmuxVersion
	doctorTmuxVersion = func() (string, error) { return "tmux 3.4", nil }
	t.Cleanup(func() { doctorTmuxVersion = oldTmux })

	oldScan := doctorScanAgents
	doctorScanAgents = func() ([]detect.Agent, error) { return nil, nil }
	t.Cleanup(func() { doctorScanAgents = oldScan })

	oldHooks := doctorHookStatus
	doctorHookStatus = func(string) (bool, bool) { return false, false }
	t.Cleanup(func() { doctorHookStatus = oldHooks })
}

// testDoctorCfg returns a config with temp state/hook dirs so the checks
// never touch the real machine, plus a matching VERSION file for the
// binary check.
func testDoctorCfg(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Defaults()
	cfg.StateDir = filepath.Join(t.TempDir(), "state")
	cfg.HookStateDir = filepath.Join(t.TempDir(), "hooks")
	return cfg
}

func TestDoctorAllPass(t *testing.T) {
	doctorEnv(t)
	oldVersion := version
	version = "0.4.2"
	t.Cleanup(func() { version = oldVersion })

	cfg := testDoctorCfg(t)
	cfg.BinDir = filepath.Join(t.TempDir(), "bin")
	if err := os.WriteFile(filepath.Join(filepath.Dir(cfg.BinDir), "VERSION"), []byte("0.4.2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	checks := runChecks(cfg)
	if !allOK(checks) {
		t.Fatalf("expected all checks to pass: %+v", checks)
	}
	if code := exitCode(checks); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
}

func TestDoctorFailsOnOldTmux(t *testing.T) {
	doctorEnv(t)
	oldTmux := doctorTmuxVersion
	doctorTmuxVersion = func() (string, error) { return "tmux 3.0", nil }
	t.Cleanup(func() { doctorTmuxVersion = oldTmux })

	checks := runChecks(testDoctorCfg(t))
	if allOK(checks) {
		t.Fatal("expected a failing check for tmux < 3.2")
	}
	if code := exitCode(checks); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	for _, c := range checks {
		if c.Name == "tmux" && c.OK {
			t.Fatalf("tmux check should fail: %+v", c)
		}
	}
}

func TestDoctorFailsOnMissingTools(t *testing.T) {
	doctorEnv(t)
	oldLook := doctorLookPath
	doctorLookPath = func(string) (string, error) { return "", os.ErrNotExist }
	t.Cleanup(func() { doctorLookPath = oldLook })

	if code := exitCode(runChecks(testDoctorCfg(t))); code != 1 {
		t.Fatalf("exit code = %d, want 1 with no tools on PATH", code)
	}
}

func TestCmdDoctorUsage(t *testing.T) {
	if code := cmdDoctor([]string{"--bogus"}); code != 2 {
		t.Fatalf("cmdDoctor(--bogus) = %d, want 2", code)
	}
	if code := cmdDoctor([]string{"--json", "extra"}); code != 2 {
		t.Fatalf("cmdDoctor(--json extra) = %d, want 2", code)
	}
}

func TestDoctorJSONShape(t *testing.T) {
	doctorEnv(t)
	checks := runChecks(testDoctorCfg(t))

	b, err := json.Marshal(struct {
		Version string  `json:"version"`
		OK      bool    `json:"ok"`
		Checks  []check `json:"checks"`
	}{version, allOK(checks), checks})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["version"] != version {
		t.Errorf("version = %v, want %q", got["version"], version)
	}
	if got["ok"] != true {
		t.Errorf("ok = %v, want true", got["ok"])
	}
	arr, ok := got["checks"].([]any)
	if !ok || len(arr) == 0 {
		t.Fatalf("checks = %v, want a non-empty array", got["checks"])
	}
	first := arr[0].(map[string]any)
	for _, k := range []string{"name", "detail", "ok"} {
		if _, ok := first[k]; !ok {
			t.Errorf("check missing key %q: %v", k, first)
		}
	}
}

func TestTmuxVersionOK(t *testing.T) {
	cases := []struct {
		raw string
		ok  bool
	}{
		{"tmux 3.4", true},
		{"tmux 3.2", true},
		{"tmux 3.2a", true},
		{"tmux 4.0", true},
		{"tmux 3.5-rc1", true},
		{"tmux next-3.2", true}, // dev builds are newer than any release
		{"tmux master", true},   // unparseable: assume recent
		{"tmux 3.0", false},
		{"tmux 2.9", false},
	}
	for _, c := range cases {
		ok, _ := tmuxVersionOK(c.raw)
		if ok != c.ok {
			t.Errorf("tmuxVersionOK(%q) = %v, want %v", c.raw, ok, c.ok)
		}
	}
}

func TestParseVersion(t *testing.T) {
	cases := []struct {
		in           string
		major, minor int
		ok           bool
	}{
		{"3.4", 3, 4, true},
		{"3.2a", 3, 2, true},
		{"3.5-rc1", 3, 5, true},
		{"10.0", 10, 0, true},
		{"master", 0, 0, false},
		{"3", 0, 0, false},
		{"", 0, 0, false},
	}
	for _, c := range cases {
		major, minor, ok := parseVersion(c.in)
		if major != c.major || minor != c.minor || ok != c.ok {
			t.Errorf("parseVersion(%q) = %d,%d,%v, want %d,%d,%v", c.in, major, minor, ok, c.major, c.minor, c.ok)
		}
	}
}

func TestCheckBinary(t *testing.T) {
	oldVersion := version
	t.Cleanup(func() { version = oldVersion })

	// Dev builds skip the check (bootstrap never overwrites them).
	version = "dev"
	if c := checkBinary(config.Defaults()); !c.OK {
		t.Errorf("dev build check = %+v, want ok", c)
	}

	// Matching version passes.
	plugin := t.TempDir()
	if err := os.WriteFile(filepath.Join(plugin, "VERSION"), []byte("0.4.2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.BinDir = filepath.Join(plugin, "bin")
	version = "0.4.2"
	if c := checkBinary(cfg); !c.OK {
		t.Errorf("matching version = %+v, want ok", c)
	}

	// A stale binary fails.
	version = "0.4.1"
	if c := checkBinary(cfg); c.OK {
		t.Errorf("stale binary = %+v, want fail", c)
	}

	// Missing VERSION file fails.
	version = "0.4.2"
	cfg.BinDir = filepath.Join(t.TempDir(), "bin")
	if c := checkBinary(cfg); c.OK {
		t.Errorf("missing VERSION = %+v, want fail", c)
	}
}

func TestCheckStateDir(t *testing.T) {
	cfg := config.Defaults()

	// A writable temp dir passes.
	cfg.StateDir = filepath.Join(t.TempDir(), "state")
	if c := checkStateDir(cfg); !c.OK {
		t.Errorf("writable state dir = %+v, want ok", c)
	}

	// A path under a regular file can't be created → fails.
	f := filepath.Join(t.TempDir(), "notadir")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg.StateDir = filepath.Join(f, "state")
	if c := checkStateDir(cfg); c.OK {
		t.Errorf("uncreatable state dir = %+v, want fail", c)
	}
}

func TestCheckHooksFindings(t *testing.T) {
	oldHooks := doctorHookStatus
	t.Cleanup(func() { doctorHookStatus = oldHooks })

	// Every agent installed, hooks missing → all hook checks fail.
	doctorHookStatus = func(string) (bool, bool) { return true, false }
	checks := checkHooks()
	if len(checks) != len(hookTargets) {
		t.Fatalf("checkHooks() = %d checks, want %d", len(checks), len(hookTargets))
	}
	for _, c := range checks {
		if c.OK {
			t.Errorf("%s = %+v, want fail when hooks are missing", c.Name, c)
		}
	}

	// Hooks installed → all pass.
	doctorHookStatus = func(string) (bool, bool) { return true, true }
	for _, c := range checkHooks() {
		if !c.OK {
			t.Errorf("%s = %+v, want ok when hooks are installed", c.Name, c)
		}
	}

	// Agent absent → informational pass, never a failure.
	doctorHookStatus = func(string) (bool, bool) { return false, false }
	if !allOK(checkHooks()) {
		t.Error("absent agents should be informational, not failures")
	}
}

func TestCheckWorkerStates(t *testing.T) {
	cfg := testDoctorCfg(t)

	// Fresh install: no heartbeat, no usage.json → informational OK.
	if c := checkWorker(cfg); !c.OK {
		t.Errorf("fresh install worker = %+v, want ok", c)
	}

	// Disabled via the stop marker → OK.
	usageDir := filepath.Join(cfg.StateDir, "usage")
	if err := os.MkdirAll(usageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(worker.DisabledMarkerPath(cfg.StateDir), []byte("stopped\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := checkWorker(cfg); !c.OK || !strings.Contains(c.Detail, "disabled") {
		t.Errorf("disabled worker = %+v, want ok + disabled detail", c)
	}
	if err := os.Remove(worker.DisabledMarkerPath(cfg.StateDir)); err != nil {
		t.Fatal(err)
	}

	// A previously running worker crashed: usage.json exists but the
	// heartbeat went stale → FAIL (it auto-restarts on the next poll).
	if err := worker.SaveUsageFile(cfg.StateDir, worker.UsageFile{SchemaVersion: worker.SchemaVersion, GeneratedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-time.Hour).Unix()
	if err := os.WriteFile(worker.HeartbeatPath(cfg.StateDir), []byte(strconv.FormatInt(stale, 10)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := checkWorker(cfg); c.OK {
		t.Errorf("stale heartbeat + usage.json = %+v, want fail (crashed worker)", c)
	}

	// A fresh heartbeat → running.
	if err := worker.WriteHeartbeat(cfg.StateDir); err != nil {
		t.Fatal(err)
	}
	if c := checkWorker(cfg); !c.OK || !strings.Contains(c.Detail, "running") {
		t.Errorf("fresh heartbeat = %+v, want ok + running detail", c)
	}
}

func TestCheckUsageFile(t *testing.T) {
	cfg := testDoctorCfg(t)

	// Missing file → informational OK.
	if c := checkUsageFile(cfg); !c.OK || !strings.Contains(c.Detail, "not written") {
		t.Errorf("missing usage.json = %+v, want ok", c)
	}

	// A valid v1 file → OK with the version in the detail.
	if err := worker.SaveUsageFile(cfg.StateDir, worker.UsageFile{SchemaVersion: worker.SchemaVersion, GeneratedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if c := checkUsageFile(cfg); !c.OK || !strings.Contains(c.Detail, "v1") {
		t.Errorf("valid usage.json = %+v, want ok + v1 detail", c)
	}

	// A corrupt file → FAIL.
	if err := os.WriteFile(worker.UsageFilePath(cfg.StateDir), []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := checkUsageFile(cfg); c.OK {
		t.Errorf("corrupt usage.json = %+v, want fail", c)
	}

	// A wrong schema version → FAIL.
	if err := os.WriteFile(worker.UsageFilePath(cfg.StateDir), []byte(`{"schemaVersion":99}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := checkUsageFile(cfg); c.OK {
		t.Errorf("wrong schema version = %+v, want fail", c)
	}
}

func TestCheckQuota(t *testing.T) {
	cfg := testDoctorCfg(t)

	// No quota data yet → informational OK.
	if c := checkQuota(cfg); !c.OK {
		t.Errorf("empty quota = %+v, want ok", c)
	}

	// A populated quota block → OK with a per-key summary.
	if err := worker.SaveUsageFile(cfg.StateDir, worker.UsageFile{
		SchemaVersion: worker.SchemaVersion,
		GeneratedAt:   time.Now(),
		Quota: map[string]worker.Quota{
			"claude": {Pct: 38, Label: "Session (5-hour)"},
			"codex":  {Pct: 0, StatusText: "no credentials"},
			"fable":  {Pct: 0, Label: "Weekly (7-day)"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	c := checkQuota(cfg)
	if !c.OK {
		t.Errorf("quota check = %+v, want ok", c)
	}
	for _, want := range []string{"claude 38% (Session (5-hour))", "codex: no credentials", "fable 0% (Weekly (7-day))"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("quota detail %q missing %q", c.Detail, want)
		}
	}
}
