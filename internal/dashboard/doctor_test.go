package dashboard

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// doctorTestChecks is a small deterministic report for the popin tests: one
// pass, one fail, one pass.
func doctorTestChecks() []DoctorCheck {
	return []DoctorCheck{
		{Name: "tmux", Detail: "tmux 3.4 (>= 3.2)", OK: true},
		{Name: "binary", Detail: "installed v0.4.1, repo wants v0.6.0 (reload tmux to re-bootstrap)", OK: false},
		{Name: "usage.json", Detail: "v1, generated 2026-08-07 12:00:00", OK: true},
	}
}

// tallDoctorChecks builds a report tall enough to overflow a short popin.
func tallDoctorChecks(n int) []DoctorCheck {
	out := make([]DoctorCheck, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, DoctorCheck{Name: fmt.Sprintf("check%02d", i), Detail: "ok", OK: true})
	}
	return out
}

// openDoctor presses d and applies the resulting async run, so the report
// is settled (not busy) when the test inspects it.
func openDoctor(t *testing.T, m Model) Model {
	t.Helper()
	nm, cmd := m.Update(key('d'))
	m = nm.(Model)
	if cmd != nil {
		nm, _ = m.Update(cmd())
		m = nm.(Model)
	}
	return m
}

// rerunDoctor presses r and applies the resulting async run.
func rerunDoctor(t *testing.T, m Model) Model {
	t.Helper()
	nm, cmd := m.Update(key('r'))
	m = nm.(Model)
	if cmd != nil {
		nm, _ = m.Update(cmd())
		m = nm.(Model)
	}
	return m
}

func TestDoctorFooterHint(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 140, 24

	v := ansi.Strip(m.View().Content)
	footer := strings.Split(v, "\n")[m.height-2]
	if !strings.Contains(footer, "[d] doctor") {
		t.Fatalf("footer missing the doctor hint, got %q", footer)
	}
	// The doctor hint sits at the end of the hint list, after navigate.
	if idxNav, idxDoc := strings.Index(footer, "[↑/↓ j/k] navigate"), strings.Index(footer, "[d] doctor"); idxNav < 0 || idxDoc < 0 || idxNav > idxDoc {
		t.Fatalf("doctor hint should sit after the navigate hint, got %q", footer)
	}
}

func TestDoctorOpensOnD(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true).WithDoctor(func() []DoctorCheck { return doctorTestChecks() })
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 100, 24

	m = openDoctor(t, m)
	if !m.doctorMode {
		t.Fatal("'d' should open the doctor report")
	}
	if len(m.doctorChecks) != 3 {
		t.Fatalf("checks = %d, want 3 (ran on open)", len(m.doctorChecks))
	}
	if m.doctorScroll != 0 {
		t.Fatalf("scroll on open = %d, want 0", m.doctorScroll)
	}
	if m.doctorRanAt.IsZero() {
		t.Fatal("the first run should stamp the report's last-run time")
	}
	if m.doctorRuns != 1 {
		t.Fatalf("runs = %d, want 1 after opening", m.doctorRuns)
	}
	if m.doctorBusy {
		t.Fatal("the re-running state should clear once the report lands")
	}

	v := ansi.Strip(m.View().Content)
	for _, want := range []string{
		asciiLogo[0],
		asciiLogo[1] + " 🩺 Doctor",
		"[esc/q] quit",
		"tmon doctor",
		"run 1",
		"last run",
		"✓ tmux",
		"✗ binary",
		"1 problem(s) found.",
		"[↑/↓] scroll",
		"[r] re-run",
	} {
		if !strings.Contains(v, want) {
			t.Fatalf("doctor view missing %q:\n%s", want, v)
		}
	}
	// The agent list is not rendered while the report is open.
	if strings.Contains(v, "Grok Build") {
		t.Fatalf("agent list should not render in doctor mode:\n%s", v)
	}
}

func TestDoctorAllPassSummary(t *testing.T) {
	m := New(nil, true).WithDoctor(func() []DoctorCheck {
		return []DoctorCheck{{Name: "tmux", Detail: "tmux 3.4 (>= 3.2)", OK: true}}
	})
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 100, 24

	m = openDoctor(t, m)
	v := ansi.Strip(m.View().Content)
	if !strings.Contains(v, "All checks passed — tmon is ready to go.") {
		t.Fatalf("all-pass report should show the green summary:\n%s", v)
	}
}

func TestDoctorEscQReturnToAgents(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	for _, tc := range []struct {
		name string
		key  tea.KeyPressMsg
	}{
		{"esc", tea.KeyPressMsg{Code: tea.KeyEsc}},
		{"q", key('q')},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeLoader{data: Data{Rows: testRows()}}
			m := New(f.load, true).WithDoctor(func() []DoctorCheck { return doctorTestChecks() })
			m = applyMsg(t, m, initMsg{})
			m.width, m.height = 100, 24

			m = openDoctor(t, m)
			if !m.doctorMode {
				t.Fatal("precondition: 'd' should open the report")
			}
			m = applyMsg(t, m, tc.key)

			if m.doctorMode {
				t.Fatal(tc.name + " should close the doctor report")
			}
			v := ansi.Strip(m.View().Content)
			if !strings.Contains(v, "Grok Build") {
				t.Fatalf("agent list should be back after %s:\n%s", tc.name, v)
			}
		})
	}
}

func TestDoctorReRunRefreshesChecks(t *testing.T) {
	current := doctorTestChecks()
	m := New(nil, true).WithDoctor(func() []DoctorCheck { return current })
	m.width, m.height = 100, 24

	m = openDoctor(t, m)
	if len(m.doctorChecks) != 3 {
		t.Fatalf("checks on open = %d, want 3", len(m.doctorChecks))
	}

	// r re-runs the checks, bumps the run counter, refreshes the last-run
	// stamp, and resets the scroll to the top.
	current = current[:1]
	m.doctorScroll = 5
	ranAt := m.doctorRanAt
	m = rerunDoctor(t, m)
	if len(m.doctorChecks) != 1 {
		t.Fatalf("checks after r = %d, want 1", len(m.doctorChecks))
	}
	if m.doctorScroll != 0 {
		t.Fatalf("scroll after r = %d, want 0", m.doctorScroll)
	}
	if m.doctorRuns != 2 {
		t.Fatalf("runs after r = %d, want 2", m.doctorRuns)
	}
	if !m.doctorRanAt.After(ranAt) {
		t.Fatalf("r should refresh the last-run stamp (was %v, now %v)", ranAt, m.doctorRanAt)
	}
}

func TestDoctorRerunShowsBusyState(t *testing.T) {
	m := New(nil, true).WithDoctor(func() []DoctorCheck { return doctorTestChecks() })
	m.width, m.height = 100, 24

	// While the async re-run is in flight, the report shows the spinner
	// state instead of the (unchanged) results.
	nm, cmd := m.Update(key('d'))
	m = nm.(Model)
	if !m.doctorBusy {
		t.Fatal("d should put the report into the re-running state")
	}
	v := ansi.Strip(m.View().Content)
	if !strings.Contains(v, "re-running checks") {
		t.Fatalf("busy view should show the re-running state:\n%s", v)
	}

	// The fresh report lands once the run completes.
	nm, _ = m.Update(cmd())
	m = nm.(Model)
	if m.doctorBusy {
		t.Fatal("the re-running state should clear when the run lands")
	}
	if m.doctorRuns != 1 {
		t.Fatalf("runs = %d, want 1", m.doctorRuns)
	}
	v = ansi.Strip(m.View().Content)
	if !strings.Contains(v, "run 1") || !strings.Contains(v, "✓ tmux") {
		t.Fatalf("report should show the run counter and checks after the run:\n%s", v)
	}
}

func TestDoctorCtrlCQuits(t *testing.T) {
	m := New(nil, true).WithDoctor(func() []DoctorCheck { return doctorTestChecks() })
	m.width, m.height = 100, 24

	m = openDoctor(t, m)
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("ctrl+c in the doctor report should quit the popup")
	}
}

func TestDoctorTitleBar(t *testing.T) {
	m := New(nil, true).WithDoctor(func() []DoctorCheck { return doctorTestChecks() })
	m.width, m.height = 100, 24

	m = openDoctor(t, m)
	rows := strings.Split(ansi.Strip(m.View().Content), "\n")
	// Row 1: the wordmark on the left, the quit hint right-aligned with one
	// cell of margin before the border.
	if got := rows[1]; !strings.HasPrefix(got, "│ "+asciiLogo[0]) || !strings.HasSuffix(got, "[esc/q] quit │") {
		t.Fatalf("title row = %q, want prefix %q and suffix %q", got, "│ "+asciiLogo[0], "[esc/q] quit │")
	}
	// Row 2: the wordmark's second line, with "🩺 Doctor" where the agent
	// view shows the version.
	if got := rows[2]; !strings.Contains(got, " "+asciiLogo[1]) || !strings.Contains(got, "🩺 Doctor") {
		t.Fatalf("title second row = %q, want wordmark line 2 and %q", got, "🩺 Doctor")
	}
}

func TestDoctorScrollKeys(t *testing.T) {
	m := New(nil, true).WithDoctor(func() []DoctorCheck { return tallDoctorChecks(30) })
	m.width, m.height = 100, 12

	m = openDoctor(t, m)
	// contentN = 10-3-2 = 5; report = 30+4 = 34 lines; max scroll = 29.
	if got := m.doctorMaxScroll(); got != 29 {
		t.Fatalf("max scroll = %d, want 29", got)
	}

	m = applyMsg(t, m, key('j'))
	if m.doctorScroll != 1 {
		t.Fatalf("after j: scroll = %d, want 1", m.doctorScroll)
	}
	m = applyMsg(t, m, key('k'))
	if m.doctorScroll != 0 {
		t.Fatalf("after k: scroll = %d, want 0", m.doctorScroll)
	}

	// Scrolling past the top clamps at 0.
	m = applyMsg(t, m, key('k'))
	if m.doctorScroll != 0 {
		t.Fatalf("scroll below top = %d, want 0", m.doctorScroll)
	}

	// G jumps to the end; j past the end clamps at the max.
	m = applyMsg(t, m, key('G'))
	if m.doctorScroll != 29 {
		t.Fatalf("after G: scroll = %d, want 29", m.doctorScroll)
	}
	m = applyMsg(t, m, key('j'))
	if m.doctorScroll != 29 {
		t.Fatalf("scroll past end = %d, want 29", m.doctorScroll)
	}

	// The view honors the scroll: the last report line is visible.
	v := ansi.Strip(m.View().Content)
	if !strings.Contains(v, "check29") {
		t.Fatalf("view should show the report tail after G:\n%s", v)
	}
}

func TestDoctorWheelScrolls(t *testing.T) {
	m := New(nil, true).WithDoctor(func() []DoctorCheck { return tallDoctorChecks(30) })
	m.width, m.height = 100, 12

	m = openDoctor(t, m)
	m = applyMsg(t, m, tea.MouseWheelMsg{X: 5, Y: 5, Button: tea.MouseWheelDown})
	if m.doctorScroll == 0 {
		t.Fatal("wheel down should scroll the report")
	}
	before := m.doctorScroll
	m = applyMsg(t, m, tea.MouseWheelMsg{X: 5, Y: 5, Button: tea.MouseWheelUp})
	if m.doctorScroll >= before {
		t.Fatalf("wheel up should scroll back, got %d before %d", m.doctorScroll, before)
	}
}

func TestDoctorIgnoresAgentKeys(t *testing.T) {
	m := New(nil, true).WithDoctor(func() []DoctorCheck { return doctorTestChecks() })
	m.width, m.height = 100, 24

	m = openDoctor(t, m)
	// Theme, view, and status-filter keys must not leak into the report.
	m = applyMsg(t, m, key('t'))
	if m.themeMode {
		t.Fatal("t in doctor mode should not open the theme selector")
	}
	before := m.viewMode
	m = applyMsg(t, m, key('v'))
	if m.viewMode != before {
		t.Fatal("v in doctor mode should not change the view")
	}
}
