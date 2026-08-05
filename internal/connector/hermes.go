// hermes.go — Hermes Agent connector (CLI/TUI sessions only).
//
// Hermes is multi-home: the default profile lives at ~/.hermes and named
// profiles under ~/.hermes/profiles/<name>/. Each home has its own state.db
// and config. The connector surfaces live local CLI/TUI processes (not the
// messaging gateway), enriched with session title, model, profile name, and
// context-token stats from state.db.
//
// Dangerous-command approvals are in-memory inside Hermes; optional shell
// hooks (tmon hooks install hermes) write pending state under HookStateDir
// so the connector can mark those sessions blocked.
package connector

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
	"github.com/guillaumemeyer/tmon/internal/detect"
	"github.com/guillaumemeyer/tmon/internal/proc"

	_ "modernc.org/sqlite"
)

// hermesHome returns the default Hermes config directory (~/.hermes). A var
// so tests can point it at a fixture directory.
var hermesHome = func() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(h, ".hermes")
}

// Hermes enriches live CLI/TUI Hermes processes with session state.
type Hermes struct{}

func (Hermes) Name() string { return "hermes" }

// Enabled reports whether any Hermes home looks installed.
func (Hermes) Enabled(cfg config.Config) bool {
	for _, h := range hermesHomes() {
		if hermesHomeInstalled(h.path) {
			return true
		}
	}
	return false
}

// Probe returns one record per live CLI/TUI Hermes process, excluding the
// messaging gateway. Empty when no interactive Hermes is running.
func (Hermes) Probe(cfg config.Config) ([]Record, error) {
	procs, err := hermesCLIProcesses()
	if err != nil || len(procs) == 0 {
		return nil, err
	}

	homes := hermesHomes()
	sessionsByHome := make(map[string][]hermesSession, len(homes))
	for _, h := range homes {
		sessionsByHome[h.path] = loadHermesLocalSessions(h.path)
	}
	approvals := loadHermesApprovals(cfg)

	// Count live PIDs per home for pairing heuristics.
	pidsPerHome := make(map[string]int)
	resolved := make([]struct {
		p       hermesProc
		home    hermesHomeInfo
		profile string
	}, 0, len(procs))
	for _, p := range procs {
		home, profile := resolveHermesHome(p, homes, sessionsByHome)
		resolved = append(resolved, struct {
			p       hermesProc
			home    hermesHomeInfo
			profile string
		}{p, home, profile})
		if home.path != "" {
			pidsPerHome[home.path]++
		}
	}

	now := time.Now()
	recs := make([]Record, 0, len(resolved))
	for _, r := range resolved {
		rec := Record{
			PID:     r.p.pid,
			Label:   "Hermes",
			Status:  agent.StatusIdle,
			Profile: r.profile,
			CWD:     r.p.cwd,
			At:      now, // live process: keep the record fresh each poll
		}
		sess := pairHermesSession(r.p, r.home.path, sessionsByHome[r.home.path], pidsPerHome[r.home.path])
		if sess != nil {
			rec.Title = sess.Title
			if sess.CWD != "" {
				rec.CWD = proc.CWDShort(sess.CWD)
			}
			if sess.Model != "" {
				rec.Detail = "model:" + sess.Model
			}
			rec.Usage = hermesSessionUsage(r.home.path, sess)
			if sess.LastMsgAt > 0 {
				age := now.Sub(time.Unix(sess.LastMsgAt, 0))
				if age >= 0 && age <= cfg.ConnectorFreshness {
					rec.Status = agent.StatusWorking
					if rec.Detail == "" {
						rec.Detail = "active"
					}
				}
			}
		}
		if rec.Detail == "" {
			if m := hermesConfigModel(r.home.path); m != "" {
				rec.Detail = "model:" + m
			}
		}

		if ap := matchHermesApproval(approvals, r.home.path, sess); ap != nil {
			rec.Status = agent.StatusBlocked
			detail := "approval"
			if ap.PatternKey != "" {
				detail = "approval:" + ap.PatternKey
			}
			rec.Detail = detail
			rec.At = now
		}
		recs = append(recs, rec)
	}
	return recs, nil
}

// ─── homes / profiles ────────────────────────────────────────────────────────

type hermesHomeInfo struct {
	name string // "default" or profile name
	path string // absolute HERMES_HOME
}

// hermesHomes lists the default home and every named profile directory.
func hermesHomes() []hermesHomeInfo {
	root := hermesHome()
	if root == "" {
		return nil
	}
	out := []hermesHomeInfo{{name: "default", path: root}}
	entries, err := os.ReadDir(filepath.Join(root, "profiles"))
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "" || strings.HasPrefix(name, ".") {
			continue
		}
		out = append(out, hermesHomeInfo{
			name: name,
			path: filepath.Join(root, "profiles", name),
		})
	}
	return out
}

func hermesHomeInstalled(path string) bool {
	for _, name := range []string{"config.yaml", "state.db", "gateway_state.json"} {
		if fi, err := os.Stat(filepath.Join(path, name)); err == nil && !fi.IsDir() {
			return true
		}
	}
	return false
}

// profileNameFromHome maps a HERMES_HOME path to a profile name.
func profileNameFromHome(homePath, root string) string {
	homePath = filepath.Clean(homePath)
	root = filepath.Clean(root)
	if homePath == root {
		return "default"
	}
	// …/profiles/<name>
	rel, err := filepath.Rel(filepath.Join(root, "profiles"), homePath)
	if err == nil && rel != "." && !strings.HasPrefix(rel, "..") && !strings.Contains(rel, string(os.PathSeparator)) {
		return rel
	}
	// basename fallback when root differs (custom HERMES_HOME)
	if base := filepath.Base(homePath); base != "" && base != "." {
		if base == ".hermes" || base == "hermes" {
			return "default"
		}
		return base
	}
	return "default"
}

// ─── process discovery ───────────────────────────────────────────────────────

type hermesProc struct {
	pid     int
	cmdline string
	cwd     string // short form
	cwdFull string
	envHome string // HERMES_HOME from environ, may be empty
}

// hermesListPIDs / hermesReadCmdline are seams for tests.
var (
	hermesListPIDs    = proc.ListPIDs
	hermesReadCmdline = proc.ReadCmdline
	hermesReadCWD     = proc.ReadCWD
	hermesReadEnv     = proc.ReadEnv
)

func hermesCLIProcesses() ([]hermesProc, error) {
	pids, err := hermesListPIDs()
	if err != nil {
		return nil, err
	}
	var out []hermesProc
	for _, pid := range pids {
		cmdline, err := hermesReadCmdline(pid)
		if err != nil || cmdline == "" {
			continue
		}
		if detect.MatchLabel(cmdline) != "Hermes" {
			continue
		}
		if isHermesGateway(cmdline) {
			continue
		}
		p := hermesProc{pid: pid, cmdline: cmdline, envHome: hermesReadEnv(pid, "HERMES_HOME")}
		if c, err := hermesReadCWD(pid); err == nil {
			p.cwdFull = c
			p.cwd = proc.CWDShort(c)
		}
		out = append(out, p)
	}
	return out, nil
}

// isHermesGateway reports messaging-gateway processes that must not appear
// as dashboard agents.
func isHermesGateway(cmdline string) bool {
	c := strings.ToLower(cmdline)
	if strings.Contains(c, "gateway run") {
		return true
	}
	if strings.Contains(c, "hermes_cli.main") && strings.Contains(c, "gateway") {
		return true
	}
	// systemd unit / service helpers
	if strings.Contains(c, "hermes-gateway") {
		return true
	}
	return false
}

func resolveHermesHome(p hermesProc, homes []hermesHomeInfo, sessions map[string][]hermesSession) (hermesHomeInfo, string) {
	root := hermesHome()
	if p.envHome != "" {
		name := profileNameFromHome(p.envHome, root)
		return hermesHomeInfo{name: name, path: filepath.Clean(p.envHome)}, name
	}
	// Match process CWD against open session cwds across homes.
	if p.cwdFull != "" {
		short := proc.CWDShort(p.cwdFull)
		for _, h := range homes {
			for _, s := range sessions[h.path] {
				if s.CWD == "" {
					continue
				}
				if proc.CWDShort(s.CWD) == short || s.CWD == p.cwdFull {
					return h, h.name
				}
			}
		}
	}
	// Sticky active_profile or default home.
	if len(homes) > 0 {
		if name := readActiveProfile(root); name != "" && name != "default" {
			for _, h := range homes {
				if h.name == name {
					return h, h.name
				}
			}
		}
		return homes[0], homes[0].name
	}
	return hermesHomeInfo{name: "default", path: root}, "default"
}

func readActiveProfile(root string) string {
	b, err := os.ReadFile(filepath.Join(root, "active_profile"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// ─── sessions (state.db) ─────────────────────────────────────────────────────

type hermesSession struct {
	ID        string
	Title     string
	Model     string
	CWD       string
	Source    string
	InTokens  int64
	OutTokens int64
	StartedAt float64
	LastMsgAt int64 // unix seconds of newest message; 0 if unknown
}

func loadHermesLocalSessions(home string) []hermesSession {
	dbPath := filepath.Join(home, "state.db")
	if _, err := os.Stat(dbPath); err != nil {
		return nil
	}
	// Read-only open; immutable=1 helps when the writer holds a WAL lock.
	dsn := fmt.Sprintf("file:%s?mode=ro&_pragma=query_only(1)", filepath.ToSlash(dbPath))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	rows, err := db.Query(`
		SELECT id,
		       COALESCE(title, ''),
		       COALESCE(model, ''),
		       COALESCE(cwd, ''),
		       COALESCE(source, ''),
		       COALESCE(input_tokens, 0),
		       COALESCE(output_tokens, 0),
		       COALESCE(started_at, 0)
		FROM sessions
		WHERE ended_at IS NULL
		  AND source IN ('cli', 'tui')
		  AND COALESCE(archived, 0) = 0
		ORDER BY started_at DESC
	`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []hermesSession
	for rows.Next() {
		var s hermesSession
		if err := rows.Scan(&s.ID, &s.Title, &s.Model, &s.CWD, &s.Source, &s.InTokens, &s.OutTokens, &s.StartedAt); err != nil {
			continue
		}
		out = append(out, s)
	}
	// Best-effort last message timestamps for working detection.
	for i := range out {
		var ts sql.NullFloat64
		err := db.QueryRow(
			`SELECT MAX(timestamp) FROM messages WHERE session_id = ? AND COALESCE(active, 1) = 1`,
			out[i].ID,
		).Scan(&ts)
		if err == nil && ts.Valid && ts.Float64 > 0 {
			out[i].LastMsgAt = int64(ts.Float64)
		}
	}
	return out
}

// pairHermesSession binds a live CLI/TUI process to an open state.db session.
// Pairing is conservative: a wrong title is worse than no title. Hermes leaves
// many "open" rows (ended_at IS NULL) from prior TUIs, so guessing the newest
// row attaches stale names (e.g. an old "Gateway Auth Failure" chat) to a
// brand-new process in a different workspace.
//
// Match order:
//  1. Session CWD equals the process CWD (strong signal).
//  2. Sole open session with no recorded CWD, and only one live PID in this
//     home — older Hermes rows omit cwd; safe only when there is nothing else
//     to confuse them with.
//  3. Otherwise unpaired (Title/Usage stay empty; model falls back to config).
func pairHermesSession(p hermesProc, homePath string, sessions []hermesSession, pidsInHome int) *hermesSession {
	if len(sessions) == 0 {
		return nil
	}
	if p.cwdFull != "" || p.cwd != "" {
		short := p.cwd
		if short == "" {
			short = proc.CWDShort(p.cwdFull)
		}
		for i := range sessions {
			s := &sessions[i]
			if s.CWD == "" {
				continue
			}
			if proc.CWDShort(s.CWD) == short || s.CWD == p.cwdFull {
				return s
			}
		}
	}
	// No CWD match. Do not fall back to "newest open session" — that is how
	// stale titles leak into the popup. Only accept a sole empty-CWD row when
	// a single process owns the home (nothing to disambiguate against).
	if pidsInHome <= 1 && len(sessions) == 1 && sessions[0].CWD == "" {
		return &sessions[0]
	}
	return nil
}

// ─── usage / model window ────────────────────────────────────────────────────

func hermesSessionUsage(home string, s *hermesSession) agent.Usage {
	if s == nil {
		return agent.Usage{}
	}
	used := s.InTokens + s.OutTokens
	if used <= 0 {
		return agent.Usage{}
	}
	u := agent.Usage{TokensUsed: used}
	if s.Model != "" {
		if w := hermesModelWindow(home, s.Model); w > 0 {
			u.WindowTokens = w
		}
	}
	return u
}

// hermesConfigModel reads model.default from config.yaml without a full YAML
// dependency (top-level model: block only).
func hermesConfigModel(home string) string {
	b, err := os.ReadFile(filepath.Join(home, "config.yaml"))
	if err != nil {
		return ""
	}
	inModel := false
	for _, line := range strings.Split(string(b), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if indent == 0 {
			inModel = strings.HasPrefix(trimmed, "model:")
			continue
		}
		if !inModel {
			continue
		}
		if strings.HasPrefix(trimmed, "default:") {
			v := strings.TrimSpace(strings.TrimPrefix(trimmed, "default:"))
			v = strings.Trim(v, `"'`)
			return v
		}
	}
	return ""
}

var (
	hermesWindowMu    sync.Mutex
	hermesWindowCache = map[string]int64{} // model → window
)

// hermesModelWindow looks up the context window for model in the home's
// models_dev_cache.json (or cache/model_catalog when needed).
func hermesModelWindow(home, model string) int64 {
	if model == "" {
		return 0
	}
	hermesWindowMu.Lock()
	if w, ok := hermesWindowCache[model]; ok {
		hermesWindowMu.Unlock()
		return w
	}
	hermesWindowMu.Unlock()

	w := lookupModelWindow(filepath.Join(home, "models_dev_cache.json"), model)
	if w <= 0 {
		w = lookupModelWindow(filepath.Join(home, "cache", "model_catalog.json"), model)
	}
	hermesWindowMu.Lock()
	hermesWindowCache[model] = w
	hermesWindowMu.Unlock()
	return w
}

// lookupModelWindow walks a models.dev-style JSON tree for limit.context
// entries whose path contains the model id.
func lookupModelWindow(path, model string) int64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	var root any
	if json.Unmarshal(b, &root) != nil {
		return 0
	}
	want := strings.ToLower(model)
	var found int64
	var walk func(any, string)
	walk = func(v any, pathKey string) {
		if found > 0 {
			return
		}
		switch t := v.(type) {
		case map[string]any:
			if lim, ok := t["limit"].(map[string]any); ok {
				if ctx, ok := lim["context"]; ok && modelKeyMatch(pathKey, want) {
					found = jsonNumber(ctx)
					return
				}
			}
			// Also accept direct context_window / max_context fields.
			for _, k := range []string{"context_window", "max_context", "context"} {
				if n, ok := t[k]; ok && modelKeyMatch(pathKey, want) {
					if v := jsonNumber(n); v > 0 {
						found = v
						return
					}
				}
			}
			for k, child := range t {
				next := pathKey
				if next != "" {
					next += "/"
				}
				next += strings.ToLower(k)
				walk(child, next)
			}
		case []any:
			for _, child := range t {
				walk(child, pathKey)
			}
		}
	}
	walk(root, "")
	return found
}

func modelKeyMatch(pathKey, want string) bool {
	if pathKey == "" || want == "" {
		return false
	}
	if strings.Contains(pathKey, want) {
		return true
	}
	// path may be provider/model; want may be bare model
	parts := strings.Split(want, "/")
	bare := parts[len(parts)-1]
	return bare != "" && strings.Contains(pathKey, bare)
}

func jsonNumber(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	case string:
		var f float64
		if _, err := fmt.Sscanf(n, "%f", &f); err == nil {
			return int64(f)
		}
	}
	return 0
}

// ─── approvals (hook state) ──────────────────────────────────────────────────

type hermesApprovalFile struct {
	Pending map[string]hermesApprovalEntry `json:"pending"`
}

type hermesApprovalEntry struct {
	PatternKey  string `json:"pattern_key"`
	Description string `json:"description"`
	Surface     string `json:"surface"`
	HermesHome  string `json:"hermes_home"`
	SessionKey  string `json:"session_key"`
	TS          int64  `json:"ts"`
}

func loadHermesApprovals(cfg config.Config) *hermesApprovalFile {
	path := filepath.Join(cfg.HookStateDir, "hermes", "approvals.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return &hermesApprovalFile{Pending: map[string]hermesApprovalEntry{}}
	}
	var f hermesApprovalFile
	if json.Unmarshal(b, &f) != nil || f.Pending == nil {
		return &hermesApprovalFile{Pending: map[string]hermesApprovalEntry{}}
	}
	return &f
}

func matchHermesApproval(f *hermesApprovalFile, home string, sess *hermesSession) *hermesApprovalEntry {
	if f == nil || len(f.Pending) == 0 {
		return nil
	}
	home = filepath.Clean(home)
	for k, e := range f.Pending {
		// Skip smart-mode auto decisions if they somehow landed here.
		if strings.EqualFold(e.Surface, "smart") {
			continue
		}
		if e.HermesHome != "" && filepath.Clean(e.HermesHome) == home {
			ent := e
			ent.SessionKey = k
			return &ent
		}
		// Match session id substring in session_key when home not set.
		if sess != nil && sess.ID != "" && strings.Contains(k, sess.ID) {
			ent := e
			return &ent
		}
	}
	// Any pending for this default home when hermes_home empty and home is default.
	if home == filepath.Clean(hermesHome()) {
		for k, e := range f.Pending {
			if e.HermesHome == "" && !strings.EqualFold(e.Surface, "smart") {
				ent := e
				ent.SessionKey = k
				return &ent
			}
		}
	}
	return nil
}
