package connector

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"

	_ "modernc.org/sqlite"
)

// hermesFixtureHome points hermesHome at a temp ~/.hermes layout and returns
// the home path. Optional profile homes are created under profiles/.
func hermesFixtureHome(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, ".hermes")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	// Minimal install marker.
	writeFile(t, filepath.Join(home, "config.yaml"), "model:\n  default: deepseek-v4-flash\n")
	old := hermesHome
	hermesHome = func() string { return home }
	t.Cleanup(func() { hermesHome = old })
	return home
}

func hermesTestCfg(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Defaults()
	cfg.StateDir = t.TempDir()
	cfg.HookStateDir = filepath.Join(cfg.StateDir, "hooks")
	cfg.ConnectorFreshness = time.Minute
	return cfg
}

// stubHermesProcs replaces process discovery with a fixed list.
func stubHermesProcs(t *testing.T, procs []hermesProc) {
	t.Helper()
	oldList := hermesListPIDs
	oldCmd := hermesReadCmdline
	oldCWD := hermesReadCWD
	oldEnv := hermesReadEnv
	byPID := make(map[int]hermesProc, len(procs))
	pids := make([]int, 0, len(procs))
	for _, p := range procs {
		byPID[p.pid] = p
		pids = append(pids, p.pid)
	}
	hermesListPIDs = func() ([]int, error) { return pids, nil }
	hermesReadCmdline = func(pid int) (string, error) {
		if p, ok := byPID[pid]; ok {
			return p.cmdline, nil
		}
		return "", os.ErrNotExist
	}
	hermesReadCWD = func(pid int) (string, error) {
		if p, ok := byPID[pid]; ok && p.cwdFull != "" {
			return p.cwdFull, nil
		}
		return "", os.ErrNotExist
	}
	hermesReadEnv = func(pid int, key string) string {
		if p, ok := byPID[pid]; ok && key == "HERMES_HOME" {
			return p.envHome
		}
		return ""
	}
	t.Cleanup(func() {
		hermesListPIDs = oldList
		hermesReadCmdline = oldCmd
		hermesReadCWD = oldCWD
		hermesReadEnv = oldEnv
	})
}

// writeHermesStateDB creates a minimal state.db with one open local session.
func writeHermesStateDB(t *testing.T, home string, s hermesSession, lastMsgAt int64) {
	t.Helper()
	path := filepath.Join(home, "state.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`
		CREATE TABLE sessions (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL,
			title TEXT,
			model TEXT,
			cwd TEXT,
			input_tokens INTEGER DEFAULT 0,
			output_tokens INTEGER DEFAULT 0,
			cache_read_tokens INTEGER DEFAULT 0,
			api_call_count INTEGER DEFAULT 0,
			started_at REAL NOT NULL,
			ended_at REAL,
			archived INTEGER DEFAULT 0
		);
		CREATE TABLE messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			role TEXT NOT NULL,
			content TEXT,
			timestamp REAL NOT NULL,
			active INTEGER DEFAULT 1
		);
	`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(
		`INSERT INTO sessions (id, source, title, model, cwd, input_tokens, output_tokens, cache_read_tokens, api_call_count, started_at, ended_at, archived)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, 0)`,
		s.ID, s.Source, s.Title, s.Model, s.CWD, s.InTokens, s.OutTokens, s.CacheRead, s.APICalls, s.StartedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if lastMsgAt > 0 {
		_, err = db.Exec(
			`INSERT INTO messages (session_id, role, content, timestamp) VALUES (?, 'assistant', 'hi', ?)`,
			s.ID, float64(lastMsgAt),
		)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestHermesContextTokens(t *testing.T) {
	// Single-call session: in + cache_read ≈ CLI last_prompt (the 0%→1% case).
	if got := hermesContextTokens(&hermesSession{InTokens: 4440, CacheRead: 896, OutTokens: 64, APICalls: 1}); got != 5336 {
		t.Errorf("single call = %d, want 5336", got)
	}
	// Multi-call: average prompt-side tokens so cumulative cache does not
	// report multi-window totals.
	if got := hermesContextTokens(&hermesSession{InTokens: 22498, CacheRead: 740992, APICalls: 31}); got != (22498+740992)/31 {
		t.Errorf("multi call avg = %d, want %d", got, (22498+740992)/31)
	}
	// No prompt-side data: fall back to output-only lifetime when present.
	if got := hermesContextTokens(&hermesSession{OutTokens: 15}); got != 15 {
		t.Errorf("fallback = %d, want 15", got)
	}
	if got := hermesContextTokens(nil); got != 0 {
		t.Errorf("nil = %d, want 0", got)
	}
}

func TestHermesProbeUsageIncludesCacheRead(t *testing.T) {
	home := hermesFixtureHome(t)
	// Mirror the live "Exploring the tmon codebase" row: one API call with
	// cache so the popup can show 1% of a 1M window after rounding.
	writeHermesStateDB(t, home, hermesSession{
		ID: "sess1", Source: "tui", Title: "Exploring the tmon codebase",
		Model: "deepseek-v4-flash", CWD: "/home/guillaume/code/tmon",
		InTokens: 4440, OutTokens: 64, CacheRead: 896, APICalls: 1,
		StartedAt: float64(time.Now().Unix()),
	}, 0)
	// Window lookup needs a models cache entry.
	writeFile(t, filepath.Join(home, "models_dev_cache.json"),
		`{"deepseek":{"models":{"deepseek-v4-flash":{"limit":{"context":1000000}}}}}`)
	stubHermesProcs(t, []hermesProc{{
		pid: 42, cmdline: "hermes --tui",
		cwdFull: "/home/guillaume/code/tmon", cwd: "code/tmon",
		envHome: home,
	}})

	recs, err := (Hermes{}).Probe(hermesTestCfg(t))
	if err != nil || len(recs) != 1 {
		t.Fatalf("recs=%+v err=%v", recs, err)
	}
	u := recs[0].Usage
	if u.TokensUsed != 5336 {
		t.Errorf("TokensUsed = %d, want 5336 (in+cache_read)", u.TokensUsed)
	}
	if u.WindowTokens != 1000000 {
		t.Errorf("WindowTokens = %d, want 1000000", u.WindowTokens)
	}
	if got := u.ContextPct(); got != 1 {
		t.Errorf("ContextPct = %d, want 1 (rounded from 0.53%%)", got)
	}
}

func TestHermesGatewayOnlyYieldsNoRecords(t *testing.T) {
	hermesFixtureHome(t)
	stubHermesProcs(t, []hermesProc{{
		pid: 1, cmdline: "python -m hermes_cli.main gateway run",
	}})
	recs, err := (Hermes{}).Probe(hermesTestCfg(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 0 {
		t.Fatalf("records = %+v, want none for gateway-only", recs)
	}
}

func TestHermesCLISessionTitleProfileModel(t *testing.T) {
	home := hermesFixtureHome(t)
	writeHermesStateDB(t, home, hermesSession{
		ID: "sess1", Source: "tui", Title: "Root Cause of Gateway Auth",
		Model: "deepseek-v4-flash", CWD: "/home/u/code",
		InTokens: 1000, OutTokens: 200, StartedAt: float64(time.Now().Unix()),
	}, 0)
	stubHermesProcs(t, []hermesProc{{
		pid: 42, cmdline: "hermes --tui",
		cwdFull: "/home/u/code", cwd: "u/code",
		envHome: home,
	}})

	recs, err := (Hermes{}).Probe(hermesTestCfg(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("records = %+v, want 1", recs)
	}
	r := recs[0]
	if r.PID != 42 || r.Label != "Hermes" {
		t.Errorf("identity = %+v", r)
	}
	if r.Profile != "default" {
		t.Errorf("Profile = %q, want default", r.Profile)
	}
	if r.Title != "Root Cause of Gateway Auth" {
		t.Errorf("Title = %q", r.Title)
	}
	if r.Detail != "model:deepseek-v4-flash" {
		t.Errorf("Detail = %q", r.Detail)
	}
	// Context fill uses prompt-side tokens (input + cache_read), not lifetime
	// input+output — matching Hermes CLI's last_prompt_tokens semantics.
	if r.Usage.TokensUsed != 1000 {
		t.Errorf("TokensUsed = %d, want 1000 (in+cache, not in+out)", r.Usage.TokensUsed)
	}
	if r.Status != agent.StatusIdle {
		t.Errorf("Status = %s, want idle (no recent messages)", r.Status)
	}
}

func TestHermesNamedProfileFromEnv(t *testing.T) {
	home := hermesFixtureHome(t)
	coder := filepath.Join(home, "profiles", "coder")
	if err := os.MkdirAll(coder, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(coder, "config.yaml"), "model:\n  default: other\n")
	writeHermesStateDB(t, coder, hermesSession{
		ID: "c1", Source: "cli", Title: "Build feature",
		Model: "gpt-test", CWD: "/work",
		InTokens: 50, OutTokens: 10, StartedAt: float64(time.Now().Unix()),
	}, 0)
	stubHermesProcs(t, []hermesProc{{
		pid: 7, cmdline: "hermes chat",
		cwdFull: "/work", envHome: coder,
	}})

	recs, err := (Hermes{}).Probe(hermesTestCfg(t))
	if err != nil || len(recs) != 1 {
		t.Fatalf("recs=%+v err=%v", recs, err)
	}
	if recs[0].Profile != "coder" {
		t.Errorf("Profile = %q, want coder", recs[0].Profile)
	}
	if recs[0].Title != "Build feature" {
		t.Errorf("Title = %q", recs[0].Title)
	}
}

func TestHermesWorkingFromRecentMessage(t *testing.T) {
	home := hermesFixtureHome(t)
	now := time.Now().Unix()
	writeHermesStateDB(t, home, hermesSession{
		ID: "s", Source: "tui", Title: "Live",
		Model: "m", CWD: "/x", StartedAt: float64(now),
	}, now) // message just now
	stubHermesProcs(t, []hermesProc{{
		pid: 9, cmdline: "hermes", cwdFull: "/x", envHome: home,
	}})
	recs, err := (Hermes{}).Probe(hermesTestCfg(t))
	if err != nil || len(recs) != 1 {
		t.Fatalf("recs=%+v err=%v", recs, err)
	}
	if recs[0].Status != agent.StatusWorking {
		t.Errorf("Status = %s, want working", recs[0].Status)
	}
}

func TestHermesBlockedFromApprovals(t *testing.T) {
	home := hermesFixtureHome(t)
	writeHermesStateDB(t, home, hermesSession{
		ID: "s", Source: "cli", Title: "Approve me",
		Model: "m", CWD: "/x", StartedAt: float64(time.Now().Unix()),
	}, 0)
	stubHermesProcs(t, []hermesProc{{
		pid: 3, cmdline: "hermes chat", cwdFull: "/x", envHome: home,
	}})
	cfg := hermesTestCfg(t)
	apDir := filepath.Join(cfg.HookStateDir, "hermes")
	if err := os.MkdirAll(apDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(hermesApprovalFile{Pending: map[string]hermesApprovalEntry{
		"sess-key": {
			PatternKey: "rm_rf",
			Surface:    "cli",
			HermesHome: home,
			TS:         time.Now().Unix(),
		},
	}})
	writeFile(t, filepath.Join(apDir, "approvals.json"), string(body))

	recs, err := (Hermes{}).Probe(cfg)
	if err != nil || len(recs) != 1 {
		t.Fatalf("recs=%+v err=%v", recs, err)
	}
	if recs[0].Status != agent.StatusBlocked {
		t.Errorf("Status = %s, want blocked", recs[0].Status)
	}
	if recs[0].Detail != "approval:rm_rf" {
		t.Errorf("Detail = %q", recs[0].Detail)
	}
}

func TestHermesEnabledOnConfigOnly(t *testing.T) {
	home := hermesFixtureHome(t)
	// no gateway_state.json
	if !(Hermes{}).Enabled(config.Defaults()) {
		t.Error("want enabled with config.yaml present")
	}
	_ = home
	// empty dir
	empty := t.TempDir()
	old := hermesHome
	hermesHome = func() string { return empty }
	t.Cleanup(func() { hermesHome = old })
	if (Hermes{}).Enabled(config.Defaults()) {
		t.Error("want disabled with empty home")
	}
}

func TestIsHermesGateway(t *testing.T) {
	if !isHermesGateway("python -m hermes_cli.main gateway run") {
		t.Error("expected gateway run to match")
	}
	if !isHermesGateway("/path/hermes-gateway.service") {
		t.Error("expected hermes-gateway to match")
	}
	if isHermesGateway("hermes --tui") {
		t.Error("tui should not be gateway")
	}
	if isHermesGateway("hermes chat") {
		t.Error("chat should not be gateway")
	}
}

func TestProfileNameFromHome(t *testing.T) {
	root := "/home/u/.hermes"
	if g := profileNameFromHome(root, root); g != "default" {
		t.Errorf("default = %q", g)
	}
	if g := profileNameFromHome(root+"/profiles/coder", root); g != "coder" {
		t.Errorf("coder = %q", g)
	}
}

func TestHermesConfigModel(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.yaml"), "model:\n  base_url: x\n  default: deepseek-v4-flash\n  provider: deepseek\nagent:\n  max_turns: 1\n")
	if g := hermesConfigModel(home); g != "deepseek-v4-flash" {
		t.Errorf("model = %q", g)
	}
}

func TestHermesFreshCollect(t *testing.T) {
	home := hermesFixtureHome(t)
	writeHermesStateDB(t, home, hermesSession{
		ID: "s", Source: "tui", Title: "T",
		Model: "m", CWD: "/x", InTokens: 1, StartedAt: float64(time.Now().Unix()),
	}, 0)
	stubHermesProcs(t, []hermesProc{{
		pid: 99, cmdline: "hermes", cwdFull: "/x", envHome: home,
	}})
	oldReg := Registry
	Registry = []Connector{Hermes{}}
	t.Cleanup(func() { Registry = oldReg })
	oldAlive := procAlive
	procAlive = func(pid int) bool { return true }
	t.Cleanup(func() { procAlive = oldAlive })

	got := Collect(hermesTestCfg(t), time.Now())
	if len(got) != 1 || got[0].Title != "T" || got[0].Profile != "default" {
		t.Fatalf("collect = %+v", got)
	}
}

// Stale open state.db rows with a different CWD must not steal the live
// process's title (repro: new TUI in ~/code/tmon vs old "Gateway Auth" session).
func TestHermesStaleSessionCWDMismatchNoTitle(t *testing.T) {
	home := hermesFixtureHome(t)
	writeHermesStateDB(t, home, hermesSession{
		ID: "old", Source: "tui", Title: "Root Cause of Gateway Auth Failure",
		Model: "deepseek-v4-flash", CWD: "/home/u",
		InTokens: 100, OutTokens: 50, StartedAt: float64(time.Now().Add(-48 * time.Hour).Unix()),
	}, 0)
	stubHermesProcs(t, []hermesProc{{
		pid: 42, cmdline: "hermes --tui",
		cwdFull: "/home/u/code/tmon", cwd: "code/tmon",
		envHome: home,
	}})

	recs, err := (Hermes{}).Probe(hermesTestCfg(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("records = %+v, want 1", recs)
	}
	r := recs[0]
	if r.Title != "" {
		t.Errorf("Title = %q, want empty (stale session must not pair)", r.Title)
	}
	if r.CWD != "code/tmon" {
		t.Errorf("CWD = %q, want process cwd code/tmon (not stale session cwd)", r.CWD)
	}
	// Model may still come from config.yaml when no session is paired.
	if r.Detail != "model:deepseek-v4-flash" {
		t.Errorf("Detail = %q, want model from config fallback", r.Detail)
	}
}

// A freshly opened TUI has no state.db session row yet — Hermes persists the
// row lazily on the first prompt (see _ensure_session_db_row). The connector
// must still report the configured model's context window at 0% used so the
// popup shows the usage bar instead of "context: ?".
func TestHermesFreshTUIReportsEmptyUsageBar(t *testing.T) {
	home := hermesFixtureHome(t)
	// A distinct model so the package-level window cache stays isolated.
	writeFile(t, filepath.Join(home, "config.yaml"), "model:\n  default: fresh-model\n")
	writeFile(t, filepath.Join(home, "models_dev_cache.json"),
		`{"p": {"id": "p", "models": {"fresh-model": {"id": "fresh-model", "limit": {"context": 500000}}}}}`)
	// No state.db: the fresh TUI has no session row to pair with.
	stubHermesProcs(t, []hermesProc{{
		pid: 42, cmdline: "hermes --tui",
		cwdFull: "/home/u/code/tmon", cwd: "code/tmon",
		envHome: home,
	}})

	recs, err := (Hermes{}).Probe(hermesTestCfg(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("records = %+v, want 1", recs)
	}
	r := recs[0]
	if r.Usage.TokensUsed != 0 || r.Usage.WindowTokens != 500000 {
		t.Errorf("Usage = %+v, want 0/500000 (empty bar on the model's window)", r.Usage)
	}
	if r.Detail != "model:fresh-model" {
		t.Errorf("Detail = %q, want config model fallback", r.Detail)
	}
}

func TestPairHermesSessionByCWD(t *testing.T) {
	sessions := []hermesSession{
		{ID: "a", Title: "Wrong", CWD: "/home/u"},
		{ID: "b", Title: "Right", CWD: "/home/u/code/tmon"},
	}
	p := hermesProc{cwdFull: "/home/u/code/tmon", cwd: "code/tmon"}
	got := pairHermesSession(p, "", sessions, 1)
	if got == nil || got.Title != "Right" {
		t.Fatalf("got %+v, want Right by CWD", got)
	}
}

func TestPairHermesSessionNoGuessOnCWDMismatch(t *testing.T) {
	sessions := []hermesSession{
		{ID: "stale", Title: "Gateway Auth Failure", CWD: "/home/u"},
	}
	p := hermesProc{cwdFull: "/home/u/code/tmon", cwd: "code/tmon"}
	if got := pairHermesSession(p, "", sessions, 1); got != nil {
		t.Fatalf("got %+v, want nil when CWD mismatches", got)
	}
}

func TestPairHermesSessionSoleEmptyCWDFallback(t *testing.T) {
	sessions := []hermesSession{
		{ID: "only", Title: "Solo", CWD: ""},
	}
	p := hermesProc{cwdFull: "/work", cwd: "work"}
	got := pairHermesSession(p, "", sessions, 1)
	if got == nil || got.Title != "Solo" {
		t.Fatalf("got %+v, want Solo (sole open session, no recorded CWD)", got)
	}
	// Multiple live PIDs: never guess even with empty CWD.
	if got := pairHermesSession(p, "", sessions, 2); got != nil {
		t.Fatalf("got %+v, want nil when multiple PIDs and no CWD match", got)
	}
}
