package poll

import (
	"fmt"
	"testing"
	"time"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
	"github.com/guillaumemeyer/tmon/internal/connector"
	"github.com/guillaumemeyer/tmon/internal/detect"
)

func testConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Defaults()
	cfg.StateDir = t.TempDir()
	cfg.PollIntervalMs = 3000
	return cfg
}

// stubDetect replaces the /proc signature scan with a fixed agent list for
// the duration of the test.
func stubDetect(t *testing.T, agents []detect.Agent) {
	t.Helper()
	old := detectAgents
	detectAgents = func() ([]detect.Agent, error) { return agents, nil }
	t.Cleanup(func() { detectAgents = old })
}

func TestAuthoritativeWinsOverBlockedPane(t *testing.T) {
	// The pane looks blocked, but the connector says working: the agent's own
	// event must win.
	stubDetect(t, []detect.Agent{{PID: 42, Label: "Grok", CWD: "code/tmon"}})
	old := paneBlocked
	paneBlocked = func(string) bool { return true }
	t.Cleanup(func() { paneBlocked = old })

	cfg := testConfig(t)
	records := []connector.Record{{
		PID: 42, Label: "Grok", Status: agent.StatusWorking, Detail: "tool:Bash", At: time.Now(),
	}}
	res, err := run(cfg, nil, false, records)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Agents) != 1 {
		t.Fatalf("agents = %+v, want 1", res.Agents)
	}
	if res.Agents[0].Status != agent.StatusWorking {
		t.Errorf("status = %q, want working (authoritative wins over blocked pane)", res.Agents[0].Status)
	}
	if res.Agents[0].Detail != "tool:Bash" {
		t.Errorf("Detail = %q, want tool:Bash", res.Agents[0].Detail)
	}
}

func TestPaneBlockedFallbackWithoutConnector(t *testing.T) {
	// No connector record: the pane-based check is the only signal.
	stubDetect(t, []detect.Agent{{PID: 42, Label: "Grok", CWD: "code/tmon"}})
	old := paneBlocked
	paneBlocked = func(string) bool { return true }
	t.Cleanup(func() { paneBlocked = old })

	res, err := run(testConfig(t), nil, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Agents) != 1 || res.Agents[0].Status != agent.StatusBlocked {
		t.Fatalf("agents = %+v, want the blocked-pane fallback", res.Agents)
	}
}

func TestConnectorOnlyInjection(t *testing.T) {
	// A record whose PID the /proc signature table misses (e.g. the Hermes
	// gateway) becomes a new agent.
	stubDetect(t, nil)
	cfg := testConfig(t)
	records := []connector.Record{{
		PID: 7777, Label: "Hermes", Status: agent.StatusBlocked, Detail: "permission", At: time.Now(),
	}}
	res, err := run(cfg, nil, false, records)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Agents) != 1 {
		t.Fatalf("agents = %+v, want the connector-only agent injected", res.Agents)
	}
	a := res.Agents[0]
	if a.PID != 7777 || a.Label != "Hermes" || a.Status != agent.StatusBlocked || a.Detail != "permission" {
		t.Errorf("injected agent = %+v, want Hermes blocked permission", a)
	}
	if len(res.Statuses) != 1 || res.Statuses[0] != agent.StatusBlocked {
		t.Errorf("statuses = %+v, want [blocked]", res.Statuses)
	}
}

func TestUsageRidesConnectorRecord(t *testing.T) {
	// A record's usage stats land on the matching snapshot agent, for both
	// a detected baseline PID and a connector-only PID.
	stubDetect(t, []detect.Agent{{PID: 42, Label: "Grok", CWD: "code/tmon"}})
	cfg := testConfig(t)
	records := []connector.Record{
		{PID: 42, Label: "Grok", Status: agent.StatusWorking, Detail: "tool:Bash", At: time.Now(), Usage: agent.Usage{TokensUsed: 13025, WindowTokens: 262144}},
		{PID: 7777, Label: "Hermes", Status: agent.StatusIdle, Detail: "gateway", At: time.Now(), Usage: agent.Usage{TokensUsed: 5000}},
	}
	res, err := run(cfg, nil, false, records)
	if err != nil {
		t.Fatal(err)
	}
	byPID := map[int]agent.AgentState{}
	for _, a := range res.Agents {
		byPID[a.PID] = a
	}
	if u := byPID[42].Usage; u == nil || u.TokensUsed != 13025 || u.WindowTokens != 262144 {
		t.Errorf("Grok usage = %+v, want tokens 13025 window 262144", u)
	}
	if u := byPID[7777].Usage; u == nil || u.TokensUsed != 5000 {
		t.Errorf("Hermes usage = %+v, want tokens 5000", u)
	}

	// A record with empty usage must not attach a zero-value pointer.
	res2, err := run(cfg, nil, false, []connector.Record{
		{PID: 42, Label: "Grok", Status: agent.StatusIdle, At: time.Now()},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range res2.Agents {
		if a.Usage != nil {
			t.Errorf("PID %d: expected nil usage, got %+v", a.PID, a.Usage)
		}
	}
}

func TestMergeBaselineAndConnectorOnly(t *testing.T) {
	stubDetect(t, []detect.Agent{{PID: 42, Label: "Grok", CWD: "code/tmon"}})
	cfg := testConfig(t)
	records := []connector.Record{
		{PID: 42, Label: "Grok", Status: agent.StatusWorking, Detail: "tool:Bash", At: time.Now()},
		{PID: 7777, Label: "Hermes", Status: agent.StatusIdle, Detail: "gateway", At: time.Now()},
	}
	res, err := run(cfg, nil, false, records)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Agents) != 2 {
		t.Fatalf("agents = %+v, want 2", res.Agents)
	}
	byPID := map[int]agent.AgentState{}
	for _, a := range res.Agents {
		byPID[a.PID] = a
	}
	if a := byPID[42]; a.Detail != "tool:Bash" || a.Status != agent.StatusWorking {
		t.Errorf("baseline agent = %+v, want working tool:Bash", a)
	}
	if a := byPID[7777]; a.Label != "Hermes" || a.Status != agent.StatusIdle || a.Detail != "gateway" {
		t.Errorf("connector-only agent = %+v, want Hermes idle gateway", a)
	}
}

func TestStateFilePersistedWithConnectorDetail(t *testing.T) {
	stubDetect(t, nil)
	cfg := testConfig(t)
	records := []connector.Record{{
		PID: 7777, Label: "Hermes", Status: agent.StatusWorking, Detail: "phase:reasoning", At: time.Now(),
		Title: "gateway session",
	}}
	if _, err := run(cfg, nil, false, records); err != nil {
		t.Fatal(err)
	}
	sf, err := agent.LoadState(cfg.StateFilePath())
	if err != nil {
		t.Fatal(err)
	}
	if len(sf.Agents) != 1 || sf.Agents[0].Detail != "phase:reasoning" {
		t.Fatalf("state file agents = %+v, want detail persisted", sf.Agents)
	}
	if sf.Agents[0].Title != "gateway session" {
		t.Fatalf("state file agents = %+v, want title persisted", sf.Agents)
	}
}

// ─── Notifications ───────────────────────────────────────────────────────────

func TestNotifyTransitionsOnChangeOnly(t *testing.T) {
	var got []string
	old := announce
	announce = func(label string, old, new agent.Status, cwd string) {
		got = append(got, fmt.Sprintf("%s:%s->%s", label, old, new))
	}
	t.Cleanup(func() { announce = old })

	prev := map[int]agent.Status{
		1: agent.StatusIdle,
		2: agent.StatusWorking,
		3: agent.StatusIdle,
	}
	snap := []agent.AgentState{
		{PID: 1, Label: "Grok", Status: agent.StatusWorking},   // idle→working: announce
		{PID: 2, Label: "Claude", Status: agent.StatusWorking}, // unchanged: silent
		{PID: 3, Label: "Hermes", Status: agent.StatusBlocked}, // idle→blocked: announce
		{PID: 4, Label: "Codex", Status: agent.StatusIdle},     // new agent: silent
	}
	notifyTransitions(prev, snap)
	if len(got) != 2 {
		t.Fatalf("announced %v, want 2", got)
	}
}

func TestTransitionMessageFilter(t *testing.T) {
	cases := []struct {
		old, new agent.Status
		want     string
	}{
		{agent.StatusIdle, agent.StatusWorking, "Grok is now working"},
		{agent.StatusWorking, agent.StatusBlocked, ""}, // blocked is silent
		{agent.StatusBlocked, agent.StatusIdle, ""},    // idling is silent
		{agent.StatusWorking, agent.StatusWorking, ""}, // no transition
	}
	for _, c := range cases {
		if got := transitionMessage("Grok", c.old, c.new, ""); got != c.want {
			t.Errorf("transitionMessage(%s->%s) = %q, want %q", c.old, c.new, got, c.want)
		}
	}
	if got := transitionMessage("Grok", agent.StatusIdle, agent.StatusWorking, "code/tmon"); got != "Grok is now working in code/tmon" {
		t.Errorf("with cwd = %q, want \"Grok is now working in code/tmon\"", got)
	}
}
