package worker

import (
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/guillaumemeyer/tmon/internal/config"
	"github.com/guillaumemeyer/tmon/internal/detect"
	"github.com/guillaumemeyer/tmon/internal/proc"
)

// Cycle is the worker loop period: heartbeat and (Phase 2) ledger scans run
// once per cycle; quota probes at most once per QuotaTTL. A var so tests
// can shorten it.
var Cycle = 60 * time.Second

// IdleExitAfter is how long the worker stays up with no live agents and no
// open dashboard before exiting, to save battery on laptops. A var so tests
// can shorten it.
var IdleExitAfter = 30 * time.Minute

// minCycleSleep floors the inter-cycle sleep so an overrun cycle cannot
// busy-loop. A var so tests can shorten it alongside Cycle.
var minCycleSleep = time.Second

// DisabledMarkerPath is the stop marker: `tmon worker stop` writes it to
// suppress auto-respawn until it is removed or the worker is started
// manually (`tmon worker` / `tmon daemon`).
func DisabledMarkerPath(stateDir string) string {
	return filepath.Join(stateDir, "usage", "worker.disabled")
}

// PidPath is the flocked pid file (`tmon worker stop` reads the pid here).
func PidPath(stateDir string) string {
	return filepath.Join(stateDir, "usage", "worker.pid")
}

// LogPath is where the auto-spawned worker's stderr goes.
func LogPath(stateDir string) string {
	return filepath.Join(stateDir, "usage", "worker.log")
}

// Disabled reports whether the worker should not run: the stop marker
// exists or TMON_WORKER disables it.
func Disabled(stateDir string, cfg config.Config) bool {
	if !cfg.WorkerEnabled {
		return true
	}
	_, err := os.Stat(DisabledMarkerPath(stateDir))
	return err == nil
}

// Run executes the worker loop in the foreground until SIGTERM/SIGINT or
// the idle timeout. It returns nil when another worker already holds the
// pid lock (the second instance exits silently — the flock is the spawn
// race guard).
func Run(cfg config.Config) error {
	release, err := acquirePidLock(cfg.StateDir)
	if err != nil {
		if err == errAlreadyRunning {
			return nil
		}
		return err
	}
	defer release()
	// A manual run re-enables auto-respawn.
	_ = os.Remove(DisabledMarkerPath(cfg.StateDir))

	logger := log.New(os.Stderr, "tmon worker: ", log.LstdFlags)
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	lastQuota := time.Time{}
	idleSince := time.Time{}
	for {
		start := time.Now()

		// Heartbeat first: a poll respawning logic only needs to see it
		// before the slow quota probes finish.
		if err := WriteHeartbeat(cfg.StateDir); err != nil {
			logger.Printf("heartbeat: %v", err)
		}

		uf := UsageFile{SchemaVersion: SchemaVersion, GeneratedAt: start, DeviceID: deviceID()}
		if start.Sub(lastQuota) >= QuotaTTL {
			uf.Quota = runQuotaProbes(cfg)
			lastQuota = start
		} else if prev, err := LoadUsageFile(cfg.StateDir); err == nil {
			uf.Quota = prev.Quota // reuse the last probe until the TTL elapses
		}
		if err := SaveUsageFile(cfg.StateDir, uf); err != nil {
			logger.Printf("save usage.json: %v", err)
		}

		// Idle exit: no live agents and no open dashboard for the whole
		// window.
		if busy() {
			idleSince = time.Time{}
		} else if idleSince.IsZero() {
			idleSince = start
		} else if start.Sub(idleSince) >= IdleExitAfter {
			logger.Printf("idle for %s, exiting", IdleExitAfter)
			return nil
		}

		d := time.Until(start.Add(Cycle))
		if d < minCycleSleep {
			d = minCycleSleep // never busy-loop on an overrun cycle
		}
		select {
		case <-quit:
			return nil
		case <-time.After(d):
		}
	}
}

func deviceID() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

// busy reports whether anything is using tmon right now: a live agent, or
// the dashboard popup. A var so tests can inject it.
var busy = func() bool {
	if agents, err := detect.All(); err == nil && len(agents) > 0 {
		return true
	}
	return dashboardOpen()
}

// dashboardOpen reports whether the dashboard popup process is running. A
// var so tests can inject it.
var dashboardOpen = func() bool {
	pids, err := proc.ListPIDs()
	if err != nil {
		return true // unknown state: keep the worker alive
	}
	for _, pid := range pids {
		cmdline, err := proc.ReadCmdline(pid)
		if err != nil {
			continue
		}
		if strings.Contains(cmdline, "tmon") && strings.Contains(cmdline, "dashboard") {
			return true
		}
	}
	return false
}

// EnsureSpawned starts the detached worker when it is enabled and its
// heartbeat is missing or stale. The spawn is one fork+exec (well under the
// poll budget); the flock makes a duplicate spawn exit immediately.
func EnsureSpawned(cfg config.Config) {
	if Disabled(cfg.StateDir, cfg) {
		return
	}
	if HeartbeatFresh(cfg.StateDir) {
		return
	}
	if err := spawnDetached(cfg.StateDir); err != nil {
		log.Printf("tmon: spawn worker: %v", err)
	}
}

// Stop terminates the running worker and disables auto-respawn until the
// marker is removed or the worker is started manually.
func Stop(cfg config.Config) error {
	dir := filepath.Join(cfg.StateDir, "usage")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	marker := DisabledMarkerPath(cfg.StateDir)
	if err := os.WriteFile(marker, []byte("stopped "+time.Now().Format(time.RFC3339)+"\n"), 0o644); err != nil {
		return err
	}
	b, err := os.ReadFile(PidPath(cfg.StateDir))
	if err != nil {
		return nil // no worker to stop
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return nil
	}
	return syscall.Kill(pid, syscall.SIGTERM)
}
