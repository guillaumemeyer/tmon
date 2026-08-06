package dashboard

import (
	"testing"
	"time"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
	"github.com/guillaumemeyer/tmon/internal/detect"
	"github.com/guillaumemeyer/tmon/internal/git"
	"github.com/guillaumemeyer/tmon/internal/pane"
)

// refreshFixture points a config at a fresh state dir, saves the given
// agents to it, and stubs the full-reload seams with fixed /proc, pane map
// and blocked-check fakes.
func refreshFixture(t *testing.T, states []agent.AgentState, blocked map[string]string) (config.Config, *pane.Map) {
	t.Helper()
	cfg := config.Defaults()
	cfg.StateDir = t.TempDir()
	sf := agent.NewState()
	sf.Agents = states
	if err := sf.Save(cfg.StateFilePath()); err != nil {
		t.Fatal(err)
	}

	oldDetect := refreshDetect
	refreshDetect = func() ([]detect.Agent, error) {
		return []detect.Agent{
			{PID: 10, Label: "Grok", Cmdline: "grok build", CWD: "code/tmon"},
		}, nil
	}
	t.Cleanup(func() { refreshDetect = oldDetect })

	pm, err := pane.Parse("tty1|main:0.0|10|$1|main|0|shell|0\ntty2|main:0.2|999|$1|main|0|shell|2")
	if err != nil {
		t.Fatal(err)
	}
	oldPM := refreshPaneMap
	refreshPaneMap = func() (*pane.Map, error) { return pm, nil }
	t.Cleanup(func() { refreshPaneMap = oldPM })

	oldBlocked := refreshBlocked
	refreshBlocked = func(paneTarget string) (string, bool) {
		if reason, ok := blocked[paneTarget]; ok {
			return reason, true
		}
		return "", false
	}
	t.Cleanup(func() { refreshBlocked = oldBlocked })

	// Git resolution must not reach the real repository the test binary
	// runs in; specific tests override these fakes with fixed workspaces.
	oldFind := gitFind
	gitFind = func(dir string) (*git.Workspace, bool) { return nil, false }
	t.Cleanup(func() { gitFind = oldFind })
	oldPR := gitPR
	gitPR = func(root, branch string, ttl time.Duration) (git.PR, bool) {
		return git.PR{}, false
	}
	t.Cleanup(func() { gitPR = oldPR })
	return cfg, pm
}

func TestLoadFullMergesConnectorOnlyAgents(t *testing.T) {
	cfg, _ := refreshFixture(t, []agent.AgentState{
		{PID: 10, Label: "Grok", Status: agent.StatusWorking, Detail: "tool:Bash", CWD: "code/tmon", Pane: "main:0.0", LastTs: 1700000000, Title: "Refactor login"},
		{PID: 999, Label: "Hermes", Status: agent.StatusBlocked, Detail: "approval:rm_rf", CWD: "code/tmon", Pane: "main:0.2", Title: "Root Cause", Profile: "default"},
	}, nil)

	data, err := loadFull(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Rows) != 2 {
		t.Fatalf("rows = %+v, want 2 (detected + connector-only)", data.Rows)
	}

	// The detected agent keeps its state overlay.
	var grok, hermes *Row
	for i := range data.Rows {
		switch data.Rows[i].PID {
		case 10:
			grok = &data.Rows[i]
		case 999:
			hermes = &data.Rows[i]
		}
	}
	if grok == nil || grok.Detail != "tool:Bash" || grok.Status != agent.StatusWorking || grok.LastTs != 1700000000 {
		t.Errorf("Grok row = %+v, want detail/lastTs overlay from state", grok)
	}
	if grok.Title != "Refactor login" {
		t.Errorf("Grok row Title = %q, want Refactor login from state", grok.Title)
	}
	if hermes == nil {
		t.Fatal("connector-only Hermes agent missing from rows")
	}
	if hermes.Label != "Hermes" || hermes.Status != agent.StatusBlocked || hermes.Detail != "approval:rm_rf" {
		t.Errorf("Hermes row = %+v, want state-derived Hermes blocked", hermes)
	}
	if hermes.Title != "Root Cause" {
		t.Errorf("Hermes row Title = %q, want Root Cause from state", hermes.Title)
	}
	if hermes.Profile != "default" {
		t.Errorf("Hermes row Profile = %q, want default from state", hermes.Profile)
	}
	// The connector-only row is parsed back into pane components and groups
	// under the same session id as the detected agent.
	if hermes.Pane != "main:0.2" || hermes.SessionName != "main" || hermes.SessionID != "1" || hermes.WindowIndex != "0" || hermes.PaneIndex != "2" {
		t.Errorf("Hermes pane fields = %+v, want main:0.2 / session 1", hermes)
	}
}

func TestLoadFullBlockedReasonOverlay(t *testing.T) {
	cfg, _ := refreshFixture(t, []agent.AgentState{
		{PID: 10, Label: "Grok", Status: agent.StatusIdle, CWD: "code/tmon", Pane: "main:0.0"},
	}, map[string]string{"main:0.0": "[y/N]"})

	data, err := loadFull(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Rows) != 1 {
		t.Fatalf("rows = %+v, want 1", data.Rows)
	}
	r := data.Rows[0]
	if r.Status != agent.StatusBlocked {
		t.Errorf("status = %v, want blocked (live pane check overrides)", r.Status)
	}
	if r.BlockedReason != "[y/N]" {
		t.Errorf("blocked reason = %q, want [y/N]", r.BlockedReason)
	}
}

func TestLoadFullUnpanedConnectorOnlyAgent(t *testing.T) {
	cfg, _ := refreshFixture(t, []agent.AgentState{
		{PID: 999, Label: "CodeBuddy", Status: agent.StatusIdle, Detail: "session:4242", CWD: "code/tmon"},
	}, nil)

	data, err := loadFull(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Rows) != 2 {
		t.Fatalf("rows = %+v, want detected + unpaned connector-only", data.Rows)
	}
	var r *Row
	for i := range data.Rows {
		if data.Rows[i].PID == 999 {
			r = &data.Rows[i]
		}
	}
	if r == nil {
		t.Fatal("CodeBuddy row missing")
	}
	if r.Pane != "" || r.SessionID != "?" || r.SessionName != "" {
		t.Errorf("unpaned row = %+v, want empty pane and unknown session", r)
	}
}

func TestRowFromAgentStateDefaultsStatus(t *testing.T) {
	r := rowFromAgentState(agent.AgentState{PID: 5, Label: "Aider"})
	if r.Status != agent.StatusIdle {
		t.Fatalf("status = %v, want idle default", r.Status)
	}
}

func TestLoadFullGitEnrichment(t *testing.T) {
	cfg, _ := refreshFixture(t, []agent.AgentState{
		{PID: 10, Label: "Grok", Status: agent.StatusWorking, CWD: "code/tmon", Pane: "main:0.0"},
		{PID: 999, Label: "Hermes", Status: agent.StatusIdle, CWD: "/home/u/code/tmon", Pane: "main:0.2"},
	}, nil)

	oldFind := gitFind
	gitFind = func(dir string) (*git.Workspace, bool) {
		if dir == "/home/u/code/tmon" {
			return &git.Workspace{Root: "/home/u/code/tmon", Branch: "main"}, true
		}
		return nil, false
	}
	t.Cleanup(func() { gitFind = oldFind })
	oldPR := gitPR
	gitPR = func(root, branch string, ttl time.Duration) (git.PR, bool) {
		if root == "/home/u/code/tmon" && branch == "main" {
			return git.PR{Number: "42", Title: "Fix"}, true
		}
		return git.PR{}, false
	}
	t.Cleanup(func() { gitPR = oldPR })

	data, err := loadFull(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var hermes, grok *Row
	for i := range data.Rows {
		switch data.Rows[i].PID {
		case 999:
			hermes = &data.Rows[i]
		case 10:
			grok = &data.Rows[i]
		}
	}
	if hermes == nil {
		t.Fatal("Hermes row missing")
	}
	if hermes.GitRoot != "/home/u/code/tmon" || hermes.Branch != "main" || hermes.PR != "42" {
		t.Errorf("Hermes git = %q / %q / %q, want root / main / 42", hermes.GitRoot, hermes.Branch, hermes.PR)
	}
	// A short-form CWD never resolves git (would point at the dashboard's
	// own process directory).
	if grok == nil || grok.Branch != "" || grok.GitRoot != "" {
		t.Errorf("Grok git = %+v, want empty (short CWD)", grok)
	}
}

func TestLoadFullHidesByLabel(t *testing.T) {
	cfg, _ := refreshFixture(t, []agent.AgentState{
		{PID: 10, Label: "Grok", Status: agent.StatusIdle, CWD: "code/tmon", Pane: "main:0.0"},
		{PID: 999, Label: "CodeBuddy", Status: agent.StatusIdle, CWD: "code/tmon"},
	}, nil)
	cfg.HidePatterns = []string{"codebuddy"}

	data, err := loadFull(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Rows) != 1 || data.Rows[0].Label != "Grok" {
		t.Fatalf("rows = %+v, want only Grok (CodeBuddy hidden)", data.Rows)
	}
}

func TestLoadFullHidesBySession(t *testing.T) {
	cfg, _ := refreshFixture(t, []agent.AgentState{
		{PID: 10, Label: "Grok", Status: agent.StatusIdle, CWD: "code/tmon", Pane: "main:0.0"},
		{PID: 999, Label: "Hermes", Status: agent.StatusIdle, CWD: "code/tmon", Pane: "main:0.2"},
	}, nil)
	cfg.HidePatterns = []string{"main"}

	data, err := loadFull(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Rows) != 0 {
		t.Fatalf("rows = %+v, want none (all agents in hidden session main)", data.Rows)
	}
}
