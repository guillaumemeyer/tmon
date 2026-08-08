package dashboard

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// DoctorCheck is one doctor finding shown in the in-popup report: a name,
// what was found, and whether it passes.
type DoctorCheck struct {
	Name   string
	Detail string
	OK     bool
}

// WithDoctor wires the doctor report popin: recheck re-runs the doctor
// checks when the user presses d to open the report (and r to re-run it).
// A nil recheck leaves the report empty — tests and direct construction.
func (m Model) WithDoctor(recheck func() []DoctorCheck) Model {
	m.doctorRecheck = recheck
	return m
}

// doctorView renders the doctor report popin: a title chrome matching the
// main dashboard's (the ascii wordmark, with "🩺 Doctor" where the agent
// view shows the version, and the quit hint right-aligned with one cell of
// margin), a divider, the report body (scrollable), a divider, and a footer
// with right-aligned hints. The body mirrors `tmon doctor`'s text report:
// a version header, one line per check, and a pass/fail summary. It always
// emits exactly height lines of width cells so the popin fills its pane.
func (m Model) doctorView(w, h int) string {
	innerW, innerH := w-2, h-2
	lines := make([]string, 0, innerH)

	lines = append(lines, m.headerLines(innerW, [mainHeaderHeight]string{"[esc/q] quit ", ""}, "🩺 Doctor")...)
	lines = append(lines, fit(m.st.dim.Render(strings.Repeat("━", innerW)), innerW))

	contentN := bodyLinesFor(innerH, mainHeaderHeight+1)
	if contentN < 1 {
		contentN = 1
	}
	var content []string
	if m.doctorBusy {
		content = m.doctorBusyLines()
	} else {
		content = m.doctorReportLines(innerW)
	}
	s := m.doctorScroll
	if s < 0 {
		s = 0
	}
	if max := len(content) - contentN; max > 0 && s > max {
		s = max
	}
	for i := 0; i < contentN; i++ {
		idx := s + i
		if idx < len(content) {
			lines = append(lines, fit(content[idx], innerW))
		} else {
			lines = append(lines, fit("", innerW))
		}
	}
	for len(lines) < innerH-2 {
		lines = append(lines, fit("", innerW))
	}
	lines = append(lines, fit(m.st.dim.Render(strings.Repeat("━", innerW)), innerW))
	lines = append(lines, m.doctorFooterLine(innerW))

	return strings.Join(paintRows(w, framed(w, lines, m.st.white), m.st.bg), "\n")
}

// doctorRunMsg carries the result of an async doctor re-run.
type doctorRunMsg struct {
	checks []DoctorCheck
}

// doctorRunMinVisible is the minimum time the "re-running…" state stays up
// so a re-run is always perceivable, even when the checks complete
// instantly and every result is unchanged.
const doctorRunMinVisible = 300 * time.Millisecond

// doctorRunCmd re-runs the checks off the event loop, so the popup stays
// responsive during the scan, and returns the fresh report. The run waits
// at least doctorRunMinVisible so the busy state never flashes by unseen.
func (m Model) doctorRunCmd() tea.Cmd {
	recheck := m.doctorRecheck
	return func() tea.Msg {
		var checks []DoctorCheck
		if recheck != nil {
			checks = recheck()
		}
		time.Sleep(doctorRunMinVisible)
		return doctorRunMsg{checks: checks}
	}
}

// doctorBusyLines is the report body while a re-run is in flight: the
// spinner plus "re-running checks…", and the previous run's stamp so the
// report still reads as a report while it refreshes.
func (m Model) doctorBusyLines() []string {
	lines := []string{"  " + m.spinner.View() + " re-running checks…"}
	if !m.doctorRanAt.IsZero() {
		lines = append(lines, m.st.dim.Render("  last run "+m.doctorRanAt.Format("15:04:05")))
	}
	return lines
}

// doctorReportLines renders the report body: a version header, a blank
// line, one line per check (✓/✗ in the status color, name padded to a
// fixed column, detail dimmed), a blank line, and the pass/fail summary —
// mirroring `tmon doctor`'s text report. The header shows the run count
// and the last-run time, so every re-run is visibly confirmed even when
// every check result is unchanged.
func (m Model) doctorReportLines(w int) []string {
	header := "tmon doctor"
	if m.version != "" {
		header += " — " + m.version
	}
	if m.doctorRuns > 0 {
		header += " · run " + strconv.Itoa(m.doctorRuns)
	}
	if !m.doctorRanAt.IsZero() {
		header += " · last run " + m.doctorRanAt.Format("15:04:05")
	}
	out := []string{m.st.dim.Render(header), ""}
	for _, c := range m.doctorChecks {
		mark, markStyle := "✓", m.st.green
		if !c.OK {
			mark, markStyle = "✗", m.st.warn
		}
		name := fit(c.Name, 14)
		out = append(out, "  "+markStyle.Render(mark)+" "+name+" "+m.st.dim.Render(c.Detail))
	}
	out = append(out, "")
	if fails := m.doctorFailCount(); fails == 0 {
		out = append(out, m.st.green.Render("All checks passed — tmon is ready to go."))
	} else {
		out = append(out, m.st.warn.Render(fmt.Sprintf("%d problem(s) found.", fails)))
	}
	return out
}

// doctorFooterLine is the report popin's bottom status bar: scroll and
// re-run hints, right-aligned with one cell of right margin. Hints that do
// not fit the popin width are dropped from the end.
func (m Model) doctorFooterLine(w int) string {
	parts := []string{"[↑/↓] scroll", "[r] re-run"}
	text := strings.Join(parts, "  ") + " "
	for ansi.StringWidth(text) > w && len(parts) > 1 {
		parts = parts[:len(parts)-1]
		text = strings.Join(parts, "  ") + " "
	}
	if ansi.StringWidth(text) > w {
		text = ansi.Truncate(text, w, "")
	}
	pad := w - ansi.StringWidth(text)
	if pad > 0 {
		text = strings.Repeat(" ", pad) + text
	}
	return m.st.dim.Render(text)
}

// doctorFailCount is the number of failing checks in the current report.
func (m Model) doctorFailCount() int {
	n := 0
	for _, c := range m.doctorChecks {
		if !c.OK {
			n++
		}
	}
	return n
}

// doctorContentHeight is the number of report lines visible in the popin
// body: the canvas height minus the frame (2), the wordmark title chrome
// and its divider (mainHeaderHeight+1 rows), and the fixed divider+footer
// pair below.
func (m Model) doctorContentHeight() int {
	return bodyLinesFor(m.height-2, mainHeaderHeight+1)
}

// doctorMaxScroll is the largest valid scroll offset for the report body.
// The report has header + blank + checks + blank + summary lines.
func (m Model) doctorMaxScroll() int {
	n := len(m.doctorChecks) + 4
	if max := n - m.doctorContentHeight(); max > 0 {
		return max
	}
	return 0
}

// doctorScrollBy moves the report scroll by d lines and clamps it into
// range.
func (m *Model) doctorScrollBy(d int) {
	m.doctorScroll += d
	if m.doctorScroll < 0 {
		m.doctorScroll = 0
	}
	if max := m.doctorMaxScroll(); m.doctorScroll > max {
		m.doctorScroll = max
	}
}

// handleDoctorKey dispatches keys in the doctor report view: scrolling
// (j/k/up/down, ctrl+u/ctrl+d, g/G, mouse wheel), re-running the checks
// (r), returning to the agent view (esc/q), and quitting the popup
// (ctrl+c).
func (m Model) handleDoctorKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.doctorMode = false
		m.doctorScroll = 0
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	case "r":
		m.doctorBusy = true
		m.doctorScroll = 0
		return m, m.doctorRunCmd()
	case "up", "k":
		m.doctorScrollBy(-1)
	case "down", "j":
		m.doctorScrollBy(1)
	case "ctrl+u", "pgup":
		m.doctorScrollBy(-m.doctorContentHeight() / 2)
	case "ctrl+d", "pgdown":
		m.doctorScrollBy(m.doctorContentHeight() / 2)
	case "g", "home":
		m.doctorScroll = 0
	case "G", "end":
		m.doctorScroll = m.doctorMaxScroll()
	}
	return m, nil
}
