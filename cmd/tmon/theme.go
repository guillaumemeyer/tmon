package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/guillaumemeyer/tmon/internal/config"
	"github.com/guillaumemeyer/tmon/internal/theme"
)

// cmdTheme lists the theme presets or prints a live preview of one.
//
//	tmon theme                list presets
//	tmon theme preview [name] color swatches + sample status line
func cmdTheme(args []string) int {
	if len(args) == 0 {
		return listThemes()
	}
	switch args[0] {
	case "list", "ls":
		return listThemes()
	case "preview":
		name := "default"
		if len(args) > 1 {
			name = args[1]
		}
		return previewTheme(name)
	default:
		fmt.Fprintf(os.Stderr, "tmon: theme: unknown subcommand %q (want 'preview [name]')\n", args[0])
		return 2
	}
}

// resolveTheme builds the theme options from the runtime config so every
// consumer (status bar, dashboard, preview) resolves identically.
func resolveTheme(cfg config.Config) theme.Theme {
	return theme.Resolve(theme.Options{
		Name:           cfg.Theme,
		ColorOverrides: cfg.ColorOverrides,
		IconOverrides:  cfg.IconOverrides,
		ASCII:          cfg.ASCII,
	})
}

func listThemes() int {
	for _, n := range theme.Names() {
		fmt.Println(n)
	}
	return 0
}

func previewTheme(name string) int {
	t := theme.Resolve(theme.Options{Name: name})
	fmt.Printf("theme: %s\n\n", t.Name)

	swatches := []struct{ label, value string }{
		{"app", t.Palette.App},
		{"blocked", t.Palette.Blocked},
		{"working", t.Palette.Working},
		{"idle", t.Palette.Idle},
		{"dim", t.Palette.Dim},
		{"accent", t.Palette.Accent},
		{"warn", t.Palette.Warn},
		{"selbg", t.Palette.SelBg},
	}
	for _, s := range swatches {
		block := lipgloss.NewStyle().
			Background(lipgloss.Color(theme.Lipgloss(s.value))).
			Render("  ")
		fmt.Printf("  %s  %-8s %s\n", block, s.label, s.value)
	}

	fmt.Println()
	fmt.Println("  emoji: " + sampleStatusLine(t, theme.Resolve(theme.Options{Name: name}).Icons))
	fmt.Println("  ascii: " + sampleStatusLine(t, theme.Resolve(theme.Options{Name: name, ASCII: true}).Icons))
	return 0
}

// sampleStatusLine renders one indicator line ("🤖-🛑1-⚡️2-💤1") in the
// theme's actual colors, so `tmon theme preview` shows real terminal colors
// rather than raw tmux directives.
func sampleStatusLine(t theme.Theme, ic theme.Icons) string {
	col := func(c string) lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Lipgloss(c))) }
	app := col(t.Palette.App).Render(ic.App)

	var segs []string
	add := func(glyph, color string, n int) {
		if n <= 0 {
			return
		}
		segs = append(segs, col(color).Render(glyph+strconv.Itoa(n)))
	}
	add(ic.Blocked, t.Palette.Blocked, 1)
	add(ic.Working, t.Palette.Working, 2)
	add(ic.Idle, t.Palette.Idle, 1)

	line := app
	if len(segs) > 0 {
		line += "-" + strings.Join(segs, "-")
	}
	return line
}
