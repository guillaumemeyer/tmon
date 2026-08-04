package poll

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
	"github.com/guillaumemeyer/tmon/internal/connector"
	"github.com/guillaumemeyer/tmon/internal/detect"
	"github.com/guillaumemeyer/tmon/internal/theme"
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
	notifyTransitions(prev, snap, false)
	if len(got) != 2 {
		t.Fatalf("announced %v, want 2", got)
	}
}

func TestNotifyBellOnBlockedTransition(t *testing.T) {
	var bells int
	old := ringBell
	ringBell = func() { bells++ }
	t.Cleanup(func() { ringBell = old })
	oldAnn := announce
	announce = func(string, agent.Status, agent.Status, string) {}
	t.Cleanup(func() { announce = oldAnn })

	prev := map[int]agent.Status{1: agent.StatusWorking}
	snap := []agent.AgentState{{PID: 1, Label: "Grok", Status: agent.StatusBlocked}}

	// working→blocked with the bell enabled: rings once.
	notifyTransitions(prev, snap, true)
	if bells != 1 {
		t.Fatalf("bells = %d, want 1 on working→blocked", bells)
	}

	// Disabled: no bell.
	notifyTransitions(prev, snap, false)
	if bells != 1 {
		t.Fatalf("bells after disabled = %d, want still 1", bells)
	}

	// blocked→working with the bell enabled: no bell (only blocked rings).
	notifyTransitions(map[int]agent.Status{1: agent.StatusBlocked}, []agent.AgentState{{PID: 1, Label: "Grok", Status: agent.StatusWorking}}, true)
	if bells != 1 {
		t.Fatalf("bells after blocked→working = %d, want still 1", bells)
	}

	// Steady state: no transition, no bell.
	notifyTransitions(map[int]agent.Status{1: agent.StatusBlocked}, snap, true)
	if bells != 1 {
		t.Fatalf("bells after steady blocked = %d, want still 1", bells)
	}

	// A first sighting of a blocked agent is silent (not a transition).
	notifyTransitions(nil, snap, true)
	if bells != 1 {
		t.Fatalf("bells after first sighting = %d, want still 1", bells)
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

// ─── Pane tint ────────────────────────────────────────────────────────────

// tintCalls swaps in a recording tintPane and returns the recorded
// "pane|style" calls.
func tintCalls(t *testing.T) *[]string {
	t.Helper()
	calls := &[]string{}
	old := tintPane
	tintPane = func(pane, style string) { *calls = append(*calls, pane+"|"+style) }
	t.Cleanup(func() { tintPane = old })
	return calls
}

func TestApplyTintsChangedAndUnchanged(t *testing.T) {
	calls := tintCalls(t)
	prev := []agent.AgentState{
		{PID: 1, Label: "Grok", Pane: "s:1.1", Status: agent.StatusWorking},
		{PID: 2, Label: "Claude", Pane: "s:1.2", Status: agent.StatusBlocked},
	}
	snap := []agent.AgentState{
		{PID: 1, Label: "Grok", Pane: "s:1.1", Status: agent.StatusBlocked},  // changed: tint
		{PID: 2, Label: "Claude", Pane: "s:1.2", Status: agent.StatusBlocked}, // unchanged: skip
	}
	applyTints(theme.Default.Palette, prev, snap)
	want := []string{"s:1.1|bg=#592f00,fg=default"}
	if !reflect.DeepEqual(*calls, want) {
		t.Fatalf("tint calls = %v, want %v", *calls, want)
	}
}

func TestApplyTintsWorkingTint(t *testing.T) {
	calls := tintCalls(t)
	prev := []agent.AgentState{{PID: 1, Label: "Grok", Pane: "s:1.1", Status: agent.StatusIdle}}
	snap := []agent.AgentState{{PID: 1, Label: "Grok", Pane: "s:1.1", Status: agent.StatusWorking}}
	applyTints(theme.Default.Palette, prev, snap)
	want := []string{"s:1.1|bg=#002c00,fg=default"} // green darkened
	if !reflect.DeepEqual(*calls, want) {
		t.Fatalf("tint calls = %v, want %v", *calls, want)
	}
}

func TestApplyTintsIdleClears(t *testing.T) {
	calls := tintCalls(t)
	prev := []agent.AgentState{{PID: 1, Label: "Grok", Pane: "s:1.1", Status: agent.StatusBlocked}}
	snap := []agent.AgentState{{PID: 1, Label: "Grok", Pane: "s:1.1", Status: agent.StatusIdle}}
	applyTints(theme.Default.Palette, prev, snap)
	want := []string{"s:1.1|bg=default,fg=default"}
	if !reflect.DeepEqual(*calls, want) {
		t.Fatalf("tint calls = %v, want %v", *calls, want)
	}
}

func TestApplyTintsExitRestores(t *testing.T) {
	calls := tintCalls(t)
	prev := []agent.AgentState{{PID: 1, Label: "Grok", Pane: "s:1.1", Status: agent.StatusWorking}}
	applyTints(theme.Default.Palette, prev, nil)
	want := []string{"s:1.1|bg=default,fg=default"}
	if !reflect.DeepEqual(*calls, want) {
		t.Fatalf("tint calls = %v, want %v", *calls, want)
	}
}

func TestApplyTintsNewAgent(t *testing.T) {
	// A brand-new agent landing in idle is left alone (the pane was never
	// tinted by us); one reported blocked/working on first sight tints.
	calls := tintCalls(t)
	applyTints(theme.Default.Palette, nil, []agent.AgentState{
		{PID: 9, Label: "Codex", Pane: "s:2.1", Status: agent.StatusIdle},
	})
	if len(*calls) != 0 {
		t.Fatalf("new idle agent tinted: %v, want no calls", *calls)
	}

	applyTints(theme.Default.Palette, nil, []agent.AgentState{
		{PID: 9, Label: "Codex", Pane: "s:2.1", Status: agent.StatusBlocked},
	})
	want := []string{"s:2.1|bg=#592f00,fg=default"}
	if !reflect.DeepEqual(*calls, want) {
		t.Fatalf("new blocked agent tint calls = %v, want %v", *calls, want)
	}
}

func TestApplyTintsSkipsUnmappedPanes(t *testing.T) {
	// "?" and empty panes (outside tmux, or unresolved) never reach tintPane.
	calls := tintCalls(t)
	prev := []agent.AgentState{
		{PID: 1, Label: "Grok", Pane: "?", Status: agent.StatusWorking},
		{PID: 2, Label: "Claude", Pane: "", Status: agent.StatusBlocked},
	}
	snap := []agent.AgentState{
		{PID: 1, Label: "Grok", Pane: "?", Status: agent.StatusBlocked},
		{PID: 2, Label: "Claude", Pane: "", Status: agent.StatusIdle},
	}
	applyTints(theme.Default.Palette, prev, snap)
	if len(*calls) != 0 {
		t.Fatalf("unmapped panes tinted: %v, want no calls", *calls)
	}
}

func TestTintDisabledNoOp(t *testing.T) {
	// With @tmon-pane-tint off (the default) the poll loop never tints,
	// even though agents and statuses are flowing.
	stubDetect(t, []detect.Agent{{PID: 42, Label: "Grok", CWD: "code/tmon"}})
	calls := tintCalls(t)
	cfg := testConfig(t) // PaneTint defaults to false
	if _, err := run(cfg, nil, false, nil); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 {
		t.Fatalf("tint calls with option disabled = %v, want none", *calls)
	}
}
