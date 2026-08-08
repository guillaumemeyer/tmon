package dashboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/guillaumemeyer/tmon/internal/agent"
)

func TestViewKeyCyclesModes(t *testing.T) {
	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})

	if m.viewMode != viewList {
		t.Fatalf("default view = %v, want list", m.viewMode)
	}
	m = applyMsg(t, m, key('v'))
	if m.viewMode != viewProjects {
		t.Fatalf("after v = %v, want projects", m.viewMode)
	}
	m = applyMsg(t, m, key('v'))
	if m.viewMode != viewStatus {
		t.Fatalf("after vv = %v, want status", m.viewMode)
	}
	m = applyMsg(t, m, key('v'))
	if m.viewMode != viewAgents {
		t.Fatalf("after vvv = %v, want agents", m.viewMode)
	}
	m = applyMsg(t, m, key('v'))
	if m.viewMode != viewList {
		t.Fatalf("after vvvv = %v, want list", m.viewMode)
	}
}

func TestViewPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dashboard.json")

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true).WithSettingsPath(path)
	m = applyMsg(t, m, initMsg{})
	m = applyMsg(t, m, key('v')) // projects
	if m.viewMode != viewProjects {
		t.Fatalf("view = %v, want projects", m.viewMode)
	}

	// On-disk value is the string name.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var s dashSettings
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	if s.View != "projects" {
		t.Fatalf("persisted view = %q, want projects", s.View)
	}

	// A fresh model with the same path reloads projects.
	m2 := New(f.load, true).WithSettingsPath(path)
	if m2.viewMode != viewProjects {
		t.Fatalf("reloaded view = %v, want projects", m2.viewMode)
	}
}

func TestProjectEntriesGroupByCWD(t *testing.T) {
	rows := []Row{
		{PID: 1, Label: "A", Status: agent.StatusIdle, CWD: "/home/u/blog",
			SessionID: "1", WindowIndex: "0", PaneIndex: "0"},
		{PID: 2, Label: "B", Status: agent.StatusWorking, CWD: "/home/u/code/tmon",
			SessionID: "1", WindowIndex: "0", PaneIndex: "1"},
		{PID: 3, Label: "C", Status: agent.StatusBlocked, CWD: "/home/u/blog",
			SessionID: "2", WindowIndex: "0", PaneIndex: "0"},
	}
	f := &fakeLoader{data: Data{Rows: rows}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m.viewMode = viewProjects

	entries := m.buildListEntries()
	// Projects sort alphabetically by display path; both use absolute paths
	// when HOME is unset/mismatched, so order is by raw path string.
	var sections []string
	var agents []string
	for _, e := range entries {
		if e.blank {
			continue
		}
		if e.section != "" {
			sections = append(sections, e.section)
			continue
		}
		agents = append(agents, m.rows[m.filtered[e.agent]].Label)
	}
	if len(sections) != 2 {
		t.Fatalf("sections = %v, want 2 projects", sections)
	}
	// blog agents keep filtered (list) order: A then C.
	if strings.Join(agents, ",") != "A,C,B" && strings.Join(agents, ",") != "B,A,C" {
		// Depending on alpha order of the two paths.
		t.Logf("agents order %v sections %v", agents, sections)
	}
	// Same-project agents stay together and in list order.
	blogIdx := -1
	for i, s := range sections {
		if strings.Contains(s, "blog") {
			blogIdx = i
			break
		}
	}
	if blogIdx < 0 {
		t.Fatalf("no blog section in %v", sections)
	}
	// Walk entries: after blog header, A then C before any other section.
	seenBlog := false
	var blogAgents []string
	for _, e := range entries {
		if e.blank {
			if seenBlog {
				break
			}
			continue
		}
		if e.section != "" {
			if strings.Contains(e.section, "blog") {
				seenBlog = true
				continue
			}
			if seenBlog {
				break
			}
			continue
		}
		if seenBlog {
			blogAgents = append(blogAgents, m.rows[m.filtered[e.agent]].Label)
		}
	}
	if strings.Join(blogAgents, ",") != "A,C" {
		t.Fatalf("blog agents = %v, want A,C (list order)", blogAgents)
	}
}

func TestProjectEntriesGroupByGitRoot(t *testing.T) {
	rows := []Row{
		{PID: 1, Label: "A", Status: agent.StatusIdle, CWD: "/home/u/code/tmon/internal",
			GitRoot: "/home/u/code/tmon", Branch: "main", PR: "42"},
		{PID: 2, Label: "B", Status: agent.StatusWorking, CWD: "/home/u/code/tmon/cmd",
			GitRoot: "/home/u/code/tmon", Branch: "main", PR: "42"},
		{PID: 3, Label: "C", Status: agent.StatusBlocked, CWD: "/home/u/blog",
			GitRoot: "/home/u/blog", Branch: "fix-x"},
	}
	f := &fakeLoader{data: Data{Rows: rows}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m.viewMode = viewProjects

	entries := m.buildListEntries()
	var sections []string
	var agents []string
	for _, e := range entries {
		if e.blank {
			continue
		}
		if e.section != "" {
			sections = append(sections, e.section)
			continue
		}
		agents = append(agents, m.rows[m.filtered[e.agent]].Label)
	}

	// Two repos → two sections, both tagged with their branch.
	if len(sections) != 2 {
		t.Fatalf("sections = %v, want 2 repos", sections)
	}
	hasTmon := false
	hasBlog := false
	for _, s := range sections {
		if strings.Contains(s, "tmon") {
			hasTmon = true
			if !strings.Contains(s, "main · #42") {
				t.Errorf("tmon section = %q, want branch + PR tag", s)
			}
		}
		if strings.Contains(s, "blog") {
			hasBlog = true
			if !strings.Contains(s, "fix-x") {
				t.Errorf("blog section = %q, want branch tag", s)
			}
		}
	}
	if !hasTmon || !hasBlog {
		t.Fatalf("sections = %v, want tmon and blog", sections)
	}

	// The two tmon agents (different subdirs) land in one group; the blog
	// agent lands in its own group. Blank entries separate groups.
	var groups [][]string
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			groups = append(groups, cur)
			cur = nil
		}
	}
	for _, e := range entries {
		if e.blank {
			flush()
			continue
		}
		if e.isAgent() {
			cur = append(cur, m.rows[m.filtered[e.agent]].Label)
		}
	}
	flush()
	find := func(label string) []string {
		for _, g := range groups {
			for _, l := range g {
				if l == label {
					return g
				}
			}
		}
		return nil
	}
	ga, gb, gc := find("A"), find("B"), find("C")
	if ga == nil || gb == nil || gc == nil {
		t.Fatalf("groups = %v, want agents A, B, C each in a group", groups)
	}
	if len(ga) != 2 || (ga[0] != "B" && ga[1] != "B") {
		t.Fatalf("A group = %v, want A and B together", ga)
	}
	if len(gc) != 1 {
		t.Fatalf("C group = %v, want C alone", gc)
	}
	if strings.Join(agents, ",") != "A,B,C" && strings.Join(agents, ",") != "C,A,B" {
		t.Fatalf("agents = %v, want grouped by repo", agents)
	}
}

func TestProjectEntriesGitRootWinsOverCWD(t *testing.T) {
	// Two agents in different dirs of the same repo share one group; an
	// agent whose CWD equals the repo root groups with them too.
	rows := []Row{
		{PID: 1, Label: "A", Status: agent.StatusIdle, CWD: "/home/u/repo/x",
			GitRoot: "/home/u/repo", Branch: "dev"},
		{PID: 2, Label: "B", Status: agent.StatusIdle, CWD: "/home/u/repo",
			GitRoot: "/home/u/repo", Branch: "dev"},
		{PID: 3, Label: "C", Status: agent.StatusIdle, CWD: "/home/u/other",
			GitRoot: "", Branch: ""},
	}
	f := &fakeLoader{data: Data{Rows: rows}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m.viewMode = viewProjects

	entries := m.buildListEntries()
	var sections []string
	for _, e := range entries {
		if e.section != "" {
			sections = append(sections, e.section)
		}
	}
	if len(sections) != 2 {
		t.Fatalf("sections = %v, want repo group + CWD fallback group", sections)
	}
	if !strings.Contains(sections[0], "dev") && !strings.Contains(sections[1], "dev") {
		t.Fatalf("no repo section with branch dev in %v", sections)
	}
}

func TestStatusEntriesOrder(t *testing.T) {
	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m.viewMode = viewStatus

	entries := m.buildListEntries()
	var sections []string
	var labels []string
	blanks := 0
	for _, e := range entries {
		if e.blank {
			blanks++
			continue
		}
		if e.section != "" {
			sections = append(sections, e.section)
			continue
		}
		labels = append(labels, m.rows[m.filtered[e.agent]].Label)
	}
	wantSec := []string{"blocked", "working", "idle"}
	if strings.Join(sections, ",") != strings.Join(wantSec, ",") {
		t.Fatalf("sections = %v, want %v", sections, wantSec)
	}
	// One blank between each pair of groups (3 groups → 2 blanks).
	if blanks != 2 {
		t.Fatalf("blank separators = %d, want 2", blanks)
	}
	// Within each status only one agent in testRows; overall blocked, working, idle.
	if strings.Join(labels, ",") != "Claude,Grok,Codex" {
		t.Fatalf("agents = %v, want Claude,Grok,Codex", labels)
	}
}

func TestAgentEntriesGroupByLabel(t *testing.T) {
	rows := []Row{
		{PID: 1, Label: "Grok", Status: agent.StatusWorking, CWD: "a"},
		{PID: 2, Label: "Hermes", Status: agent.StatusIdle, CWD: "b"},
		{PID: 3, Label: "Grok", Status: agent.StatusBlocked, CWD: "c"},
		{PID: 4, Label: "Claude", Status: agent.StatusIdle, CWD: "d"},
	}
	f := &fakeLoader{data: Data{Rows: rows}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m.viewMode = viewAgents

	entries := m.buildListEntries()
	var sections []string
	var labels []string
	for _, e := range entries {
		if e.blank {
			continue
		}
		if e.section != "" {
			sections = append(sections, e.section)
			continue
		}
		labels = append(labels, m.rows[m.filtered[e.agent]].Label)
	}
	// Sections sort alphabetically by display name: Claude Code, Grok Build,
	// Hermes Agent.
	wantSec := []string{"Claude Code", "Grok Build", "Hermes Agent"}
	if strings.Join(sections, ",") != strings.Join(wantSec, ",") {
		t.Fatalf("sections = %v, want %v", sections, wantSec)
	}
	// Both Grok agents stay together, in list order.
	if strings.Join(labels, ",") != "Claude,Grok,Grok,Hermes" {
		t.Fatalf("labels = %v, want Claude,Grok,Grok,Hermes", labels)
	}
}

func TestAgentEntriesUnknownLabel(t *testing.T) {
	rows := []Row{
		{PID: 1, Label: "", Status: agent.StatusIdle, CWD: "a"},
		{PID: 2, Label: "Grok", Status: agent.StatusIdle, CWD: "b"},
	}
	f := &fakeLoader{data: Data{Rows: rows}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m.viewMode = viewAgents

	entries := m.buildListEntries()
	var sections []string
	for _, e := range entries {
		if e.section != "" {
			sections = append(sections, e.section)
		}
	}
	if len(sections) != 2 {
		t.Fatalf("sections = %v, want 2 (unknown + Grok Build)", sections)
	}
	if !strings.Contains(sections[0], "Grok Build") && !strings.Contains(sections[1], "Grok Build") {
		t.Fatalf("no Grok Build section in %v", sections)
	}
	// The unknown label gets a "?" section rather than an empty header.
	if !strings.Contains(sections[0], "?") && !strings.Contains(sections[1], "?") {
		t.Fatalf("no unknown-label section in %v", sections)
	}
}

func TestAgentEntriesBlankBetweenGroups(t *testing.T) {
	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m.viewMode = viewAgents

	entries := m.buildListEntries()
	sections, blanks := 0, 0
	for _, e := range entries {
		if e.blank {
			blanks++
		}
		if e.section != "" {
			sections++
		}
	}
	// testRows has three distinct agent types (Grok, Claude, Codex).
	if sections != 3 {
		t.Fatalf("sections = %d, want 3", sections)
	}
	if blanks != sections-1 {
		t.Fatalf("blanks = %d, want %d (one between each group)", blanks, sections-1)
	}
}

func TestAgentViewNavigationOrder(t *testing.T) {
	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m = applyMsg(t, m, key('v')) // projects
	m = applyMsg(t, m, key('v')) // status
	m = applyMsg(t, m, key('v')) // agents

	// Visual order follows section layout: by display name (Claude Code,
	// Codex CLI, Grok Build) with agents inside a group keeping list order.
	want := []string{"Claude", "Codex", "Grok"}
	items := m.agentList.Items()
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3", len(items))
	}
	for i, item := range items {
		ai := item.(agentItem)
		if ai.row.Label != want[i] {
			t.Fatalf("item %d = %q, want %q (agent visual order)", i, ai.row.Label, want[i])
		}
	}

	// Back to list restores load order (Grok, Claude, Codex).
	m = applyMsg(t, m, key('v'))
	wantList := []string{"Grok", "Claude", "Codex"}
	for i, item := range m.agentList.Items() {
		ai := item.(agentItem)
		if ai.row.Label != wantList[i] {
			t.Fatalf("list item %d = %q, want %q", i, ai.row.Label, wantList[i])
		}
	}
}

func TestProjectEntriesBlankBetweenGroups(t *testing.T) {
	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m.viewMode = viewProjects

	entries := m.buildListEntries()
	sections, blanks := 0, 0
	for _, e := range entries {
		if e.blank {
			blanks++
		}
		if e.section != "" {
			sections++
		}
	}
	if sections < 2 {
		t.Fatalf("sections = %d, want at least 2", sections)
	}
	if blanks != sections-1 {
		t.Fatalf("blanks = %d, want %d (one between each group)", blanks, sections-1)
	}
}

func TestFooterShowsViewHint(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true).WithVersion("v0.5.0")
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 100, 24

	rows := strings.Split(ansi.Strip(m.View().Content), "\n")
	logoLine2 := rows[2]
	if !strings.Contains(logoLine2, asciiLogo[1]+" v0.5.0") {
		t.Fatalf("second logo line should show version next to logo, got %q", logoLine2)
	}
	footer := rows[m.height-2]
	if strings.Contains(footer, "v0.5.0") {
		t.Fatalf("footer should not contain the version, got %q", footer)
	}
	if !strings.Contains(footer, "[v] view (List)") {
		t.Fatalf("footer should show [v] view (List) on the right, got %q", footer)
	}

	m = applyMsg(t, m, key('v'))
	footer = ansi.Strip(strings.Split(m.View().Content, "\n")[m.height-2])
	if !strings.Contains(footer, "[v] view (By project)") {
		t.Fatalf("footer should show [v] view (By project), got %q", footer)
	}

	m = applyMsg(t, m, key('v'))
	footer = ansi.Strip(strings.Split(m.View().Content, "\n")[m.height-2])
	if !strings.Contains(footer, "[v] view (By status)") {
		t.Fatalf("footer should show [v] view (By status), got %q", footer)
	}

	m = applyMsg(t, m, key('v'))
	footer = ansi.Strip(strings.Split(m.View().Content, "\n")[m.height-2])
	if !strings.Contains(footer, "[v] view (By agent)") {
		t.Fatalf("footer should show [v] view (By agent), got %q", footer)
	}
}

func TestProjectsViewRendersSectionHeaders(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m = applyMsg(t, m, key('v')) // projects
	m.width, m.height = 140, 30

	v := ansi.Strip(m.View().Content)
	// Each distinct CWD from testRows becomes a section header.
	for _, want := range []string{"code/tmon", "site", "blog"} {
		if !strings.Contains(v, want) {
			t.Fatalf("projects view missing section %q:\n%s", want, v)
		}
	}
}

func TestStatusViewNavigationOrder(t *testing.T) {
	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m = applyMsg(t, m, key('v')) // projects
	m = applyMsg(t, m, key('v')) // status

	// Visual order: blocked (Claude), working (Grok), idle (Codex).
	want := []string{"Claude", "Grok", "Codex"}
	items := m.agentList.Items()
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3", len(items))
	}
	for i, item := range items {
		ai := item.(agentItem)
		if ai.row.Label != want[i] {
			t.Fatalf("item %d = %q, want %q (status visual order)", i, ai.row.Label, want[i])
		}
	}

	// Back to list restores load order (Grok, Claude, Codex).
	m = applyMsg(t, m, key('v')) // agents
	m = applyMsg(t, m, key('v')) // list
	wantList := []string{"Grok", "Claude", "Codex"}
	for i, item := range m.agentList.Items() {
		ai := item.(agentItem)
		if ai.row.Label != wantList[i] {
			t.Fatalf("list item %d = %q, want %q", i, ai.row.Label, wantList[i])
		}
	}
}

func TestParseViewMode(t *testing.T) {
	cases := map[string]ViewMode{
		"list":     viewList,
		"projects": viewProjects,
		"status":   viewStatus,
		"agents":   viewAgents,
		"LIST":     viewList,
		"":         viewList,
		"nope":     viewList,
	}
	for in, want := range cases {
		if got := parseViewMode(in); got != want {
			t.Errorf("parseViewMode(%q) = %v, want %v", in, got, want)
		}
	}
}
