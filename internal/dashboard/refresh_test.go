package dashboard

import (
	"testing"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
	"github.com/guillaumemeyer/tmon/internal/detect"
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
	sf.Frame = 3
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
	return cfg, pm
}

func TestLoadFullMergesConnectorOnlyAgents(t *testing.T) {
	cfg, _ := refreshFixture(t, []agent.AgentState{
		{PID: 10, Label: "Grok", Status: agent.StatusActive, Detail: "tool:Bash", CWD: "code/tmon", Pane: "main:0.0", LastTs: 1700000000},
		{PID: 999, Label: "Hermes", Status: agent.StatusBlocked, Detail: "3 active agents", CWD: "code/tmon", Pane: "main:0.2"},
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
	if grok == nil || grok.Detail != "tool:Bash" || grok.Status != agent.StatusActive || grok.LastTs != 1700000000 {
		t.Errorf("Grok row = %+v, want detail/lastTs overlay from state", grok)
	}
	if hermes == nil {
		t.Fatal("connector-only Hermes agent missing from rows")
	}
	if hermes.Label != "Hermes" || hermes.Status != agent.StatusBlocked || hermes.Detail != "3 active agents" {
		t.Errorf("Hermes row = %+v, want state-derived Hermes blocked", hermes)
	}
	// The connector-only row is parsed back into pane components and groups
	// under the same session id as the detected agent.
	if hermes.Pane != "main:0.2" || hermes.SessionName != "main" || hermes.SessionID != "1" || hermes.WindowIndex != "0" || hermes.PaneIndex != "2" {
		t.Errorf("Hermes pane fields = %+v, want main:0.2 / session 1", hermes)
	}
}

func TestLoadFullBlockedReasonOverlay(t *testing.T) {
	cfg, _ := refreshFixture(t, []agent.AgentState{
		{PID: 10, Label: "Grok", Status: agent.StatusRunning, CWD: "code/tmon", Pane: "main:0.0"},
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
		{PID: 999, Label: "CodeBuddy", Status: agent.StatusRunning, Detail: "session:4242", CWD: "code/tmon"},
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
	if r.Status != agent.StatusRunning {
		t.Fatalf("status = %v, want running default", r.Status)
	}
}
