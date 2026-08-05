package dashboard

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/guillaumemeyer/tmon/internal/theme"
)

// sortedThemeNames is theme.Names() (alphabetical); index 0 is catppuccin,
// 2 is dracula — used by the selector tests as jump targets.
func TestThemeSelectorOpensAndListsThemes(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 100, 24

	m = applyMsg(t, m, key('t'))
	if !m.themeMode {
		t.Fatal("'t' should open the theme selector")
	}
	if got := m.themeNames(); len(got) != len(theme.Names()) {
		t.Fatalf("theme list = %d names, want %d", len(got), len(theme.Names()))
	}
	v := ansi.Strip(m.View())
	for _, n := range theme.Names() {
		if !strings.Contains(v, n) {
			t.Fatalf("selector view missing theme %q", n)
		}
	}
	// The first preset (catppuccin, alphabetically) is highlighted, so its
	// palette preview shows its app swatch color.
	if !strings.Contains(v, "#cba6f7") {
		t.Fatalf("preview should show the highlighted theme's swatches:\n%s", v)
	}
	// Header and footer switch to theme-mode hints.
	if !strings.Contains(v, "tmon — themes") ||
		!strings.Contains(v, "[enter/space] apply") ||
		!strings.Contains(v, "[esc/q] revert") {
		t.Fatalf("theme-mode header/footer hints missing:\n%s", v)
	}
}

func TestThemeSelectorMoveUpdatesPreview(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	var persisted []string
	oldPersist := persistTheme
	persistTheme = func(name string) { persisted = append(persisted, name) }
	t.Cleanup(func() { persistTheme = oldPersist })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 100, 24

	m = applyMsg(t, m, key('t'))
	if !strings.Contains(ansi.Strip(m.View()), "#cba6f7") {
		t.Fatalf("expected catppuccin preview before moving")
	}

	// Move down twice: catppuccin → default → dracula. The dracula app
	// swatch (#bd93f9) appears only once the cursor lands on dracula, and
	// the catppuccin swatch leaves.
	m = applyMsg(t, m, key('j'))
	m = applyMsg(t, m, key('j'))
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "#bd93f9") {
		t.Fatalf("preview should show dracula's swatches after moving:\n%s", v)
	}
	if strings.Contains(v, "#cba6f7") {
		t.Fatal("preview still shows the previous theme's swatches")
	}
	// Browsing applies the highlighted theme live to the whole popup…
	if m.theme.Name != "dracula" {
		t.Fatalf("live preview theme = %q, want dracula", m.theme.Name)
	}
	if m.theme.Palette.App != "#bd93f9" {
		t.Fatalf("live preview app color = %q, want dracula's", m.theme.Palette.App)
	}
	// …but must not persist anything: only enter/space commits.
	if len(persisted) != 0 {
		t.Fatalf("browsing must not persist a theme, got %v", persisted)
	}
}

func TestThemeSelectorApplyPersists(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	var persisted []string
	oldPersist := persistTheme
	persistTheme = func(name string) { persisted = append(persisted, name) }
	t.Cleanup(func() { persistTheme = oldPersist })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 100, 24

	m = applyMsg(t, m, key('t'))
	m = applyMsg(t, m, key('j'))
	m = applyMsg(t, m, key('j')) // dracula
	m = applyMsg(t, m, key(' ')) // apply (space, like agent focus)

	if m.themeMode {
		t.Fatal("apply should close the theme selector")
	}
	if m.theme.Name != "dracula" {
		t.Fatalf("applied theme = %q, want dracula", m.theme.Name)
	}
	if m.theme.Palette.App != "#bd93f9" {
		t.Fatalf("applied palette app color = %q, want dracula's", m.theme.Palette.App)
	}
	if len(persisted) != 1 || persisted[0] != "dracula" {
		t.Fatalf("persisted = %v, want [dracula]", persisted)
	}
	// The agent list is still there after the selector closes.
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "Grok Build") {
		t.Fatalf("agent list missing after applying a theme:\n%s", v)
	}
}

func TestThemeSelectorApplyPersistsToFile(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	var persisted []string
	oldPersist := persistTheme
	persistTheme = func(name string) { persisted = append(persisted, name) }
	t.Cleanup(func() { persistTheme = oldPersist })

	dir := t.TempDir()
	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true).WithSettingsPath(dir + "/dashboard.json")
	m = applyMsg(t, m, initMsg{})
	m.width, m.height = 100, 24

	m = applyMsg(t, m, key('t'))
	m = applyMsg(t, m, key('j'))
	m = applyMsg(t, m, key('j')) // dracula
	m = applyMsg(t, m, key(' ')) // apply (space, like agent focus)

	data, err := os.ReadFile(dir + "/theme")
	if err != nil {
		t.Fatalf("theme file not written: %v", err)
	}
	if got := string(data); got != "dracula" {
		t.Fatalf("theme file = %q, want dracula", got)
	}

	// A fresh model over the same settings path re-reads the preview width;
	// the theme itself is restored by tmon.tmux at load from this file.
	m2 := New(f.load, true).WithSettingsPath(dir + "/dashboard.json")
	if m2.settingsPath == "" {
		t.Fatal("settings path should carry over")
	}
	if got := m2.themeStateDir(); got != dir {
		t.Fatalf("themeStateDir = %q, want %q", got, dir)
	}
}

func TestThemeSelectorEscKeepsTheme(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	for _, tc := range []struct {
		name string
		key  tea.KeyMsg
	}{
		{"esc", tea.KeyMsg{Type: tea.KeyEsc}},
		{"q", key('q')},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var persisted []string
			oldPersist := persistTheme
			persistTheme = func(name string) { persisted = append(persisted, name) }
			t.Cleanup(func() { persistTheme = oldPersist })

			f := &fakeLoader{data: Data{Rows: testRows()}}
			m := New(f.load, true)
			m = applyMsg(t, m, initMsg{})
			m.width, m.height = 100, 24
			orig := m.theme.Name

			m = applyMsg(t, m, key('t'))
			m = applyMsg(t, m, key('j'))
			m = applyMsg(t, m, key('j'))
			if m.theme.Name == orig {
				t.Fatal("browsing should preview the highlighted theme live")
			}
			m = applyMsg(t, m, tc.key)

			if m.themeMode {
				t.Fatal(tc.name + " should close the theme selector")
			}
			if m.theme.Name != orig {
				t.Fatalf("theme = %q after %s, want %q (revert the live preview)",
					m.theme.Name, tc.name, orig)
			}
			if len(persisted) != 0 {
				t.Fatalf("%s should not persist a theme, got %v", tc.name, persisted)
			}
		})
	}
}

func TestThemeSelectorHonorsOverrides(t *testing.T) {
	old := capturePane
	capturePane = func(p string) string { return "x" }
	t.Cleanup(func() { capturePane = old })

	f := &fakeLoader{data: Data{Rows: testRows()}}
	m := New(f.load, true)
	m = applyMsg(t, m, initMsg{})
	m = m.WithThemeOptions(theme.Options{
		Name:           "default",
		ColorOverrides: map[string]string{"app": "#ff00ff"},
	})
	m.width, m.height = 100, 24

	// The highlighted preset (catppuccin) is re-resolved with the stored
	// override, so its app swatch shows #ff00ff instead of #cba6f7.
	m = applyMsg(t, m, key('t'))
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "#ff00ff") {
		t.Fatalf("preview should apply the stored app override:\n%s", v)
	}
	if strings.Contains(v, "#cba6f7") {
		t.Fatal("preview ignored the override and showed the preset color")
	}

	// Applying keeps the override on the chosen preset: catppuccin(0) →
	// default(1) → dracula(2) → gruvbox(3) → nord(4).
	m = applyMsg(t, m, key('j'))
	m = applyMsg(t, m, key('j'))
	m = applyMsg(t, m, key('j'))
	m = applyMsg(t, m, key('j'))
	m = applyMsg(t, m, key(' ')) // apply (nord)
	if m.theme.Name != "nord" {
		t.Fatalf("applied theme = %q, want nord", m.theme.Name)
	}
	if m.theme.Palette.App != "#ff00ff" {
		t.Fatalf("applied palette ignored the override: app = %q", m.theme.Palette.App)
	}
}
