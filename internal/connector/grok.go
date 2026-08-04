// grok.go — Grok Build connector.
//
// Grok is the EXACT tier: it exposes its own live state, no installation
// needed. The connector reads ~/.grok/active_sessions.json (the
// authoritative list of running sessions) and tails each session's
// events.jsonl for the current phase.
package connector

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
	"github.com/guillaumemeyer/tmon/internal/proc"
)

// grokHome returns the Grok config directory (~/.grok). A var so tests can
// point it at a fixture directory.
var grokHome = func() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(h, ".grok")
}

// grokEventTailBytes bounds how much of a session's events.jsonl is read
// per poll. The file grows to megabytes; the phase signal we need is always
// at the tail.
const grokEventTailBytes = 128 * 1024

// Grok reads Grok Build's live session state.
type Grok struct{}

// activeSession is one entry of ~/.grok/active_sessions.json.
type activeSession struct {
	SessionID string    `json:"session_id"`
	PID       int       `json:"pid"`
	CWD       string    `json:"cwd"`
	OpenedAt  time.Time `json:"opened_at"`
}

// grokEvent is one line of a session's events.jsonl; only the fields the
// connector needs are decoded.
type grokEvent struct {
	TS       string `json:"ts"`
	Type     string `json:"type"`
	Phase    string `json:"phase"`
	ToolName string `json:"tool_name"`
}

func (Grok) Name() string { return "grok" }

// Enabled reports whether the Grok state surface exists.
func (Grok) Enabled(cfg config.Config) bool {
	p := filepath.Join(grokHome(), "active_sessions.json")
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// Probe returns one record per active Grok session.
func (Grok) Probe(cfg config.Config) ([]Record, error) {
	path := filepath.Join(grokHome(), "active_sessions.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var sessions []activeSession
	if err := json.Unmarshal(b, &sessions); err != nil {
		return nil, err
	}

	home := grokHome()
	now := time.Now()
	recs := make([]Record, 0, len(sessions))
	for _, s := range sessions {
		if s.PID <= 0 || s.SessionID == "" {
			continue
		}
		rec := Record{
			PID:    s.PID,
			Label:  "Grok",
			Status: agent.StatusIdle,
			Detail: "started",
			At:     now,
		}
		if !s.OpenedAt.IsZero() {
			rec.At = s.OpenedAt // a session with no events yet is "idle since opened"
		}
		if s.CWD != "" {
			rec.CWD = proc.CWDShort(s.CWD)
		}
		if dir := sessionDir(home, s.CWD, s.SessionID); dir != "" {
			enrichGrok(&rec, dir)
		}
		recs = append(recs, rec)
	}
	return recs, nil
}

// sessionDir locates the session directory under ~/.grok/sessions. Grok
// names it <url-encoded cwd>/<session_id>; the glob fallback covers any
// encoding mismatch.
func sessionDir(home, cwd, sessionID string) string {
	if cwd != "" {
		d := filepath.Join(home, "sessions", url.PathEscape(cwd), sessionID)
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			return d
		}
	}
	matches, _ := filepath.Glob(filepath.Join(home, "sessions", "*", sessionID))
	for _, m := range matches {
		if fi, err := os.Stat(m); err == nil && fi.IsDir() {
			return m
		}
	}
	return ""
}

// enrichGrok tails the session's events.jsonl and fills the record's status
// and detail from the last phase_changed event, plus the running tool or
// pending permission.
func enrichGrok(rec *Record, dir string) {
	var lastPhase, lastTool, lastPermission, lastCompletion grokEvent
	for _, l := range tailEvents(filepath.Join(dir, "events.jsonl")) {
		var e grokEvent
		if err := json.Unmarshal([]byte(l), &e); err != nil {
			continue // first (truncated) line in the window or noise
		}
		switch e.Type {
		case "phase_changed":
			lastPhase = e
		case "tool_started":
			lastTool = e
		case "permission_requested":
			lastPermission = e
		case "tool_completed":
			lastCompletion = e
		}
	}

	if lastPhase.TS != "" {
		// The phase event's timestamp drives the freshness gate: a phase
		// that stopped emitting (turn finished, prompt awaiting) decays to
		// the heuristic path after TMON_CONNECTOR_FRESHNESS.
		if ts, err := time.Parse(time.RFC3339Nano, lastPhase.TS); err == nil {
			rec.At = ts
		}
		rec.Status, rec.Detail = mapGrokPhase(lastPhase.Phase)
		if rec.Status == agent.StatusWorking && rec.Detail == "tool:" {
			name := lastTool.ToolName
			if name == "" || (lastCompletion.ToolName == name && lastCompletion.TS > lastTool.TS) {
				name = "running"
			}
			rec.Detail = "tool:" + name
		}
		if rec.Status == agent.StatusBlocked && lastPermission.ToolName != "" {
			rec.Detail = "permission:" + lastPermission.ToolName
		}
	}

	// Enrichment from signals.json: model + context usage.
	if sig := readGrokSignals(dir); sig.PrimaryModel != "" {
		rec.Detail += fmt.Sprintf(" · model:%s · ctx:%d%%", sig.PrimaryModel, sig.ContextUsage)
	}

	// Session title: summary.json carries the generated conversation title
	// (also mirrored in session_summary), which the dashboard shows as
	// "Title (Grok Build)".
	if t := readGrokTitle(dir); t != "" {
		rec.Title = t
	}
}

// mapGrokPhase maps a Grok phase_changed value to the tmon status machine.
// Unknown phases are shown as idle — new Grok versions may add phases.
func mapGrokPhase(phase string) (agent.Status, string) {
	switch phase {
	case "permission_prompt":
		return agent.StatusBlocked, "waiting:approval"
	case "streaming_reasoning":
		return agent.StatusWorking, "phase:reasoning"
	case "streaming_text":
		return agent.StatusWorking, "phase:responding"
	case "tool_execution":
		return agent.StatusWorking, "tool:"
	case "waiting_for_model":
		return agent.StatusWorking, "phase:waiting-model"
	default:
		return agent.StatusIdle, "phase:" + phase
	}
}

// grokSignals is the subset of ~/.grok/sessions/.../signals.json the
// connector uses for enrichment.
type grokSignals struct {
	PrimaryModel  string `json:"primaryModelId"`
	ContextUsage  int    `json:"contextWindowUsage"`
	ContextTokens int64  `json:"contextTokensUsed"`
	ContextWindow int64  `json:"contextWindowTokens"`
}

func readGrokSignals(dir string) grokSignals {
	b, err := os.ReadFile(filepath.Join(dir, "signals.json"))
	if err != nil {
		return grokSignals{}
	}
	var s grokSignals
	_ = json.Unmarshal(b, &s) // best-effort enrichment
	return s
}

// grokSummary is the subset of ~/.grok/sessions/.../summary.json used for
// the session title. generated_title is the conversation's AI-generated
// title; session_summary carries the same text in practice.
type grokSummary struct {
	GeneratedTitle string `json:"generated_title"`
	SessionSummary string `json:"session_summary"`
}

// readGrokTitle returns the session's title from summary.json, preferring
// generated_title with session_summary as a fallback. Empty when the file
// is missing or carries no title (brand-new sessions).
func readGrokTitle(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "summary.json"))
	if err != nil {
		return ""
	}
	var s grokSummary
	if json.Unmarshal(b, &s) != nil {
		return ""
	}
	if s.GeneratedTitle != "" {
		return s.GeneratedTitle
	}
	return s.SessionSummary
}

// tailEvents returns the complete lines from the end of path, bounded by
// grokEventTailBytes. The first line in the window is partial when the file
// is larger than the window; callers drop it via the JSON parse.
func tailEvents(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil
	}
	start := fi.Size() - grokEventTailBytes
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

	lines := strings.Split(string(b), "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}
