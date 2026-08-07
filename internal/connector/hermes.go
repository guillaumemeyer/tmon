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

		// A live CLI/TUI with no session row yet (Hermes persists rows only
		// on the first prompt, so a freshly opened TUI has none) still owns
		// a real context window. Report the empty bar at 0% — instead of
		// "context: ?" — so the row carries context usage from the first
		// poll. Once the first prompt lands, the row appears and the real
		// counters take over.
		if rec.Usage.Empty() {
			if m := strings.TrimPrefix(rec.Detail, "model:"); m != rec.Detail {
				if w := hermesModelWindow(r.home.path, m); w > 0 {
					rec.Usage = agent.Usage{WindowTokens: w}
				}
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
	pid       int
	cmdline   string
	cwd       string // short form
	cwdFull   string
	envHome   string // HERMES_HOME from environ, may be empty
	startedAt int64  // unix seconds; 0 when unknown
}

// hermesListPIDs / hermesReadCmdline are seams for tests.
var (
	hermesListPIDs    = proc.ListPIDs
	hermesReadCmdline = proc.ReadCmdline
	hermesReadCWD     = proc.ReadCWD
	hermesReadEnv     = proc.ReadEnv
	hermesReadStart   = proc.StartTimeUnix
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
		if t, err := hermesReadStart(pid); err == nil {
			p.startedAt = t
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
	CacheRead int64
	APICalls  int64
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
		       COALESCE(cache_read_tokens, 0),
		       COALESCE(api_call_count, 0),
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
		if err := rows.Scan(&s.ID, &s.Title, &s.Model, &s.CWD, &s.Source, &s.InTokens, &s.OutTokens, &s.CacheRead, &s.APICalls, &s.StartedAt); err != nil {
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

// hermesPairSkew is how much later a session row may start relative to the
// process that would claim it and still be considered "the process's
// session". Both timestamps are wall-clock unix seconds; the tolerance
// covers HZ rounding in the process start time and small clock skew without
// swallowing a session that legitimately began around the same moment.
const hermesPairSkew = 60

// hermesStaleSession reports whether an open session row predates the
// process that would claim it. Hermes leaves many "open" rows behind when a
// TUI is killed, and a fresh TUI has no row of its own until the first
// prompt (rows persist lazily) — so without this check a brand-new process
// in a directory with an abandoned open row (same cwd) steals that row's old
// title and token counts. A resumed session keeps its original started_at
// too, but a resume creates a fresh row with a new started_at on the first
// prompt, which takes over immediately. When either timestamp is unknown
// (start time unreadable, or the row predates started_at recording) the
// session is never considered stale, so nothing regresses on platforms
// without process start times.
func hermesStaleSession(s hermesSession, procStart int64) bool {
	if procStart <= 0 || s.StartedAt <= 0 {
		return false
	}
	return int64(s.StartedAt) < procStart-hermesPairSkew
}

// pairHermesSession binds a live CLI/TUI process to an open state.db session.
// Pairing is conservative: a wrong title is worse than no title. Hermes leaves
// many "open" rows (ended_at IS NULL) from prior TUIs, so guessing the newest
// row attaches stale names (e.g. an old "Gateway Auth Failure" chat) to a
// brand-new process in a different workspace.
//
// Match order:
//  1. Session CWD equals the process CWD (strong signal), provided the row
//     is not older than the process itself (see hermesStaleSession) — a
//     fresh TUI must not inherit an abandoned row that happens to share the
//     directory.
//  2. Sole open session with no recorded CWD, and only one live PID in this
//     home — older Hermes rows omit cwd; safe only when there is nothing else
//     to confuse them with, and the row is still not older than the process.
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
				if hermesStaleSession(*s, p.startedAt) {
					continue
				}
				return s
			}
		}
	}
	// No CWD match. Do not fall back to "newest open session" — that is how
	// stale titles leak into the popup. Only accept a sole empty-CWD row when
	// a single process owns the home (nothing to disambiguate against) and
	// the row is not older than the process.
	if pidsInHome <= 1 && len(sessions) == 1 && sessions[0].CWD == "" && !hermesStaleSession(sessions[0], p.startedAt) {
		return &sessions[0]
	}
	return nil
}

// ─── usage / model window ────────────────────────────────────────────────────

func hermesSessionUsage(home string, s *hermesSession) agent.Usage {
	if s == nil {
		return agent.Usage{}
	}
	used := hermesContextTokens(s)
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

// hermesContextTokens approximates the CLI's last_prompt_tokens (current
// context occupancy) from state.db lifetime counters. Hermes itself refuses
// to use session totals for the gauge; without the in-memory compressor we
// estimate:
//
//   - prompt-side tokens: input + cache_read (what fills the window on a call)
//   - when api_call_count > 1, average per call so multi-turn cache sums do
//     not report multi-window totals (e.g. 2.7M cache reads / 1M window)
//   - single-call sessions match the CLI closely (in+cache ≈ last prompt)
//
// Falls back to input+output when no prompt-side tokens are recorded.
func hermesContextTokens(s *hermesSession) int64 {
	if s == nil {
		return 0
	}
	prompt := s.InTokens + s.CacheRead
	if prompt > 0 {
		if s.APICalls > 1 {
			return prompt / s.APICalls
		}
		return prompt
	}
	return s.InTokens + s.OutTokens
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
// entries whose path matches the model id. The tree is decoded into maps,
// whose iteration order is randomized, so the walk collects every match and
// returns the smallest window per match class (exact segment first, then
// substring): a model may be listed under several providers with different
// limits, and the smallest consistent value is both the conservative
// estimate for a context bar and a deterministic one. Exact segment matches
// win so a different model whose name merely contains the id (e.g.
// deepseek-v4-flash-free) never caps the real model's window.
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
	var exact, sub int64
	var walk func(any, string)
	walk = func(v any, pathKey string) {
		switch t := v.(type) {
		case map[string]any:
			if lim, ok := t["limit"].(map[string]any); ok {
				if ctx, ok := lim["context"]; ok {
					if isExact, matched := modelMatchClass(pathKey, want); matched {
						if isExact {
							exact = minNonZero(exact, jsonNumber(ctx))
						} else {
							sub = minNonZero(sub, jsonNumber(ctx))
						}
					}
				}
			}
			// Also accept direct context_window / max_context fields.
			for _, k := range []string{"context_window", "max_context", "context"} {
				if n, ok := t[k]; ok {
					if isExact, matched := modelMatchClass(pathKey, want); matched {
						if isExact {
							exact = minNonZero(exact, jsonNumber(n))
						} else {
							sub = minNonZero(sub, jsonNumber(n))
						}
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
	if exact > 0 {
		return exact
	}
	return sub
}

// minNonZero returns the smaller of the two values, treating 0 as "no value
// yet": the first nonzero value wins, then the minimum of both.
func minNonZero(a, b int64) int64 {
	if a == 0 {
		return b
	}
	if b == 0 || b >= a {
		return a
	}
	return b
}

// modelMatchClass classifies how a JSON path matches a model id. The first
// result reports an exact match: the final path segment, ignoring a
// ":variant" suffix (e.g. ":thinking"), equals the model id. The second
// result is false when the path does not match at all. Exact matches win
// over substring matches (provider/model paths where the id is a suffix, or
// longer names that merely contain it).
func modelMatchClass(pathKey, want string) (exact, ok bool) {
	if pathKey == "" || want == "" {
		return false, false
	}
	last := pathKey
	if i := strings.LastIndex(last, "/"); i >= 0 {
		last = last[i+1:]
	}
	if i := strings.IndexByte(last, ':'); i >= 0 {
		last = last[:i]
	}
	if last == want {
		return true, true
	}
	return false, strings.Contains(pathKey, want)
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
