// codex.go — Codex CLI connector.
//
// Codex >= 0.14x writes one "rollout" JSONL per session under
// ~/.codex/sessions/YYYY/MM/DD/rollout-<ts>-<session-id>.jsonl — a
// readable live-state surface, no installation needed. Each file opens
// with a session_meta event carrying the session's working directory; the
// tail carries task_started / message / token_count / task_complete
// events. tmon reads these files natively for authoritative status and
// token usage.
//
// Installed lifecycle hooks (~/.codex/hooks.json, see `tmon hooks install
// codex`) remain an override: hook events expose permission waits and
// running tools the rollout does not record, so hook status wins when both
// sources are present; the rollout still supplies the token usage.
package connector

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
	"github.com/guillaumemeyer/tmon/internal/proc"
)

// Codex reads Codex rollout session files (and hook state when installed).
type Codex struct{}

func (Codex) Name() string { return "codex" }

// Enabled reports whether Codex state is readable: its rollout session
// directory exists, or hook state has been written (older setups).
func (Codex) Enabled(cfg config.Config) bool {
	if hookDirExists(cfg, "codex") {
		return true
	}
	home := codexHome()
	if home == "" {
		return false
	}
	fi, err := os.Stat(filepath.Join(home, "sessions"))
	return err == nil && fi.IsDir()
}

// Probe returns one record per live Codex session: status and detail from
// the session's rollout file (overridden by hook state when installed),
// token usage from the latest token_count event.
func (Codex) Probe(cfg config.Config) ([]Record, error) {
	recs := codexRolloutRecords(cfg)
	if !hookDirExists(cfg, "codex") {
		return recs, nil
	}
	hookRecs, err := pairHookSessions(cfg, "codex", "Codex")
	if err != nil {
		return recs, nil
	}
	return mergeCodexRecords(recs, hookRecs), nil
}

// codexHome returns the Codex config directory (~/.codex). A var so tests
// can point it at a fixture directory.
var codexHome = func() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(h, ".codex")
}

// ─── rollout session files ───────────────────────────────────────────────

// codexRolloutFreshness is how many days back a rollout file may be for its
// session to count as live. Codex names session dirs YYYY/MM/DD; older
// rollouts are treated as closed and their process, if still running, falls
// back to the heuristic path.
const codexRolloutFreshness = 30

// codexRolloutTailBytes bounds how much of a session's rollout is read per
// poll. The file grows to megabytes; the status and usage signals are at
// the tail.
const codexRolloutTailBytes = 256 * 1024

// codexRolloutRecords returns one record per running Codex process whose
// rollout session can be located by working directory.
func codexRolloutRecords(cfg config.Config) []Record {
	home := codexHome()
	if home == "" {
		return nil
	}
	byCWD := runningByCWD("Codex")
	if len(byCWD) == 0 {
		return nil
	}
	sessionsDir := filepath.Join(home, "sessions")
	recs := make([]Record, 0, len(byCWD))
	for cwd, pid := range byCWD {
		if path := newestCodexRollout(sessionsDir, cwd); path != "" {
			if rec, ok := codexRolloutRecord(pid, cwd, path); ok {
				recs = append(recs, rec)
			}
		}
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].PID < recs[j].PID })
	return recs
}

// newestCodexRollout returns the most recently modified rollout file whose
// session working directory matches cwd (short form), or "".
func newestCodexRollout(sessionsDir, cwd string) string {
	if sessionsDir == "" || cwd == "" {
		return ""
	}
	var best string
	var bestMod int64 = -1
	for _, path := range codexRolloutCandidates(sessionsDir, time.Now()) {
		if codexRolloutCWD(path) != cwd {
			continue
		}
		fi, err := os.Stat(path)
		if err != nil {
			continue
		}
		if fi.ModTime().UnixNano() > bestMod {
			bestMod = fi.ModTime().UnixNano()
			best = path
		}
	}
	return best
}

// codexRolloutCandidates lists rollout files under sessionsDir for the last
// codexRolloutFreshness days (Codex names session dirs YYYY/MM/DD).
func codexRolloutCandidates(sessionsDir string, now time.Time) []string {
	var out []string
	for i := 0; i < codexRolloutFreshness; i++ {
		d := now.AddDate(0, 0, -i)
		matches, _ := filepath.Glob(filepath.Join(
			sessionsDir, d.Format("2006"), d.Format("01"), d.Format("02"), "rollout-*.jsonl"))
		out = append(out, matches...)
	}
	return out
}

// codexRolloutCWD returns the session's working directory (short form) from
// a rollout file's session_meta event, or "" when the file does not open
// with one (unreadable, truncated, or not a Codex rollout).
func codexRolloutCWD(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 256*1024), 16*1024*1024)
	for i := 0; i < 8 && sc.Scan(); i++ {
		var meta struct {
			Type    string `json:"type"`
			Payload struct {
				CWD string `json:"cwd"`
			} `json:"payload"`
		}
		if json.Unmarshal(sc.Bytes(), &meta) != nil || meta.Type != "session_meta" || meta.Payload.CWD == "" {
			continue
		}
		return proc.CWDShort(meta.Payload.CWD)
	}
	return ""
}

// codexRolloutEvent is one line of a rollout file: the top-level type
// (session_meta, event_msg, response_item, ...) plus the inner event type
// and the fields the connector needs.
type codexRolloutEvent struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   struct {
		Type  string `json:"type"` // inner type for event_msg / response_item
		Phase string `json:"phase"`
		Info  *struct {
			LastTokenUsage *codexTokenUsage `json:"last_token_usage"`
			ContextWindow  int64            `json:"model_context_window"`
		} `json:"info"`
	} `json:"payload"`
}

// codexTokenUsage is the token split of one token_count event.
type codexTokenUsage struct {
	InputTokens     int64 `json:"input_tokens"`
	CachedInput     int64 `json:"cached_input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	ReasoningOutput int64 `json:"reasoning_output_tokens"`
}

// codexRolloutEvents returns the parsed lines from the tail of a rollout
// file, in file order. The first line in the window may be partial and is
// dropped by the JSON parse.
func codexRolloutEvents(path string) []codexRolloutEvent {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil
	}
	start := fi.Size() - codexRolloutTailBytes
	if start < 0 {
		start = 0
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return nil
	}
	var out []codexRolloutEvent
	for _, line := range strings.Split(string(b), "\n") {
		var ev codexRolloutEvent
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		out = append(out, ev)
	}
	return out
}

// codexRolloutRecord builds one record from a rollout file: status and
// detail from the last event, token usage from the latest token_count.
func codexRolloutRecord(pid int, cwd, path string) (Record, bool) {
	evs := codexRolloutEvents(path)
	if len(evs) == 0 {
		return Record{}, false
	}
	last := evs[len(evs)-1]
	rec := Record{
		PID:    pid,
		Label:  "Codex",
		CWD:    cwd,
		Status: agent.StatusIdle,
		Detail: "started",
	}
	rec.Status, rec.Detail = codexStatusOf(last)
	rec.At = codexEventTime(path, last)

	for i := len(evs) - 1; i >= 0; i-- {
		ev := evs[i]
		if ev.Payload.Type != "token_count" || ev.Payload.Info == nil || ev.Payload.Info.LastTokenUsage == nil {
			continue
		}
		tu := ev.Payload.Info.LastTokenUsage
		u := agent.Usage{
			TokensUsed:   tu.InputTokens + tu.CachedInput + tu.OutputTokens + tu.ReasoningOutput,
			WindowTokens: ev.Payload.Info.ContextWindow,
		}
		if !u.Empty() {
			rec.Usage = u
		}
		break
	}
	return rec, true
}

// codexEventTime is the record's signal time: the event's own timestamp,
// falling back to the file's modification time.
func codexEventTime(path string, ev codexRolloutEvent) time.Time {
	if ts, err := time.Parse(time.RFC3339Nano, ev.Timestamp); err == nil {
		return ts
	}
	if fi, err := os.Stat(path); err == nil {
		return fi.ModTime()
	}
	return time.Now()
}

// codexStatusOf maps the last rollout event to the tmon status machine.
// A completed turn (task_complete) or a session with no activity leaves the
// agent idle; streaming messages and token accounting mean working. Unknown
// event types are shown as idle — new Codex versions may add events.
func codexStatusOf(ev codexRolloutEvent) (agent.Status, string) {
	if ev.Type == "event_msg" {
		switch ev.Payload.Type {
		case "task_complete":
			return agent.StatusIdle, "turn-complete"
		case "task_started":
			return agent.StatusWorking, "working"
		case "message", "agent_message":
			if ev.Payload.Phase != "" {
				return agent.StatusWorking, "phase:" + ev.Payload.Phase
			}
			return agent.StatusWorking, "working"
		case "token_count":
			return agent.StatusWorking, "working"
		case "user_message":
			return agent.StatusWorking, "working"
		default:
			return agent.StatusIdle, "started"
		}
	}
	switch ev.Type {
	case "response_item": // streaming assistant message content
		if ev.Payload.Phase != "" {
			return agent.StatusWorking, "phase:" + ev.Payload.Phase
		}
		return agent.StatusWorking, "working"
	case "workspace-write":
		return agent.StatusWorking, "working"
	default:
		return agent.StatusIdle, "started"
	}
}

// mergeCodexRecords overlays hook records (authoritative status/detail from
// lifecycle events) onto rollout records (token usage), keyed by PID. The
// hook record's status, detail and signal time win; the rollout supplies
// the Usage when the hook record carries none.
func mergeCodexRecords(rollout, hooks []Record) []Record {
	byPID := make(map[int]Record, len(rollout)+len(hooks))
	for _, r := range rollout {
		byPID[r.PID] = r
	}
	for _, h := range hooks {
		if cur, ok := byPID[h.PID]; ok && !cur.Usage.Empty() {
			h.Usage = cur.Usage
		}
		byPID[h.PID] = h
	}
	out := make([]Record, 0, len(byPID))
	for _, r := range byPID {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out
}
