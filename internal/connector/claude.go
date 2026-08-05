// claude.go — Claude Code connector (HOOKS tier).
//
// Claude Code exposes no readable live state file; its authoritative state
// comes from lifecycle hooks. `tmon hooks install claude` installs the
// generic agent-hook.sh (embedded in the binary) that writes one JSON state
// file per session under <state>/hooks/claude/ on every hook event. The
// shared machinery in hookstate.go reads those files back and pairs them
// with running Claude Code processes by working directory.
//
// Session titles come from Claude's own registry (~/.claude/sessions/<pid>.json,
// also the source for `claude agents --json`): each live session records a
// short name (derived, or set with /rename). The registry exists without
// tmon's hooks, so titles show whenever Claude is running.
package connector

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
	"github.com/guillaumemeyer/tmon/internal/proc"
)

// Claude reads Claude Code hook state written by the installed hook script
// and enriches each record with the session name from Claude's registry.
type Claude struct{}

// claudeHome returns the Claude config directory (~/.claude). A var so
// tests can point it at a fixture directory.
var claudeHome = func() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(h, ".claude")
}

// claudeSession is one entry of ~/.claude/sessions/<pid>.json, written by
// Claude Code itself. Only the fields the connector needs are decoded.
type claudeSession struct {
	PID       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	Name      string `json:"name"`
}

func (Claude) Name() string { return "claude" }

// Enabled reports whether any Claude hook state has been written.
func (Claude) Enabled(cfg config.Config) bool {
	return hookDirExists(cfg, "claude")
}

// Probe returns one record per live Claude session with fresh hook state,
// enriched with the session title from Claude's own session registry and
// token usage summed from the session transcript.
func (Claude) Probe(cfg config.Config) ([]Record, error) {
	recs, err := pairHookSessions(cfg, "claude", "Claude")
	if err != nil {
		return nil, err
	}
	names := claudeSessionNames()
	live := claudeLiveSessions()
	for i := range recs {
		if n := names[recs[i].PID]; n != "" {
			recs[i].Title = n
		}
		recs[i].Usage = claudeUsage(cfg.StateDir, recs[i].PID, recs[i].CWD, live[recs[i].PID].SessionID)
	}
	return recs, nil
}

// claudeLiveSessions maps live Claude PIDs to their registry entries by
// reading ~/.claude/sessions/*.json (the registry `claude agents --json`
// is built from). A missing registry or unreadable entries yield an empty
// map.
func claudeLiveSessions() map[int]claudeSession {
	dir := filepath.Join(claudeHome(), "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make(map[int]claudeSession)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil || pid <= 0 {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var s claudeSession
		if json.Unmarshal(b, &s) != nil {
			continue
		}
		out[pid] = s
	}
	return out
}

// claudeSessionNames maps live Claude PIDs to their session names by
// reading ~/.claude/sessions/*.json (the registry `claude agents --json`
// is built from). A missing registry or unreadable entries yield no names.
func claudeSessionNames() map[int]string {
	live := claudeLiveSessions()
	out := make(map[int]string, len(live))
	for pid, s := range live {
		if s.Name != "" {
			out[pid] = s.Name
		}
	}
	return out
}

// claudeUsage reads the token usage for one Claude session: the live
// session's transcript under the project dir (matched by the sessionId from
// Claude's own registry), falling back to the newest transcript when the
// live one has not been written yet (brand-new session), summed
// incrementally (cheap per poll), plus the context window for the model
// named in the transcript tail. Returns zero usage when no transcript
// exists yet (brand-new session) or nothing parses.
func claudeUsage(stateDir string, pid int, cwd, sessionID string) agent.Usage {
	dir := claudeProjectDir(cwd, pid)
	if dir == "" {
		return agent.Usage{}
	}
	path := ""
	if sessionID != "" {
		if p := filepath.Join(dir, sessionID+".jsonl"); fileExists(p) {
			path = p
		}
	}
	if path == "" {
		path = newestJSONL(dir)
	}
	if path == "" {
		return agent.Usage{}
	}
	tokens, err := incrementalTokens(stateDir, path, claudeParseUsage)
	if err != nil || tokens <= 0 {
		return agent.Usage{}
	}
	u := agent.Usage{TokensUsed: tokens}
	if w := claudeContextWindow(claudeTranscriptModel(path)); w > 0 {
		u.WindowTokens = w
	}
	return u
}

// claudeAbsCWD resolves the absolute working directory of a Claude process,
// falling back to the short form from the hook state. A seam for tests.
var claudeAbsCWD = func(pid int, short string) string {
	if c, err := proc.ReadCWD(pid); err == nil && c != "" {
		return c
	}
	return short
}

// claudeProjectDir returns the ~/.claude/projects/<encoded-cwd> directory
// for a Claude process, or "" when it cannot be located. Claude names the
// dir by encoding the working directory (leading "/" dropped, "/" → "-");
// a reverse-decode scan of the projects dir catches paths the simple
// encoder misses.
func claudeProjectDir(cwd string, pid int) string {
	projects := filepath.Join(claudeHome(), "projects")
	abs := claudeAbsCWD(pid, cwd)
	if abs == "" {
		return ""
	}
	enc := encodeClaudeProject(abs)
	if dir := filepath.Join(projects, enc); dirExists(dir) {
		return dir
	}
	return claudeProjectDirByDecode(projects, abs)
}

// encodeClaudeProject encodes an absolute path the way Claude Code names
// project dirs: leading "/" dropped, each remaining "/" replaced by "-".
func encodeClaudeProject(cwd string) string {
	return "-" + strings.ReplaceAll(strings.TrimPrefix(cwd, "/"), "/", "-")
}

// claudeProjectDirByDecode scans the projects dir for a directory whose
// name decodes back to cwd (the "/"→"-" scheme in reverse).
func claudeProjectDirByDecode(projectsDir, cwd string) string {
	want := strings.TrimPrefix(cwd, "/")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		decoded := strings.ReplaceAll(strings.TrimPrefix(e.Name(), "-"), "-", "/")
		if decoded == want {
			return filepath.Join(projectsDir, e.Name())
		}
	}
	return ""
}

// newestJSONL returns the most recently modified *.jsonl in dir, or "".
func newestJSONL(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var best string
	var bestMod int64 = -1
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		if fi.ModTime().UnixNano() > bestMod {
			bestMod = fi.ModTime().UnixNano()
			best = filepath.Join(dir, e.Name())
		}
	}
	return best
}

// claudeUsageBlock is the token usage Claude attaches to an assistant
// message.
type claudeUsageBlock struct {
	InputTokens        int64 `json:"input_tokens"`
	CacheCreationInput int64 `json:"cache_creation_input_tokens"`
	CacheReadInput     int64 `json:"cache_read_input_tokens"`
	OutputTokens       int64 `json:"output_tokens"`
}

// claudeParseUsage returns the tokens counted for one transcript line:
// input + cache creation + cache read + output from the message's usage
// block. Claude nests usage under message.usage; older transcripts carried
// it at the top level, so both are accepted. Lines without usage (user
// messages, system events, partial lines) yield 0.
func claudeParseUsage(line []byte) int64 {
	var ev struct {
		Message *struct {
			Usage *claudeUsageBlock `json:"usage"`
		} `json:"message"`
		Usage *claudeUsageBlock `json:"usage"`
	}
	if json.Unmarshal(line, &ev) != nil {
		return 0
	}
	var u *claudeUsageBlock
	if ev.Message != nil {
		u = ev.Message.Usage
	}
	if u == nil {
		u = ev.Usage
	}
	if u == nil {
		return 0
	}
	return u.InputTokens + u.CacheCreationInput + u.CacheReadInput + u.OutputTokens
}

// claudeTranscriptModel returns the last model name mentioned in the
// transcript tail (assistant events carry it as message.model, e.g.
// "claude-sonnet-5"), or "".
func claudeTranscriptModel(path string) string {
	model := ""
	for _, l := range tailEvents(path) {
		var ev struct {
			Message struct {
				Model string `json:"model"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(l), &ev) == nil && ev.Message.Model != "" {
			model = ev.Message.Model
		}
	}
	return model
}

// claudeContextWindows maps known Claude model families to their context
// window size. Best-effort and deliberately small: unknown models yield 0
// (no percentage), and the dashboard falls back to tokens-only.
var claudeContextWindows = []struct {
	prefix string
	window int64
}{
	{"claude-sonnet-5", 1000000},
	{"claude-opus-4", 200000},
	{"claude-sonnet-4", 1000000},
	{"claude-sonnet", 200000},
	{"claude-haiku", 200000},
	{"claude-opus", 200000},
	{"claude-3", 200000},
}

// claudeContextWindow looks up a model's context window by longest known
// prefix; 0 when unknown.
func claudeContextWindow(model string) int64 {
	for _, e := range claudeContextWindows {
		if strings.HasPrefix(model, e.prefix) {
			return e.window
		}
	}
	return 0
}
