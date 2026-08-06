package worker

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/guillaumemeyer/tmon/internal/config"
)

var errProbe = errors.New("boom")

// stubWorkerVars replaces the loop seams with test values and restores them
// on cleanup. Tests in this package run sequentially, so mutating the
// package vars is safe.
func stubWorkerVars(t *testing.T) {
	t.Helper()
	origCycle, origIdle, origMin, origBusy, origProbes, origSpawn, origStale :=
		Cycle, IdleExitAfter, minCycleSleep, busy, probes, spawnDetached, HeartbeatStaleAfter
	t.Cleanup(func() {
		Cycle, IdleExitAfter, minCycleSleep, busy, probes, spawnDetached, HeartbeatStaleAfter =
			origCycle, origIdle, origMin, origBusy, origProbes, origSpawn, origStale
	})
}

// testProbe returns a probe factory that records its call count.
func countingProbe(count *int) Probe {
	return Probe{Key: "claude", Label: "Claude", Run: func(config.Config) (Quota, error) {
		*count++
		return Quota{Pct: 12, Label: "Session (5-hour)"}, nil
	}}
}

func TestRunWritesUsageAndHeartbeat(t *testing.T) {
	stubWorkerVars(t)
	Cycle, minCycleSleep = 20*time.Millisecond, 5*time.Millisecond
	IdleExitAfter = time.Hour
	busy = func() bool { return true } // never idle-exit; stop via SIGTERM
	probes = []Probe{countingProbe(new(int))}

	cfg := config.Defaults()
	cfg.StateDir = t.TempDir()

	done := make(chan error, 1)
	go func() { done <- Run(cfg) }()

	// Wait for the first cycle to persist usage.json. The heartbeat is
	// written at the start of the same cycle, so once usage.json exists the
	// signal handler is registered and SIGTERM is safe.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(UsageFilePath(cfg.StateDir)); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("usage.json not written within 5s")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not exit after SIGTERM")
	}

	uf, err := LoadUsageFile(cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	if uf.SchemaVersion != SchemaVersion {
		t.Errorf("schemaVersion = %d, want %d", uf.SchemaVersion, SchemaVersion)
	}
	if q := uf.Quota["claude"]; q.Pct != 12 {
		t.Errorf("claude quota = %+v, want 12%%", q)
	}
	if _, err := ReadHeartbeat(cfg.StateDir); err != nil {
		t.Errorf("heartbeat missing after run: %v", err)
	}
}

func TestRunIdleExit(t *testing.T) {
	stubWorkerVars(t)
	Cycle, minCycleSleep = 20*time.Millisecond, 5*time.Millisecond
	IdleExitAfter = 40 * time.Millisecond
	busy = func() bool { return false } // no agents, no dashboard
	probes = []Probe{countingProbe(new(int))}

	cfg := config.Defaults()
	cfg.StateDir = t.TempDir()

	start := time.Now()
	if err := Run(cfg); err != nil {
		t.Fatalf("Run = %v, want nil after idle exit", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("idle exit took %v, want well under 5s", elapsed)
	}
	if _, err := LoadUsageFile(cfg.StateDir); err != nil {
		t.Fatalf("usage.json missing after idle exit: %v", err)
	}
}

func TestRunReusesQuotaUntilTTL(t *testing.T) {
	stubWorkerVars(t)
	Cycle, minCycleSleep = 20*time.Millisecond, 5*time.Millisecond
	IdleExitAfter = 40 * time.Millisecond
	busy = func() bool { return false }
	calls := 0
	probes = []Probe{countingProbe(&calls)}

	cfg := config.Defaults()
	cfg.StateDir = t.TempDir()

	// The loop runs several cycles (each under QuotaTTL) before the idle
	// exit; the quota must be probed exactly once and reused afterwards.
	if err := Run(cfg); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("probe calls = %d, want 1 (reused until the TTL elapses)", calls)
	}
	uf, err := LoadUsageFile(cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	if q := uf.Quota["claude"]; q.Pct != 12 {
		t.Errorf("claude quota = %+v, want 12%%", q)
	}
}

func TestRunSecondInstanceExits(t *testing.T) {
	stubWorkerVars(t)
	cfg := config.Defaults()
	cfg.StateDir = t.TempDir()

	// Hold the pid lock as the first instance would.
	release, err := acquirePidLock(cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	if err := Run(cfg); err != nil {
		t.Fatalf("Run = %v, want nil (flock makes the second instance a no-op)", err)
	}
	if _, err := os.Stat(HeartbeatPath(cfg.StateDir)); err == nil {
		t.Fatal("second instance must not enter the loop or write a heartbeat")
	}
}

func TestDisabled(t *testing.T) {
	cfg := config.Defaults()
	cfg.StateDir = t.TempDir()
	cfg.WorkerEnabled = true

	if Disabled(cfg.StateDir, cfg) {
		t.Fatal("fresh state dir: worker should be enabled")
	}
	cfg.WorkerEnabled = false
	if !Disabled(cfg.StateDir, cfg) {
		t.Fatal("TMON_WORKER=off: worker should be disabled")
	}
	cfg.WorkerEnabled = true
	if err := os.MkdirAll(filepath.Join(cfg.StateDir, "usage"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(DisabledMarkerPath(cfg.StateDir), []byte("stopped\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !Disabled(cfg.StateDir, cfg) {
		t.Fatal("stop marker: worker should be disabled")
	}
}

func TestEnsureSpawned(t *testing.T) {
	stubWorkerVars(t)
	spawned := 0
	spawnDetached = func(stateDir string) error { spawned++; return nil }

	cfg := config.Defaults()
	cfg.StateDir = t.TempDir()

	// Fresh install, no heartbeat → spawn.
	EnsureSpawned(cfg)
	if spawned != 1 {
		t.Fatalf("spawns = %d, want 1 on a fresh state dir", spawned)
	}

	// Fresh heartbeat → the worker is alive, no spawn.
	if err := WriteHeartbeat(cfg.StateDir); err != nil {
		t.Fatal(err)
	}
	EnsureSpawned(cfg)
	if spawned != 1 {
		t.Fatalf("spawns = %d, want no spawn with a fresh heartbeat", spawned)
	}

	// Stale heartbeat → the worker died, spawn again.
	stale := time.Now().Add(-time.Hour).Unix()
	if err := os.WriteFile(HeartbeatPath(cfg.StateDir), []byte(strconv.FormatInt(stale, 10)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	EnsureSpawned(cfg)
	if spawned != 2 {
		t.Fatalf("spawns = %d, want a respawn after the heartbeat went stale", spawned)
	}

	// Disabled (stop marker or TMON_WORKER=off) suppresses the spawn.
	if err := os.WriteFile(DisabledMarkerPath(cfg.StateDir), []byte("stopped\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	EnsureSpawned(cfg)
	if spawned != 2 {
		t.Fatalf("spawns = %d, want no spawn while disabled", spawned)
	}
}

func TestStop(t *testing.T) {
	cfg := config.Defaults()
	cfg.StateDir = t.TempDir()

	// No pid file yet: the marker is still written and no error returned.
	if err := Stop(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(DisabledMarkerPath(cfg.StateDir)); err != nil {
		t.Fatalf("stop marker not written: %v", err)
	}

	// A garbage pid must not fail the stop.
	if err := os.WriteFile(PidPath(cfg.StateDir), []byte("not-a-pid\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Stop(cfg); err != nil {
		t.Fatalf("Stop with a garbage pid: %v", err)
	}

	// A live child process is terminated by its pid.
	sleep := exec.Command("sleep", "30")
	if err := sleep.Start(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(PidPath(cfg.StateDir), []byte(strconv.Itoa(sleep.Process.Pid)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Stop(cfg); err != nil {
		t.Fatalf("Stop with a live worker: %v", err)
	}
	if err := sleep.Wait(); err == nil {
		t.Fatal("worker process still alive after Stop")
	}
}

func TestRunQuotaProbesRecordsFailures(t *testing.T) {
	stubWorkerVars(t)
	probes = []Probe{
		{Key: "claude", Label: "Claude", Run: func(config.Config) (Quota, error) {
			return Quota{Pct: 10, Label: "Session (5-hour)"}, nil
		}},
		{Key: "codex", Label: "Codex", Run: func(config.Config) (Quota, error) {
			return Quota{}, errProbe
		}},
	}
	quota := runQuotaProbes(config.Defaults())
	if q := quota["claude"]; q.Pct != 10 {
		t.Errorf("claude = %+v, want 10%%", q)
	}
	if q := quota["codex"]; q.StatusText != "boom" {
		t.Errorf("codex = %+v, want the probe error as status text", q)
	}
}

func TestLazyQuotaTTLCache(t *testing.T) {
	stubWorkerVars(t)
	calls := 0
	probes = []Probe{countingProbe(&calls)}
	cfg := config.Defaults()
	cfg.StateDir = t.TempDir()

	if q := LazyQuota(cfg); q["claude"].Pct != 12 {
		t.Fatalf("first call quota = %+v, want 12%%", q)
	}
	if calls != 1 {
		t.Fatalf("probe calls = %d, want 1", calls)
	}
	if q := LazyQuota(cfg); q["claude"].Pct != 12 {
		t.Fatalf("second call quota = %+v, want the cached 12%%", q)
	}
	if calls != 1 {
		t.Fatalf("probe calls = %d, want 1 (TTL cache hit)", calls)
	}
}

func TestLazyQuotaReprobesAfterTTL(t *testing.T) {
	stubWorkerVars(t)
	calls := 0
	probes = []Probe{countingProbe(&calls)}
	cfg := config.Defaults()
	cfg.StateDir = t.TempDir()

	// A cache written 16 minutes ago (older than QuotaTTL) must be ignored.
	cache := map[string]any{
		"probedAt": time.Now().Add(-16 * time.Minute).Format(time.RFC3339),
		"quota":    map[string]Quota{"claude": {Pct: 99, Label: "Session (5-hour)"}},
	}
	b, err := json.Marshal(cache)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(cfg.StateDir, "usage")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lazyQuotaPath(cfg.StateDir), b, 0o644); err != nil {
		t.Fatal(err)
	}

	q := LazyQuota(cfg)
	if calls != 1 {
		t.Fatalf("probe calls = %d, want 1 after TTL expiry", calls)
	}
	if q["claude"].Pct != 12 {
		t.Fatalf("quota = %+v, want the fresh 12%%", q)
	}
}
