// codex.go — Codex CLI connector (HOOKS tier).
//
// Codex exposes no readable live state file; its authoritative state comes
// from lifecycle hooks (`tmon hooks install codex` writes ~/.codex/hooks.json
// and installs the shared agent-hook.sh). Codex additionally requires the
// hooks to be trusted in-session via /hooks before they run — that step is
// manual and documented in the README.
//
// Token usage is enriched from the session transcript
// (~/.codex/sessions/<...>/history.jsonl), whose per-message `usage` blocks
// carry input/cached/output/reasoning tokens. Parsing is tolerant: format
// drift yields zero usage (no stats line), never an error.
package connector

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
)

// Codex reads Codex hook state written by the installed hook script.
type Codex struct{}

func (Codex) Name() string { return "codex" }

// Enabled reports whether any Codex hook state has been written.
func (Codex) Enabled(cfg config.Config) bool {
	return hookDirExists(cfg, "codex")
}

// Probe returns one record per live Codex session with fresh hook state.
func (Codex) Probe(cfg config.Config) ([]Record, error) {
	recs, err := pairHookSessions(cfg, "codex", "Codex")
	if err != nil {
		return nil, err
	}
	for i := range recs {
		recs[i].Usage = codexUsage(cfg.StateDir, recs[i].CWD)
	}
	return recs, nil
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

// codexUsage reads the token usage for one Codex session: the newest
// history.jsonl under ~/.codex/sessions that matches the session's working
// directory, summed incrementally. Zero usage when nothing matches or
// parses.
func codexUsage(stateDir, cwd string) agent.Usage {
	path := newestCodexHistory(filepath.Join(codexHome(), "sessions"), cwd)
	if path == "" {
		return agent.Usage{}
	}
	tokens, err := incrementalTokens(stateDir, path, codexParseUsage)
	if err != nil || tokens <= 0 {
		return agent.Usage{}
	}
	return agent.Usage{TokensUsed: tokens}
}

// newestCodexHistory returns the most recently modified history.jsonl whose
// path mentions the working directory (dash-joined, e.g. "code-tmon", or
// the bare basename), or "".
func newestCodexHistory(sessionsDir, cwd string) string {
	if sessionsDir == "" || cwd == "" {
		return ""
	}
	var best string
	var bestMod int64 = -1
	_ = filepath.WalkDir(sessionsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "history.jsonl" {
			return nil
		}
		if !codexPathMatches(path, cwd) {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		if fi.ModTime().UnixNano() > bestMod {
			bestMod = fi.ModTime().UnixNano()
			best = path
		}
		return nil
	})
	return best
}

// codexPathMatches reports whether a history path belongs to the given
// working directory: the path mentions the dash-joined cwd ("code-tmon") or
// its bare basename ("tmon").
func codexPathMatches(path, cwd string) bool {
	if strings.Contains(path, strings.ReplaceAll(cwd, "/", "-")) {
		return true
	}
	base := cwd
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	return base != "" && strings.Contains(path, base)
}

// codexParseUsage returns the tokens counted for one history line: input +
// cached input + output + reasoning tokens from a usage block. Lines without
// usage (or partial lines) yield 0.
func codexParseUsage(line []byte) int64 {
	var ev struct {
		Usage *struct {
			InputTokens     int64 `json:"input_tokens"`
			CachedInput     int64 `json:"cached_input_tokens"`
			OutputTokens    int64 `json:"output_tokens"`
			ReasoningOutput int64 `json:"reasoning_output_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(line, &ev) != nil || ev.Usage == nil {
		return 0
	}
	return ev.Usage.InputTokens + ev.Usage.CachedInput + ev.Usage.OutputTokens + ev.Usage.ReasoningOutput
}
