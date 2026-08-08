package dashboard

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// openComposer presses s on the model (an agent with a pane must be
// selected) and returns the model with the message composer open.
func openComposer(t *testing.T, m Model) Model {
	t.Helper()
	m = applyMsg(t, m, key('s'))
	if !m.composing {
		t.Fatal("s should open the message composer")
	}
	return m
}

func TestComposeOpensOnS(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{}) // Grok selected first; Pane "main:0.0"
	m.width, m.height = 120, 24

	m = openComposer(t, m)
	if m.composeTarget != "main:0.0" {
		t.Fatalf("composeTarget = %q, want main:0.0", m.composeTarget)
	}
	if !m.compose.Focused() {
		t.Fatal("composer textarea should be focused")
	}
}

func TestComposeTypesCharacters(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 120, 24
	m = openComposer(t, m)

	for _, r := range []rune{'h', 'i'} {
		m = applyMsg(t, m, key(r))
	}
	if got := m.compose.Value(); got != "hi" {
		t.Fatalf("compose value = %q, want hi", got)
	}
	if !m.composing {
		t.Fatal("typing should keep the composer open")
	}
}

func TestComposeEnterSends(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	var gotPane, gotText string
	oldSend := sendToPane
	sendToPane = func(pane, text string) error {
		gotPane, gotText = pane, text
		return nil
	}
	t.Cleanup(func() { sendToPane = oldSend })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 120, 24
	m = openComposer(t, m)

	for _, r := range []rune{'h', 'i'} {
		m = applyMsg(t, m, key(r))
	}
	m = applyMsg(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if gotPane != "main:0.0" {
		t.Fatalf("sent to pane %q, want main:0.0", gotPane)
	}
	if gotText != "hi" {
		t.Fatalf("sent text %q, want hi", gotText)
	}
	if m.composing {
		t.Fatal("composer should close after a successful send")
	}
	if got := m.compose.Value(); got != "" {
		t.Fatalf("draft should be cleared after send, got %q", got)
	}
}

func TestComposeEnterEmptyDoesNotSend(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	sent := false
	oldSend := sendToPane
	sendToPane = func(pane, text string) error {
		sent = true
		return nil
	}
	t.Cleanup(func() { sendToPane = oldSend })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 120, 24
	m = openComposer(t, m)

	m = applyMsg(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if sent {
		t.Fatal("an empty draft must not be sent")
	}
	if m.composing {
		t.Fatal("composer should close on an empty enter")
	}
}

func TestComposeAltEnterInsertsNewline(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 120, 24
	m = openComposer(t, m)

	m = applyMsg(t, m, key('a'))
	m = applyMsg(t, m, tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt})
	m = applyMsg(t, m, key('b'))

	if got := m.compose.Value(); got != "a\nb" {
		t.Fatalf("compose value = %q, want a\\nb", got)
	}
	if !m.composing {
		t.Fatal("alt+enter should insert a newline, not send")
	}
}

func TestComposeShiftEnterInsertsNewline(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 120, 24
	m = openComposer(t, m)

	m = applyMsg(t, m, key('a'))
	m = applyMsg(t, m, tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift})

	if got := m.compose.Value(); got != "a\n" {
		t.Fatalf("compose value = %q, want a\\n", got)
	}
	if !m.composing {
		t.Fatal("shift+enter should insert a newline, not send")
	}
}

func TestComposeEscClearsAndCloses(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 120, 24
	m = openComposer(t, m)

	for _, r := range []rune{'a', 'b', 'c'} {
		m = applyMsg(t, m, key(r))
	}
	m = applyMsg(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})

	if m.composing {
		t.Fatal("esc should close the composer")
	}
	if got := m.compose.Value(); got != "" {
		t.Fatalf("esc should clear the draft, got %q", got)
	}
	if m.composeErr != "" {
		t.Fatalf("esc should clear the send error, got %q", m.composeErr)
	}
}

func TestComposeSendFailureKeepsOpen(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	oldSend := sendToPane
	sendToPane = func(pane, text string) error { return errors.New("boom") }
	t.Cleanup(func() { sendToPane = oldSend })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 120, 24
	m = openComposer(t, m)

	for _, r := range []rune{'h', 'i'} {
		m = applyMsg(t, m, key(r))
	}
	m = applyMsg(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if !m.composing {
		t.Fatal("composer should stay open after a failed send")
	}
	if m.composeErr == "" {
		t.Fatal("send failure should be recorded on the hint line")
	}
	if got := m.compose.Value(); got != "hi" {
		t.Fatalf("draft should survive a failed send, got %q", got)
	}
}

func TestComposeSendMultiLine(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	var gotText string
	oldSend := sendToPane
	sendToPane = func(pane, text string) error {
		gotText = text
		return nil
	}
	t.Cleanup(func() { sendToPane = oldSend })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 120, 24
	m = openComposer(t, m)

	for _, r := range []rune{'l', 'i', 'n', 'e', '1'} {
		m = applyMsg(t, m, key(r))
	}
	m = applyMsg(t, m, tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt})
	for _, r := range []rune{'l', 'i', 'n', 'e', '2'} {
		m = applyMsg(t, m, key(r))
	}
	m = applyMsg(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if gotText != "line1\nline2" {
		t.Fatalf("sent text %q, want line1\\nline2", gotText)
	}
	if m.composing {
		t.Fatal("composer should close after the send")
	}
}

func TestComposeRendersInputArea(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 120, 24

	// Before s: no composer, but the footer advertises the send key.
	raw := m.View().Content
	if !strings.Contains(raw, "[s] send") {
		t.Fatalf("footer should advertise [s] send:\n%q", raw)
	}
	if strings.Contains(raw, "[enter] send  [alt+enter] newline  [esc] cancel") {
		t.Fatal("composer hint must not render before it opens")
	}

	m = openComposer(t, m)
	raw = m.View().Content
	// The textarea styles the placeholder's first character with the cursor
	// position, so compare on the stripped text for the placeholder.
	plain := ansi.Strip(raw)
	if !strings.Contains(plain, "Message the agent…") {
		t.Fatalf("composer placeholder missing:\n%q", raw)
	}
	if !strings.Contains(plain, "[enter] send  [alt+enter] newline  [esc] cancel") {
		t.Fatalf("composer hint line missing:\n%q", raw)
	}
	if !strings.Contains(plain, strings.Repeat("─", 20)) {
		t.Fatalf("composer separator line missing:\n%q", raw)
	}
	// The hint must sit below the input area, i.e. after the placeholder.
	if strings.Index(plain, "Message the agent…") > strings.Index(plain, "[enter] send  [alt+enter] newline  [esc] cancel") {
		t.Fatalf("composer hint should render below the input:\n%q", raw)
	}
}

func TestComposeCtrlCQuits(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 120, 24
	m = openComposer(t, m)

	_, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("ctrl+c while composing should quit the popup")
	}
}

func TestComposeNoPaneDoesNotOpen(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 120, 24

	// Grok is selected; take its pane away so there is nothing to send to.
	m.rows[0].Pane = "?"
	m = applyMsg(t, m, key('s'))
	if m.composing {
		t.Fatal("s must not open the composer without a sendable pane")
	}

	// Same guard for an empty pane target.
	m.rows[0].Pane = ""
	m = applyMsg(t, m, key('s'))
	if m.composing {
		t.Fatal("s must not open the composer with an empty pane target")
	}
}

func TestComposeMouseIgnored(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 120, 24
	m = openComposer(t, m)

	// Click where Claude's row would be; the composer must swallow it.
	before := m.agentList.Index()
	m = applyMsg(t, m, click(20, 20))
	if m.agentList.Index() != before {
		t.Fatalf("mouse click should be ignored while composing, index %d -> %d", before, m.agentList.Index())
	}
	if !m.composing {
		t.Fatal("composer should stay open after a mouse click")
	}
}
