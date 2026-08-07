// prime.go — Prime Agent connector.
//
// Prime Agent (PrimeIntellect) runs a daemon topology: an interactive TUI
// client (the only process with a controlling tty), a supervisor daemon, a
// catalog, and one resident worker per session. All of them set
// process.title, so /proc cmdline reads "prime-agent" for every one of
// them; the controlling tty is the only discriminator. The connector reads
// the daemon's own session list (`prime-agent list --json`, TTL-gated: the
// spawn takes ~380 ms) plus each session's JSONL tail for token usage, and
// pairs sessions to the tty-owning client by cwd so pane teleport works.
// The daemon's activity field is the working signal; its taskState is only
// defined for idle sessions ("needs_input": turn finished, awaiting the
// next user message; "completed") and both map to idle — prime-agent
// exposes no native blocked signal.
package connector

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
	"github.com/guillaumemeyer/tmon/internal/detect"
	"github.com/guillaumemeyer/tmon/internal/proc"
)

// primeHome returns the Prime Agent config directory. The
// PRIME_AGENT_CODING_AGENT_DIR env var overrides the default ~/.prime/agent.
// A var so tests can point it at a fixture directory.
var primeHome = func() string {
	if d := os.Getenv("PRIME_AGENT_CODING_AGENT_DIR"); d != "" {
		return d
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(h, ".prime", "agent")
}

// primeSocketPath returns the daemon socket, mirroring prime-agent's
// defaultDaemonSocketPath(): join(tmpdir, "prime-agent-<uid>", "daemon.sock").
// A var so tests can point it at a fixture socket.
var primeSocketPath = func() string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("prime-agent-%d", os.Getuid()), "daemon.sock")
}

// primeListTTL gates the `prime-agent list --json` subprocess. The spawn
// costs ~380 ms (node startup + daemon RPC), so the list refreshes at most
// once per window; the session file reads between refreshes are free.
const primeListTTL = 15 * time.Second

// primeListTimeout caps one list spawn so a wedged daemon cannot stall the
// poll loop (which probes connectors in parallel).
const primeListTimeout = 5 * time.Second

// primeListMu guards the TTL cache below.
var (
	primeListMu    sync.Mutex
	primeListCache []primeSession
	primeListAt    time.Time
)

// primeOnPath reports whether the prime-agent CLI is on PATH. A var so
// tests can stub it.
var primeOnPath = func() bool {
	p, err := exec.LookPath("prime-agent")
	return err == nil && p != ""
}

// primeListJSON runs `prime-agent list --json` and returns the live
// top-level sessions. A var so tests can stub it without spawning node.
var primeListJSON = func() ([]primeSession, error) {
	ctx, cancel := context.WithTimeout(context.Background(), primeListTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "prime-agent", "list", "--json").Output()
	if err != nil {
		return nil, err
	}
	var pl primeList
	if err := json.Unmarshal(out, &pl); err != nil {
		return nil, err
	}
	return primeRows(pl), nil
}

// primeRows filters daemon-list rows down to the sessions tmon tracks:
// top-level sessions with a live worker. prime-agent marks a session
// "draft" until its first message is sent, so a just-opened (message-less)
// session would otherwise vanish from the connector and degrade to a
// heuristic row with "?" for usage and session; drafts stay rows and show
// the model, context window, and account quota instead. "Archived"
// (archived/crashed on-disk sessions) and RLM subagents are excluded;
// inactive on-disk sessions carry no workerPid and are dropped by the
// worker check.
func primeRows(pl primeList) []primeSession {
	var out []primeSession
	for _, s := range pl.Sessions {
		if s.Lifecycle == "archived" || s.RuntimeKind != "top-level" || s.WorkerPID <= 0 {
			continue
		}
		out = append(out, s)
	}
	return out
}

// Prime reads Prime Agent's live session state.
type Prime struct{}

func (Prime) Name() string { return "prime" }

// Enabled reports whether prime-agent is installed: the agent config
// directory exists, or the CLI is on PATH.
func (Prime) Enabled(cfg config.Config) bool {
	if d := primeHome(); d != "" {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			return true
		}
	}
	return primeOnPath()
}

// Probe returns one record per live top-level prime-agent session. Records
// come from the TTL-gated daemon list, enriched per poll from each
// session's JSONL (token usage) and from installed hook state
// (mid-turn tool detail and approval waits).
func (Prime) Probe(cfg config.Config) ([]Record, error) {
	sessions := primeListSessions()
	if len(sessions) == 0 {
		return nil, nil
	}
	now := time.Now()
	clients := primeClientsByCWD()
	hooks := loadPrimeHookState(cfg)
	recs := make([]Record, 0, len(sessions))
	for _, s := range sessions {
		rec := primeRecord(s, clients[s.CWD], hooks[s.SessionFile], now)
		recs = append(recs, rec)
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].PID < recs[j].PID })
	return recs, nil
}

// ─── daemon list (TTL-gated) ────────────────────────────────────────────────

// primeList is the shape of `prime-agent list --json`.
type primeList struct {
	Sessions []primeSession `json:"sessions"`
}

// primeSession is one daemon-list row; only the fields the connector needs
// are decoded.
type primeSession struct {
	ID             string     `json:"id"`
	SessionFile    string     `json:"sessionFile"`
	Lifecycle      string     `json:"lifecycle"`
	RuntimeKind    string     `json:"runtimeKind"`
	Activity       string     `json:"activity"`
	TaskState      string     `json:"taskState"`
	IsStreaming    bool       `json:"isStreaming"`
	IsBashRunning  bool       `json:"isBashRunning"`
	IsRunningTools bool       `json:"isRunningTools"`
	SessionName    string     `json:"sessionName"`
	FirstMessage   string     `json:"firstMessage"`
	CWD            string     `json:"cwd"`
	WorkerPID      int        `json:"workerPid"`
	LastActivityAt string     `json:"lastActivityAt"`
	Model          primeModel `json:"model"`
}

// primeModel is the subset of the daemon's model object used for the model
// label and the context window.
type primeModel struct {
	ID            string `json:"id"`
	Provider      string `json:"provider"`
	ContextWindow int64  `json:"contextWindow"`
}

// primeListSessions returns the current live session list, TTL-gated. The
// daemon socket is checked before every spawn so a stopped daemon never
// triggers a CLI spawn (which could start a new daemon, and costs ~380 ms
// of node startup per attempt). Failed spawns and missing sockets are
// cached for the TTL window too, so a down daemon cannot cause a spawn per
// poll.
func primeListSessions() []primeSession {
	primeListMu.Lock()
	defer primeListMu.Unlock()
	if time.Since(primeListAt) < primeListTTL {
		return primeListCache
	}
	if _, err := os.Stat(primeSocketPath()); err != nil {
		primeListCache = nil
		primeListAt = time.Now()
		return nil
	}
	sessions, err := primeListJSON()
	if err != nil {
		primeListCache = nil
		primeListAt = time.Now()
		return nil
	}
	primeListCache = sessions
	primeListAt = time.Now()
	return sessions
}

// ─── record mapping ─────────────────────────────────────────────────────────

// primeClientsByCWD maps each live tty-owning prime-agent client's full
// cwd to its PID. The client is the only process in the topology with a
// controlling tty, so pane teleport points at it. Headless/detached
// sessions (client closed, worker still running) have no entry and fall
// back to the worker PID with pane "?".
func primeClientsByCWD() map[string]int {
	pids, err := primeListPIDs()
	if err != nil {
		return nil
	}
	byCWD := make(map[string]int)
	for _, pid := range pids {
		cmdline, err := primeReadCmdline(pid)
		if err != nil || cmdline == "" {
			continue
		}
		if detect.MatchLabel(cmdline) != "Prime" {
			continue
		}
		if primeTTYForPID(pid) == "" {
			continue // supervisor, catalog or worker: not a client session
		}
		cwd, err := primeReadCWD(pid)
		if err != nil || cwd == "" {
			continue
		}
		byCWD[cwd] = pid
	}
	return byCWD
}

// primeRecord builds one record from a daemon-list row. clientPID is the
// tty-owning client for the session's cwd, or 0 when detached; the worker
// PID is the fallback so headless sessions stay tracked.
func primeRecord(s primeSession, clientPID int, hook primeHookState, now time.Time) Record {
	pid := clientPID
	if pid == 0 {
		pid = s.WorkerPID
	}

	status, detail, at := primeNativeStatus(s, now)
	if hook.valid {
		switch hook.status {
		case agent.StatusBlocked:
			status, detail, at = agent.StatusBlocked, hook.detail, now
		case agent.StatusWorking:
			status, detail, at = agent.StatusWorking, hook.detail, now
		case agent.StatusIdle:
			// turn_end fired: the hook event is fresher than the
			// TTL-cached daemon list. Override a stale "working" and
			// replace the daemon's needs:input fallback detail.
			if status == agent.StatusWorking || detail == "needs:input" {
				status, detail, at = agent.StatusIdle, "turn-complete", now
			}
		}
	}
	if detail == "" {
		switch status {
		case agent.StatusBlocked:
			detail = "waiting"
		case agent.StatusWorking:
			detail = "active"
		default:
			detail = "idle"
		}
	}
	if m := primeModelLabel(s.Model); m != "" {
		detail += " · model:" + m
	}

	rec := Record{
		PID:    pid,
		Label:  "Prime",
		Status: status,
		Detail: detail,
		At:     at,
	}
	if s.CWD != "" {
		rec.CWD = proc.CWDShort(s.CWD)
	}
	if s.SessionName != "" {
		rec.Title = s.SessionName
	} else if s.FirstMessage != "" {
		rec.Title = truncatePrime(s.FirstMessage)
	}
	if u := primeUsage(s.SessionFile, s.Model.ContextWindow); !u.Empty() {
		rec.Usage = u
	} else if s.Model.ContextWindow > 0 {
		// No usage recorded yet (fresh session): still report the window so
		// the dashboard shows an empty bar instead of "context: ?".
		rec.Usage = agent.Usage{WindowTokens: s.Model.ContextWindow}
	}
	return rec
}

// primeNativeStatus maps the daemon's own signals to the tmon status
// machine. The daemon's activity field is the working signal; taskState is
// only computed for idle sessions and distinguishes "needs_input" (turn
// finished, awaiting the next user message) from "completed" — both are
// idle. prime-agent exposes no native blocked signal. A draft — a
// message-less session prime-agent keeps resident with a worker before its
// first message — reads as idle: the daemon reports activity "working" for
// it, but that is only the no-verdict placeholder until an idle verdict
// lands, and nothing has actually happened in the session.
func primeNativeStatus(s primeSession, now time.Time) (agent.Status, string, time.Time) {
	at := now
	if t, err := time.Parse(time.RFC3339Nano, s.LastActivityAt); err == nil {
		at = t
	}
	if s.Lifecycle == "draft" {
		return agent.StatusIdle, "draft", at
	}
	var detail string
	switch {
	case s.IsStreaming:
		detail = "streaming"
	case s.IsBashRunning:
		detail = "tool:bash"
	case s.IsRunningTools:
		detail = "tool:running"
	}
	switch {
	case s.Activity == "working":
		if detail == "" {
			detail = "active"
		}
		return agent.StatusWorking, detail, at
	case s.TaskState == "needs_input":
		return agent.StatusIdle, "needs:input", at
	default:
		return agent.StatusIdle, "turn-complete", at
	}
}

// primeModelLabel renders the detail suffix, e.g. "deepseek/deepseek-v4-pro".
func primeModelLabel(m primeModel) string {
	if m.ID == "" {
		return ""
	}
	if m.Provider != "" {
		return m.Provider + "/" + m.ID
	}
	return m.ID
}

// ─── usage (session JSONL) ──────────────────────────────────────────────────

// primeEventTailBytes bounds how much of a session JSONL is read per poll
// when hunting the last assistant message's usage. Prime Agent writes an
// agent_status heartbeat line roughly every 25 s even while idle, so the
// file grows continuously between turns; a Grok-sized window (128 KB)
// would silently lose the usage record after a few hours of idle. One
// megabyte keeps the last assistant message visible through ~30 h of idle
// heartbeat growth while bounding the per-poll read for active sessions.
const primeEventTailBytes = 1 * 1024 * 1024

// primeMsgEvent is one line of a session JSONL; only message events with
// assistant usage are decoded.
type primeMsgEvent struct {
	Type    string `json:"type"`
	Message struct {
		Role  string         `json:"role"`
		Usage *primeMsgUsage `json:"usage"`
	} `json:"message"`
}

type primeMsgUsage struct {
	Input     int64 `json:"input"`
	CacheRead int64 `json:"cacheRead"`
}

// primeUsage estimates the session's current context occupancy: the last
// assistant message's input + cacheRead tokens from the JSONL tail
// (Hermes-style estimate), with the window straight from the daemon list.
func primeUsage(sessionFile string, window int64) agent.Usage {
	used := primeLastAssistantTokens(sessionFile)
	if used <= 0 {
		return agent.Usage{}
	}
	u := agent.Usage{TokensUsed: used}
	if window > 0 {
		u.WindowTokens = window
	}
	return u
}

// primeLastAssistantTokens returns input + cacheRead of the newest
// assistant message in the session JSONL tail, or 0 when the file is
// missing or carries no usage yet.
func primeLastAssistantTokens(sessionFile string) int64 {
	var used int64
	for _, l := range tailEvents(sessionFile, primeEventTailBytes) {
		var e primeMsgEvent
		if json.Unmarshal([]byte(l), &e) != nil || e.Type != "message" {
			continue // first (truncated) line in the window or noise
		}
		if e.Message.Role != "assistant" || e.Message.Usage == nil {
			continue
		}
		used = e.Message.Usage.Input + e.Message.Usage.CacheRead
	}
	return used
}

// ─── hook state join ────────────────────────────────────────────────────────

// primeHookState is one state file written by the tmon prime extension.
type primeHookState struct {
	status agent.Status
	detail string
	valid  bool
}

// primeHookFile is the on-disk shape; the sessionFile field is how the
// connector joins hook state to daemon-list rows.
type primeHookFile struct {
	Status      string `json:"status"`
	Detail      string `json:"detail"`
	SessionFile string `json:"sessionFile"`
}

// loadPrimeHookState loads every hook state file under <state>/hooks/prime/,
// indexed by sessionFile. Missing dir (extension not installed) is empty.
func loadPrimeHookState(cfg config.Config) map[string]primeHookState {
	dir := filepath.Join(cfg.HookStateDir, "prime")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make(map[string]primeHookState)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var f primeHookFile
		if json.Unmarshal(b, &f) != nil || f.SessionFile == "" {
			continue
		}
		status := agent.Status(f.Status)
		switch status {
		case agent.StatusBlocked, agent.StatusWorking, agent.StatusIdle:
		default:
			continue
		}
		out[f.SessionFile] = primeHookState{status: status, detail: f.Detail, valid: true}
	}
	return out
}

// ─── process scan seams ─────────────────────────────────────────────────────

var (
	primeListPIDs    = proc.ListPIDs
	primeReadCmdline = proc.ReadCmdline
	primeReadCWD     = proc.ReadCWD
	primeTTYForPID   = proc.TTYForPID
)

// truncatePrime shortens a session title fallback to a displayable length.
func truncatePrime(s string) string {
	const max = 80
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}
