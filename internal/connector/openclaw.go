// openclaw.go — OpenClaw connector (gateway tier).
//
// OpenClaw is a long-running multi-channel Gateway, similar to Hermes. The
// connector reads the Gateway ownership lock under $TMPDIR/openclaw-<uid>/
// for the process PID, then classifies activity:
//
//  1. Prefer a TTL-cached `openclaw sessions --all-agents --active N --json`
//     and count sessions with status "running" (or all --active rows when
//     status is absent on older CLIs).
//  2. If the CLI is missing or fails, fall back to the newest mtime among
//     ~/.openclaw/agents/*/agent/openclaw-agent.sqlite files.
//
// The Gateway often has no tmux pane; poll injects the record as a
// connector-only agent when /proc signatures miss the (retitled)
// openclaw-gateway process. Interactive openclaw agent/chat/run panes keep
// the CPU/IO heuristic path unless they share the Gateway PID.
package connector

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
)

// openclawHome returns the OpenClaw state directory (~/.openclaw). A var so
// tests can point it at a fixture directory.
var openclawHome = func() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(h, ".openclaw")
}

// openclawLockDir returns the Gateway lock directory
// ($TMPDIR/openclaw-<uid>). A var so tests can inject a fixture.
var openclawLockDir = func() string {
	base := os.TempDir()
	// process.Getuid is Unix-only in spirit; on platforms without it Go
	// still exposes Getuid as -1 on Windows — match OpenClaw's "openclaw"
	// suffix when the uid is unavailable.
	if uid := os.Getuid(); uid >= 0 {
		return filepath.Join(base, fmt.Sprintf("openclaw-%d", uid))
	}
	return filepath.Join(base, "openclaw")
}

// OpenClaw reads the OpenClaw Gateway lock and session activity.
type OpenClaw struct{}

func (OpenClaw) Name() string { return "openclaw" }

// Enabled reports whether OpenClaw state or a Gateway lock dir exists.
func (OpenClaw) Enabled(cfg config.Config) bool {
	if dirExists(openclawHome()) {
		return true
	}
	return dirExists(openclawLockDir())
}

// openclawLock is the subset of the Gateway lock payload the connector uses.
type openclawLock struct {
	PID       int    `json:"pid"`
	CreatedAt string `json:"createdAt"`
	Port      int    `json:"port"`
	Role      string `json:"role"`
}

// openclawSessionsJSON is the subset of `openclaw sessions --json` used for
// activity classification.
type openclawSessionsJSON struct {
	Sessions []openclawSessionRow `json:"sessions"`
}

type openclawSessionRow struct {
	Status string `json:"status"`
}

// openclawSessionsTTL bounds how often the sessions CLI runs.
const openclawSessionsTTL = 20 * time.Second

// openclawSessionsOutput runs the sessions list CLI. A seam so tests inject
// fixtures instead of spawning openclaw.
var openclawSessionsOutput = func(cfg config.Config, activeMinutes int) (string, error) {
	bin, err := exec.LookPath("openclaw")
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin,
		"sessions", "--all-agents",
		"--active", fmt.Sprintf("%d", activeMinutes),
		"--json",
	).Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// Probe returns one record for the Gateway process, or none when no live
// lock is found.
func (OpenClaw) Probe(cfg config.Config) ([]Record, error) {
	pid, ok := readOpenClawGatewayPID(openclawLockDir())
	if !ok {
		return nil, nil
	}

	rec := Record{
		PID:    pid,
		Label:  "OpenClaw",
		Status: agent.StatusIdle,
		Detail: "gateway",
		// Long-lived daemon: stamp "now" so ConnectorFreshness never decays
		// a live Gateway back to the heuristic path solely because the lock
		// file is static.
		At: time.Now(),
	}

	if n, source, ok := openclawActiveCount(cfg); ok && n > 0 {
		rec.Status = agent.StatusWorking
		switch source {
		case "sessions":
			rec.Detail = fmt.Sprintf("%d active sessions", n)
		default:
			rec.Detail = "session activity"
		}
	}
	return []Record{rec}, nil
}

// readOpenClawGatewayPID scans the lock directory for a live Gateway lock
// and returns its PID. Config locks (gateway.<hash>.lock) are preferred
// over state locks (gateway.state.<hash>.lock). Non-gateway roles are skipped.
func readOpenClawGatewayPID(lockDir string) (int, bool) {
	entries, err := os.ReadDir(lockDir)
	if err != nil {
		return 0, false
	}

	var (
		configPID int
		statePID  int
		scanned   int
	)
	const maxLocks = 32
	for _, e := range entries {
		if e.IsDir() || scanned >= maxLocks {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "gateway") || !strings.HasSuffix(name, ".lock") {
			continue
		}
		scanned++
		b, err := os.ReadFile(filepath.Join(lockDir, name))
		if err != nil {
			continue
		}
		var lock openclawLock
		if json.Unmarshal(b, &lock) != nil || lock.PID <= 0 {
			continue
		}
		if lock.Role != "" && lock.Role != "gateway" {
			continue
		}
		if strings.HasPrefix(name, "gateway.state.") {
			if statePID == 0 {
				statePID = lock.PID
			}
			continue
		}
		// Prefer the first config-style lock: gateway.<hash>.lock
		if configPID == 0 {
			configPID = lock.PID
		}
	}
	if configPID > 0 {
		return configPID, true
	}
	if statePID > 0 {
		return statePID, true
	}
	return 0, false
}

// openclawActiveCount returns how many sessions look active and a source
// tag ("sessions" or "mtime"). ok is false when activity cannot be
// determined (caller keeps idle).
func openclawActiveCount(cfg config.Config) (n int, source string, ok bool) {
	if n, ok := openclawRunningFromCLI(cfg); ok {
		return n, "sessions", true
	}
	if openclawRecentSessionFiles(cfg) {
		return 1, "mtime", true
	}
	return 0, "", false
}

// openclawRunningFromCLI TTL-caches the sessions list and counts running
// sessions. ok is false on CLI/cache failure.
func openclawRunningFromCLI(cfg config.Config) (int, bool) {
	minutes := int(cfg.ConnectorFreshness.Round(time.Minute) / time.Minute)
	if minutes < 1 {
		minutes = 1
	}
	// When freshness is under a minute (default 30s), Round can yield 0;
	// use ceil-to-minute so --active still has a positive window.
	if cfg.ConnectorFreshness > 0 && cfg.ConnectorFreshness < time.Minute {
		minutes = 1
	}

	out, err := runCachedTTL(cfg.StateDir, "openclaw-sessions", openclawSessionsTTL, func() (string, error) {
		return openclawSessionsOutput(cfg, minutes)
	})
	if err != nil {
		return 0, false
	}
	n, ok := parseOpenClawActiveSessions(out)
	return n, ok
}

// parseOpenClawActiveSessions counts active sessions in sessions --json
// output. ok is false when the payload is unparseable.
//
// Rules:
//   - Prefer status == "running".
//   - If any row has a non-empty status, only those "running" count.
//   - If no row has a status field (older CLI), every listed session
//     counts — the caller already filtered with --active.
func parseOpenClawActiveSessions(out string) (int, bool) {
	var payload openclawSessionsJSON
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		return 0, false
	}
	if payload.Sessions == nil {
		// Distinguish empty list (valid, 0 active) from missing key by
		// requiring a successful unmarshal of an object; zero sessions is ok.
		return 0, true
	}

	hasStatus := false
	running := 0
	for _, s := range payload.Sessions {
		if s.Status != "" {
			hasStatus = true
		}
		if s.Status == "running" {
			running++
		}
	}
	if hasStatus {
		return running, true
	}
	return len(payload.Sessions), true
}

// openclawRecentSessionFiles reports whether any per-agent SQLite store
// under ~/.openclaw/agents was touched within the freshness window.
func openclawRecentSessionFiles(cfg config.Config) bool {
	agents := filepath.Join(openclawHome(), "agents")
	if !dirExists(agents) {
		return false
	}
	cutoff := time.Now().Add(-cfg.ConnectorFreshness)
	if cfg.ConnectorFreshness <= 0 {
		cutoff = time.Now().Add(-30 * time.Second)
	}

	entries, err := os.ReadDir(agents)
	if err != nil {
		return false
	}
	const maxAgents = 32
	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		n++
		if n > maxAgents {
			break
		}
		// openclaw-agent.sqlite and its WAL share activity signal.
		base := filepath.Join(agents, e.Name(), "agent")
		for _, name := range []string{"openclaw-agent.sqlite", "openclaw-agent.sqlite-wal"} {
			fi, err := os.Stat(filepath.Join(base, name))
			if err != nil {
				continue
			}
			if fi.ModTime().After(cutoff) {
				return true
			}
		}
	}
	return false
}
