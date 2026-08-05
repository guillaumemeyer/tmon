package main

import (
	"fmt"
	"os"

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

// resolveThemeOpts builds the theme resolution options from the runtime
// config, shared by the status bar, the dashboard, and previews.
func resolveThemeOpts(cfg config.Config) theme.Options {
	return theme.Options{
		Name:           cfg.Theme,
		ColorOverrides: cfg.ColorOverrides,
		IconOverrides:  cfg.IconOverrides,
		ASCII:          cfg.ASCII,
	}
}

// resolveTheme builds the resolved theme from the runtime config so every
// consumer (status bar, dashboard, preview) resolves identically.
func resolveTheme(cfg config.Config) theme.Theme {
	return theme.Resolve(resolveThemeOpts(cfg))
}

func listThemes() int {
	for _, n := range theme.Names() {
		fmt.Println(n)
	}
	return 0
}

// previewTheme prints the swatch rows and sample status lines for a theme.
// The rendering itself lives in internal/theme so the in-popup theme
// selector (dashboard) shows the exact same preview.
func previewTheme(name string) int {
	t := theme.Resolve(theme.Options{Name: name})
	fmt.Printf("theme: %s\n\n", t.Name)
	for _, line := range theme.SwatchLines(t) {
		fmt.Println(line)
	}
	fmt.Println()
	fmt.Println("  emoji: " + theme.SampleLine(t, theme.Resolve(theme.Options{Name: name}).Icons))
	fmt.Println("  ascii: " + theme.SampleLine(t, theme.Resolve(theme.Options{Name: name, ASCII: true}).Icons))
	return 0
}
