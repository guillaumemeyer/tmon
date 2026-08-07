package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/guillaumemeyer/tmon/internal/config"
	"github.com/guillaumemeyer/tmon/internal/connector"
	"github.com/guillaumemeyer/tmon/internal/detect"
	"github.com/guillaumemeyer/tmon/internal/tmux"
	"github.com/guillaumemeyer/tmon/internal/worker"
)

// check is one doctor finding: a name, what was found, and whether it's ok.
type check struct {
	Name   string `json:"name"`
	Detail string `json:"detail"`
	OK     bool   `json:"ok"`
}

// cmdDoctor runs the environment checks and prints a ✓/✗ report. Exit code
// is 0 when every check passes, 1 when anything fails — the --json form is
// for CI, where the exit code plus the machine-readable checks carry the
// result.
//
//	tmon doctor         text report
//	tmon doctor --json  JSON report (version, ok, checks)
func cmdDoctor(args []string) int {
	jsonOut := false
	if len(args) > 0 {
		if len(args) == 1 && args[0] == "--json" {
			jsonOut = true
		} else {
			fmt.Fprintln(os.Stderr, "usage: tmon doctor [--json]")
			return 2
		}
	}

	checks := runChecks(config.FromEnv())
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(struct {
			Version string  `json:"version"`
			OK      bool    `json:"ok"`
			Checks  []check `json:"checks"`
		}{version, allOK(checks), checks})
		return exitCode(checks)
	}

	printDoctor(checks)
	return exitCode(checks)
}

// doctor seams: the external-world probes. Tests override these to run the
// checks against a deterministic machine.
var (
	doctorLookPath    = exec.LookPath
	doctorTmuxVersion = func() (string, error) { return tmux.Run("-V") }
	doctorScanAgents  = detect.All
	// doctorHookStatus reports, for one hook target name, whether the agent
	// is installed on this machine and whether tmon's hooks are configured.
	doctorHookStatus = func(name string) (present, installed bool) {
		target, ok := hookTargets[name]
		if !ok {
			return false, false
		}
		installed, _ = hooksInstalled(target)
		return agentPresent(target), installed
	}
)

// runChecks performs every health check against the given config.
func runChecks(cfg config.Config) []check {
	out := []check{
		checkTmux(),
		checkTool("downloader", "curl", "wget"),
		checkTool("checksum", "sha256sum", "shasum"),
		checkGH(),
		checkBinary(cfg),
		checkStateDir(cfg),
		checkAgents(),
		checkConnectors(cfg),
		checkWorker(cfg),
		checkUsageFile(cfg),
		checkQuota(cfg),
	}
	out = append(out, checkHooks()...)
	return out
}

func checkTmux() check {
	v, err := doctorTmuxVersion()
	if err != nil {
		return check{Name: "tmux", Detail: "not found (tmon is a tmux plugin)", OK: false}
	}
	ok, detail := tmuxVersionOK(v)
	return check{Name: "tmux", Detail: detail, OK: ok}
}

// tmuxVersionOK reports whether `tmux -V` output describes a version at
// least tmon's requirement of 3.2. Unparseable outputs (dev builds like
// "next-3.2", "master") are assumed new enough rather than flagged.
func tmuxVersionOK(raw string) (bool, string) {
	raw = strings.TrimSpace(raw)
	ver := ""
	for _, f := range strings.Fields(raw) {
		if f != "tmux" {
			ver = f
			break
		}
	}
	ver = strings.TrimPrefix(ver, "next-")
	major, minor, ok := parseVersion(ver)
	if !ok {
		return true, raw + " (version unparseable; assuming recent)"
	}
	if major > 3 || (major == 3 && minor >= 2) {
		return true, raw + " (>= 3.2)"
	}
	return false, raw + " (< 3.2 required)"
}

// parseVersion extracts the leading major.minor from a version string
// ("3.4", "3.2a", "3.5-rc1").
func parseVersion(s string) (major, minor int, ok bool) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(s) || s[i] != '.' {
		return 0, 0, false
	}
	major, _ = strconv.Atoi(s[:i])
	j := i + 1
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	if j == i+1 {
		return 0, 0, false
	}
	minor, _ = strconv.Atoi(s[i+1 : j])
	return major, minor, true
}

// checkTool verifies that at least one of the named binaries is on PATH
// (curl or wget for downloads, sha256sum or shasum for checksums).
func checkTool(name string, tools ...string) check {
	for _, t := range tools {
		if p, err := doctorLookPath(t); err == nil && p != "" {
			return check{Name: name, Detail: t, OK: true}
		}
	}
	return check{Name: name, Detail: "none of " + strings.Join(tools, ", ") + " on PATH", OK: false}
}

// checkGH reports whether gh is on PATH. Informational: gh only powers the
// optional PR numbers in the dashboard, so its absence is a graceful
// degradation (the dashboard omits PR numbers), never a failure.
func checkGH() check {
	if p, err := doctorLookPath("gh"); err == nil && p != "" {
		return check{Name: "gh", Detail: "found (" + p + ") — dashboard PR numbers on", OK: true}
	}
	return check{Name: "gh", Detail: "not found — dashboard omits PR numbers (optional)", OK: true}
}

// checkBinary compares the running binary against the plugin's VERSION file
// (the same comparison scripts/bootstrap.sh makes). Dev builds skip the
// check, matching bootstrap's rule that a local build is never overwritten.
func checkBinary(cfg config.Config) check {
	if version == "dev" {
		return check{Name: "binary", Detail: "dev build (release version checks skipped)", OK: true}
	}
	versionFile := filepath.Join(filepath.Dir(cfg.BinDir), "VERSION")
	want, err := os.ReadFile(versionFile)
	if err != nil {
		return check{Name: "binary", Detail: "VERSION file missing at " + versionFile, OK: false}
	}
	wantV := strings.TrimSpace(string(want))
	if version == wantV {
		return check{Name: "binary", Detail: "v" + version + " matches VERSION", OK: true}
	}
	return check{
		Name:   "binary",
		Detail: fmt.Sprintf("installed v%s, repo wants v%s (reload tmux to re-bootstrap)", version, wantV),
		OK:     false,
	}
}

func checkStateDir(cfg config.Config) check {
	if err := os.MkdirAll(cfg.StateDir, 0o755); err != nil {
		return check{Name: "state dir", Detail: "cannot create " + cfg.StateDir + ": " + err.Error(), OK: false}
	}
	f, err := os.CreateTemp(cfg.StateDir, ".doctor-*")
	if err != nil {
		return check{Name: "state dir", Detail: "not writable: " + err.Error(), OK: false}
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return check{Name: "state dir", Detail: "writable (" + cfg.StateDir + ")", OK: true}
}

// checkAgents is informational: how many agents are running right now, and
// which. A scan failure is a real problem, so that fails the run.
func checkAgents() check {
	agents, err := doctorScanAgents()
	if err != nil {
		return check{Name: "agents", Detail: "scan failed: " + err.Error(), OK: false}
	}
	if len(agents) == 0 {
		return check{Name: "agents", Detail: "none running", OK: true}
	}
	labels := make([]string, 0, len(agents))
	for _, a := range agents {
		labels = append(labels, a.Label)
	}
	sort.Strings(labels)
	return check{Name: "agents", Detail: fmt.Sprintf("%d running: %s", len(labels), strings.Join(labels, ", ")), OK: true}
}

// checkConnectors is informational: which agents' authoritative state
// sources are live right now (state paths present and readable).
func checkConnectors(cfg config.Config) check {
	var names []string
	for _, c := range connector.Registry {
		if c.Enabled(cfg) {
			names = append(names, c.Name())
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return check{Name: "connectors", Detail: "none enabled (agent not installed, or no state yet)", OK: true}
	}
	return check{Name: "connectors", Detail: strings.Join(names, ", "), OK: true}
}

// checkWorker reports the usage worker state: running (fresh heartbeat),
// disabled (stop marker or TMON_WORKER=off), or absent. On a fresh install
// an absent worker is expected — the next status poll auto-starts it — so
// the check only fails when a previously running worker has gone missing
// (usage.json exists but the heartbeat went stale: a crash).
func checkWorker(cfg config.Config) check {
	if worker.Disabled(cfg.StateDir, cfg) {
		return check{Name: "worker", Detail: "disabled (TMON_WORKER=off or stop marker)", OK: true}
	}
	if hb, err := worker.ReadHeartbeat(cfg.StateDir); err == nil && time.Since(hb) < worker.HeartbeatStaleAfter {
		return check{Name: "worker", Detail: "running (heartbeat " + hb.Local().Format("15:04:05") + ")", OK: true}
	}
	if _, err := os.Stat(worker.UsageFilePath(cfg.StateDir)); err == nil {
		return check{Name: "worker", Detail: "not running (heartbeat stale — crashed worker auto-restarts on the next status poll)", OK: false}
	}
	return check{Name: "worker", Detail: "not running yet (auto-starts on the next status poll)", OK: true}
}

// checkUsageFile reports whether usage.json exists and parses.
func checkUsageFile(cfg config.Config) check {
	uf, err := worker.LoadUsageFile(cfg.StateDir)
	if err != nil {
		if os.IsNotExist(err) {
			return check{Name: "usage.json", Detail: "not written yet (first worker cycle pending)", OK: true}
		}
		return check{Name: "usage.json", Detail: err.Error(), OK: false}
	}
	return check{
		Name:   "usage.json",
		Detail: fmt.Sprintf("v%d, generated %s", uf.SchemaVersion, uf.GeneratedAt.Local().Format("2006-01-02 15:04:05")),
		OK:     true,
	}
}

// checkQuota reports the last quota probe results. Informational: absent
// quota is expected on machines without agent credentials.
func checkQuota(cfg config.Config) check {
	uf, err := worker.LoadUsageFile(cfg.StateDir)
	if err != nil || len(uf.Quota) == 0 {
		return check{Name: "quota", Detail: "no quota data yet (agent credentials not found?)", OK: true}
	}
	keys := make([]string, 0, len(uf.Quota))
	for k := range uf.Quota {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		q := uf.Quota[k]
		switch {
		case len(q.Windows) > 0:
			ws := make([]string, 0, len(q.Windows))
			for _, w := range q.Windows {
				ws = append(ws, fmt.Sprintf("%d%% %s", w.Pct, w.Label))
			}
			parts = append(parts, k+": "+strings.Join(ws, ", "))
		case q.Pct > 0 || q.Label != "":
			parts = append(parts, fmt.Sprintf("%s %d%% (%s)", k, q.Pct, q.Label))
		case q.StatusText != "":
			parts = append(parts, k+": "+q.StatusText)
		default:
			parts = append(parts, k+": n/a")
		}
	}
	return check{Name: "quota", Detail: strings.Join(parts, ", "), OK: true}
}

// checkHooks reports one finding per hook-tier agent. A running agent whose
// hooks are missing means tmon falls back to heuristics for it — worth a
// failing check with the one-command fix.
func checkHooks() []check {
	names := make([]string, 0, len(hookTargets))
	for n := range hookTargets {
		names = append(names, n)
	}
	sort.Strings(names)

	out := make([]check, 0, len(names))
	for _, name := range names {
		present, installed := doctorHookStatus(name)
		switch {
		case !present:
			out = append(out, check{Name: "hooks/" + name, Detail: "agent not installed", OK: true})
		case installed:
			out = append(out, check{Name: "hooks/" + name, Detail: "installed", OK: true})
		default:
			out = append(out, check{Name: "hooks/" + name, Detail: "agent running but hooks missing — run `tmon hooks auto`", OK: false})
		}
	}
	return out
}

func printDoctor(checks []check) {
	fmt.Printf("tmon doctor — v%s\n\n", version)
	for _, c := range checks {
		mark := "✓"
		if !c.OK {
			mark = "✗"
		}
		fmt.Printf("  %s %-14s %s\n", mark, c.Name, c.Detail)
	}
	fmt.Println()
	if fails := countFails(checks); fails == 0 {
		fmt.Println("All checks passed — tmon is ready to go.")
	} else {
		fmt.Printf("%d problem(s) found.\n", fails)
	}
}

func allOK(checks []check) bool { return countFails(checks) == 0 }

func countFails(checks []check) int {
	n := 0
	for _, c := range checks {
		if !c.OK {
			n++
		}
	}
	return n
}

func exitCode(checks []check) int {
	if allOK(checks) {
		return 0
	}
	return 1
}
