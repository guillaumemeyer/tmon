package poll

import (
	"reflect"
	"testing"
	"time"

	"github.com/guillaumemeyer/tmon/internal/agent"
	"github.com/guillaumemeyer/tmon/internal/config"
	"github.com/guillaumemeyer/tmon/internal/connector"
	"github.com/guillaumemeyer/tmon/internal/detect"
	"github.com/guillaumemeyer/tmon/internal/theme"
	"github.com/guillaumemeyer/tmon/internal/worker"
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
	res, err := run(cfg, records)
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

	res, err := run(testConfig(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Agents) != 1 || res.Agents[0].Status != agent.StatusBlocked {
		t.Fatalf("agents = %+v, want the blocked-pane fallback", res.Agents)
	}
}

func TestSeedFromStateDetectsWorking(t *testing.T) {
	stubDetect(t, []detect.Agent{{PID: 42, Label: "Grok", CWD: "code"}})
	oldB := paneBlocked
	paneBlocked = func(string) bool { return false }
	t.Cleanup(func() { paneBlocked = oldB })

	cfg := testConfig(t)
	// Prior poll left a baseline in state.json
	sf := agent.NewState()
	sf.Agents = []agent.AgentState{{
		PID: 42, Label: "Grok", Status: agent.StatusIdle, CPU: 1000, IO: 0, CWD: "code",
	}}
	if err := sf.Save(cfg.StateFilePath()); err != nil {
		t.Fatal(err)
	}

	oldCPU, oldIO := readCPU, readIO
	readCPU = func(pid int) (int64, error) { return 1200, nil } // +200 ≥ 150
	readIO = func(pid int) (int64, error) { return 0, nil }
	t.Cleanup(func() { readCPU, readIO = oldCPU, oldIO })

	res, err := run(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Agents) != 1 || res.Agents[0].Status != agent.StatusWorking {
		t.Fatalf("agents = %+v, want working after seed from state", res.Agents)
	}
}

func TestNoStateFirstPollIsIdle(t *testing.T) {
	// Regression: first ever sighting stays idle (baseline poll).
	stubDetect(t, []detect.Agent{{PID: 42, Label: "Grok", CWD: "code"}})
	oldB := paneBlocked
	paneBlocked = func(string) bool { return false }
	t.Cleanup(func() { paneBlocked = oldB })
	cfg := testConfig(t)
	oldCPU, oldIO := readCPU, readIO
	readCPU = func(int) (int64, error) { return 5000, nil }
	readIO = func(int) (int64, error) { return 0, nil }
	t.Cleanup(func() { readCPU, readIO = oldCPU, oldIO })

	res, err := run(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Agents[0].Status != agent.StatusIdle {
		t.Errorf("cold start = %q, want idle", res.Agents[0].Status)
	}
}

func TestConnectorOnlyInjection(t *testing.T) {
	// A record whose PID the /proc signature table misses becomes a new agent.
	stubDetect(t, nil)
	cfg := testConfig(t)
	records := []connector.Record{{
		PID: 7777, Label: "Hermes", Status: agent.StatusBlocked, Detail: "approval:rm_rf", At: time.Now(),
		Profile: "default", Title: "Fix build",
	}}
	res, err := run(cfg, records)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Agents) != 1 {
		t.Fatalf("agents = %+v, want the connector-only agent injected", res.Agents)
	}
	a := res.Agents[0]
	if a.PID != 7777 || a.Label != "Hermes" || a.Status != agent.StatusBlocked || a.Detail != "approval:rm_rf" {
		t.Errorf("injected agent = %+v, want Hermes blocked approval", a)
	}
	if a.Profile != "default" || a.Title != "Fix build" {
		t.Errorf("profile/title = %q/%q, want default/Fix build", a.Profile, a.Title)
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
		{PID: 7777, Label: "Hermes", Status: agent.StatusIdle, Detail: "model:m", At: time.Now(), Profile: "coder", Usage: agent.Usage{TokensUsed: 5000, WindowTokens: 100000}},
	}
	res, err := run(cfg, records)
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
	if u := byPID[7777].Usage; u == nil || u.TokensUsed != 5000 || u.WindowTokens != 100000 {
		t.Errorf("Hermes usage = %+v, want tokens 5000 window 100000", u)
	}
	if byPID[7777].Profile != "coder" {
		t.Errorf("Hermes profile = %q, want coder", byPID[7777].Profile)
	}

	// A record with empty usage must not attach a zero-value pointer.
	res2, err := run(cfg, []connector.Record{
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

func TestAttachQuotaFromUsageFile(t *testing.T) {
	stubDetect(t, nil)
	cfg := testConfig(t)

	// Simulate the worker's usage.json output. Build the reset time from a
	// local instant so the "15:00" rendering is zone-independent.
	reset := time.Date(2026, 8, 6, 15, 0, 0, 0, time.Local).Format(time.RFC3339)
	if err := worker.SaveUsageFile(cfg.StateDir, worker.UsageFile{
		SchemaVersion: worker.SchemaVersion,
		GeneratedAt:   time.Now(),
		Quota: map[string]worker.Quota{
			"claude": {Pct: 38, Label: "Session (5-hour)", ResetAt: reset},
		},
	}); err != nil {
		t.Fatal(err)
	}

	records := []connector.Record{{
		PID: 42, Label: "Claude", Status: agent.StatusWorking, Detail: "tool:Bash", At: time.Now(),
	}}
	res, err := run(cfg, records)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Agents) != 1 {
		t.Fatalf("agents = %+v, want 1", res.Agents)
	}
	u := res.Agents[0].Usage
	if u == nil || u.QuotaPct != 38 {
		t.Fatalf("usage = %+v, want quota 38%%", u)
	}
	if u.QuotaReset != "15:00" {
		t.Errorf("QuotaReset = %q, want 15:00", u.QuotaReset)
	}
}

func TestAttachQuotaNewestRecordOnly(t *testing.T) {
	// Quota is account-level: multiple sessions of one agent share a window,
	// so only the newest live record carries it.
	stubDetect(t, nil)
	cfg := testConfig(t)
	if err := worker.SaveUsageFile(cfg.StateDir, worker.UsageFile{
		SchemaVersion: worker.SchemaVersion,
		GeneratedAt:   time.Now(),
		Quota:         map[string]worker.Quota{"claude": {Pct: 38, Label: "Session (5-hour)"}},
	}); err != nil {
		t.Fatal(err)
	}

	records := []connector.Record{
		{PID: 1, Label: "Claude", Status: agent.StatusIdle, At: time.Now().Add(-time.Minute)},
		{PID: 2, Label: "Claude", Status: agent.StatusWorking, Detail: "tool:Bash", At: time.Now()},
	}
	res, err := run(cfg, records)
	if err != nil {
		t.Fatal(err)
	}
	byPID := map[int]agent.AgentState{}
	for _, a := range res.Agents {
		byPID[a.PID] = a
	}
	if u := byPID[2].Usage; u == nil || u.QuotaPct != 38 {
		t.Errorf("newest Claude usage = %+v, want quota 38%%", u)
	}
	if u := byPID[1].Usage; u != nil {
		t.Errorf("older Claude usage = %+v, want nil", u)
	}
}

func TestAttachQuotaSkipsWhenNoData(t *testing.T) {
	// No usage.json yet: records pass through untouched.
	stubDetect(t, nil)
	cfg := testConfig(t)
	records := []connector.Record{{
		PID: 42, Label: "Claude", Status: agent.StatusWorking, Detail: "tool:Bash", At: time.Now(),
	}}
	res, err := run(cfg, records)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range res.Agents {
		if a.Usage != nil {
			t.Errorf("PID %d usage = %+v, want nil without quota data", a.PID, a.Usage)
		}
	}
}

func TestMergeBaselineAndConnectorOnly(t *testing.T) {
	stubDetect(t, []detect.Agent{{PID: 42, Label: "Grok", CWD: "code/tmon"}})
	cfg := testConfig(t)
	records := []connector.Record{
		{PID: 42, Label: "Grok", Status: agent.StatusWorking, Detail: "tool:Bash", At: time.Now()},
		{PID: 7777, Label: "Hermes", Status: agent.StatusIdle, Detail: "model:m", At: time.Now(), Profile: "default"},
	}
	res, err := run(cfg, records)
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
	if a := byPID[7777]; a.Label != "Hermes" || a.Status != agent.StatusIdle || a.Detail != "model:m" {
		t.Errorf("connector-only agent = %+v, want Hermes idle model:m", a)
	}
}

func TestStateFilePersistedWithConnectorDetail(t *testing.T) {
	stubDetect(t, nil)
	cfg := testConfig(t)
	records := []connector.Record{{
		PID: 7777, Label: "Hermes", Status: agent.StatusWorking, Detail: "model:m", At: time.Now(),
		Title: "Root Cause", Profile: "default",
	}}
	if _, err := run(cfg, records); err != nil {
		t.Fatal(err)
	}
	sf, err := agent.LoadState(cfg.StateFilePath())
	if err != nil {
		t.Fatal(err)
	}
	if len(sf.Agents) != 1 || sf.Agents[0].Detail != "model:m" {
		t.Fatalf("state file agents = %+v, want detail persisted", sf.Agents)
	}
	if sf.Agents[0].Title != "Root Cause" || sf.Agents[0].Profile != "default" {
		t.Fatalf("state file agents = %+v, want title/profile persisted", sf.Agents)
	}
}

// ─── Pane border strip ────────────────────────────────────────────────────

// borderCalls records set/clear/chrome operations for applyBorders tests.
type borderRecorder struct {
	sets   []string // "pane|value"
	clears []string
	chrome []string // positions
}

func borderCalls(t *testing.T) *borderRecorder {
	t.Helper()
	r := &borderRecorder{}
	oldSet, oldClear, oldChrome := setPaneBorder, clearPaneBorder, ensureBorderChrome
	setPaneBorder = func(pane, value string) { r.sets = append(r.sets, pane+"|"+value) }
	clearPaneBorder = func(pane string) { r.clears = append(r.clears, pane) }
	ensureBorderChrome = func(position string) { r.chrome = append(r.chrome, position) }
	t.Cleanup(func() {
		setPaneBorder, clearPaneBorder, ensureBorderChrome = oldSet, oldClear, oldChrome
	})
	return r
}

func TestBorderLine(t *testing.T) {
	th := theme.Default
	got := borderLine(th, agent.StatusBlocked)
	want := "#[fg=colour208] " + th.Icons.Blocked + " blocked "
	if got != want {
		t.Fatalf("blocked line = %q, want %q", got, want)
	}
	got = borderLine(th, agent.StatusWorking)
	want = "#[fg=green] " + th.Icons.Working + " working "
	if got != want {
		t.Fatalf("working line = %q, want %q", got, want)
	}
	if got := borderLine(th, agent.StatusIdle); got != "" {
		t.Fatalf("idle line = %q, want empty", got)
	}
}

func TestBorderLineASCII(t *testing.T) {
	th := theme.Resolve(theme.Options{ASCII: true})
	got := borderLine(th, agent.StatusBlocked)
	want := "#[fg=colour208] B blocked "
	if got != want {
		t.Fatalf("ascii blocked = %q, want %q", got, want)
	}
	got = borderLine(th, agent.StatusWorking)
	want = "#[fg=green] W working "
	if got != want {
		t.Fatalf("ascii working = %q, want %q", got, want)
	}
}

func TestApplyBordersSyncsAllActive(t *testing.T) {
	// Every blocked/working pane is rewritten each poll (including steady
	// state), so enabling the feature mid-session still paints borders.
	r := borderCalls(t)
	prev := []agent.AgentState{
		{PID: 1, Label: "Grok", Pane: "s:1.1", Status: agent.StatusWorking},
		{PID: 2, Label: "Claude", Pane: "s:1.2", Status: agent.StatusBlocked},
	}
	snap := []agent.AgentState{
		{PID: 1, Label: "Grok", Pane: "s:1.1", Status: agent.StatusWorking},
		{PID: 2, Label: "Claude", Pane: "s:1.2", Status: agent.StatusBlocked},
	}
	applyBorders(theme.Default, "top", nil, prev, snap)
	want := map[string]bool{
		"s:1.1|" + borderLine(theme.Default, agent.StatusWorking): true,
		"s:1.2|" + borderLine(theme.Default, agent.StatusBlocked): true,
	}
	if len(r.sets) != 2 {
		t.Fatalf("sets = %v, want 2 entries", r.sets)
	}
	for _, s := range r.sets {
		if !want[s] {
			t.Fatalf("unexpected set %q in %v", s, r.sets)
		}
	}
	if len(r.clears) != 0 {
		t.Fatalf("clears = %v, want none", r.clears)
	}
	if !reflect.DeepEqual(r.chrome, []string{"top"}) {
		t.Fatalf("chrome = %v, want [top]", r.chrome)
	}
}

func TestApplyBordersWorking(t *testing.T) {
	r := borderCalls(t)
	prev := []agent.AgentState{{PID: 1, Label: "Grok", Pane: "s:1.1", Status: agent.StatusIdle}}
	snap := []agent.AgentState{{PID: 1, Label: "Grok", Pane: "s:1.1", Status: agent.StatusWorking}}
	applyBorders(theme.Default, "bottom", nil, prev, snap)
	want := []string{"s:1.1|" + borderLine(theme.Default, agent.StatusWorking)}
	if !reflect.DeepEqual(r.sets, want) {
		t.Fatalf("sets = %v, want %v", r.sets, want)
	}
	if !reflect.DeepEqual(r.chrome, []string{"bottom"}) {
		t.Fatalf("chrome = %v, want [bottom]", r.chrome)
	}
}

func TestApplyBordersIdleClears(t *testing.T) {
	// Idle reverts to the default (empty) border strip.
	r := borderCalls(t)
	prev := []agent.AgentState{{PID: 1, Label: "Grok", Pane: "s:1.1", Status: agent.StatusBlocked}}
	snap := []agent.AgentState{{PID: 1, Label: "Grok", Pane: "s:1.1", Status: agent.StatusIdle}}
	applyBorders(theme.Default, "top", nil, prev, snap)
	if len(r.sets) != 0 {
		t.Fatalf("sets on idle = %v, want none", r.sets)
	}
	if !reflect.DeepEqual(r.clears, []string{"s:1.1"}) {
		t.Fatalf("clears = %v, want [s:1.1]", r.clears)
	}
}

func TestApplyBordersExitClears(t *testing.T) {
	r := borderCalls(t)
	prev := []agent.AgentState{{PID: 1, Label: "Grok", Pane: "s:1.1", Status: agent.StatusWorking}}
	applyBorders(theme.Default, "top", nil, prev, nil)
	if !reflect.DeepEqual(r.clears, []string{"s:1.1"}) {
		t.Fatalf("clears = %v, want [s:1.1]", r.clears)
	}
}

func TestApplyBordersNewAgent(t *testing.T) {
	r := borderCalls(t)
	applyBorders(theme.Default, "top", nil, nil, []agent.AgentState{
		{PID: 9, Label: "Codex", Pane: "s:2.1", Status: agent.StatusIdle},
	})
	if len(r.sets) != 0 {
		t.Fatalf("new idle sets = %v, want none", r.sets)
	}
	if !reflect.DeepEqual(r.clears, []string{"s:2.1"}) {
		t.Fatalf("new idle clears = %v, want [s:2.1]", r.clears)
	}

	r = borderCalls(t)
	applyBorders(theme.Default, "top", nil, nil, []agent.AgentState{
		{PID: 9, Label: "Codex", Pane: "s:2.1", Status: agent.StatusBlocked},
	})
	want := []string{"s:2.1|" + borderLine(theme.Default, agent.StatusBlocked)}
	if !reflect.DeepEqual(r.sets, want) {
		t.Fatalf("new blocked sets = %v, want %v", r.sets, want)
	}
}

func TestApplyBordersSkipsUnmapped(t *testing.T) {
	r := borderCalls(t)
	prev := []agent.AgentState{
		{PID: 1, Label: "Grok", Pane: "?", Status: agent.StatusWorking},
		{PID: 2, Label: "Claude", Pane: "", Status: agent.StatusBlocked},
	}
	snap := []agent.AgentState{
		{PID: 1, Label: "Grok", Pane: "?", Status: agent.StatusBlocked},
		{PID: 2, Label: "Claude", Pane: "", Status: agent.StatusIdle},
	}
	applyBorders(theme.Default, "top", nil, prev, snap)
	if len(r.sets) != 0 || len(r.clears) != 0 {
		t.Fatalf("unmapped: sets=%v clears=%v, want none", r.sets, r.clears)
	}
}

func TestApplyBordersHidesByLabel(t *testing.T) {
	// A hidden agent gets no strip even when blocked.
	r := borderCalls(t)
	prev := []agent.AgentState{}
	snap := []agent.AgentState{
		{PID: 1, Label: "Aider", Pane: "s:1.1", Status: agent.StatusBlocked},
		{PID: 2, Label: "Claude", Pane: "s:1.2", Status: agent.StatusBlocked},
	}
	applyBorders(theme.Default, "top", []string{"aider"}, prev, snap)
	if len(r.sets) != 1 {
		t.Fatalf("sets = %v, want only the visible agent", r.sets)
	}
	want := "s:1.2|" + borderLine(theme.Default, agent.StatusBlocked)
	if r.sets[0] != want {
		t.Fatalf("sets[0] = %q, want %q", r.sets[0], want)
	}
}

func TestApplyBordersHidesBySession(t *testing.T) {
	r := borderCalls(t)
	prev := []agent.AgentState{
		{PID: 3, Label: "Codex", Pane: "tool-x:0.0", Status: agent.StatusWorking},
	}
	snap := []agent.AgentState{
		{PID: 3, Label: "Codex", Pane: "tool-x:0.0", Status: agent.StatusWorking},
	}
	// The agent is now hidden by session; its leftover strip must clear.
	applyBorders(theme.Default, "top", []string{"tool-*"}, prev, snap)
	if len(r.sets) != 0 {
		t.Fatalf("sets = %v, want none for a hidden pane", r.sets)
	}
	if !reflect.DeepEqual(r.clears, []string{"tool-x:0.0"}) {
		t.Fatalf("clears = %v, want the leftover strip cleared", r.clears)
	}
}

func TestBorderDisabledNoOp(t *testing.T) {
	stubDetect(t, []detect.Agent{{PID: 42, Label: "Grok", CWD: "code/tmon"}})
	r := borderCalls(t)
	cfg := testConfig(t)
	cfg.PaneBorder = false
	if _, err := run(cfg, nil); err != nil {
		t.Fatal(err)
	}
	if len(r.sets) != 0 || len(r.clears) != 0 || len(r.chrome) != 0 {
		t.Fatalf("border traffic with option disabled: sets=%v clears=%v chrome=%v", r.sets, r.clears, r.chrome)
	}
}
