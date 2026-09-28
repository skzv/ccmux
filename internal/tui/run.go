package tui

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/project"
)

// resolveProjectsOverride resolves a `--projects` / `ccmux DIR` root the
// way the CLI subcommands do (project.ResolveRoot: "~" expanded, made
// absolute) and checks it is a directory. filepath.Abs alone left a
// quoted or `--projects=~/work` tilde unexpanded, so the TUI refused a
// root the rest of the CLI accepted.
func resolveProjectsOverride(raw string) (string, error) {
	root := project.ResolveRoot(raw)
	if fi, err := os.Stat(root); err != nil {
		return "", fmt.Errorf("projects dir %q: %w", root, err)
	} else if !fi.IsDir() {
		return "", fmt.Errorf("projects dir %q is not a directory", root)
	}
	return root, nil
}

// Run is the main entrypoint called from cmd/ccmux. Loads config, builds
// the App, runs Bubble Tea, returns any program-level error.
//
// `projectsOverride` lets the caller (`ccmux <dir>` or `--projects PATH`)
// point the TUI at a different projects root for this invocation only,
// without rewriting config.toml. Empty string falls back to the config
// value.
//
// CCMUX_DEBUG=1 enables a per-run log at
// ~/.local/state/ccmux/ccmux.log so the user can tail bugs they
// couldn't otherwise capture interactively.
func Run(version string, projectsOverride string, expandNotes bool) error {
	initDebugLog()
	defer closeDebugLog()
	if dbg := debugLogger(); dbg != nil {
		dbg.Printf("ccmux %s starting", version)
	}

	cfg, cfgErr := config.Load()
	if projectsOverride != "" {
		root, err := resolveProjectsOverride(projectsOverride)
		if err != nil {
			return err
		}
		projectsOverride = root
	}
	// Per-run overrides, re-applied whenever the app adopts a fresh
	// config from disk and never saved. `--expand-notes` can force the
	// Notes folder tree open, but never forces it collapsed (that's the
	// default and the config-driven choice).
	overrides := func(c *config.Config) {
		if expandNotes {
			c.Notes.ExpandFolders = true
		}
		if projectsOverride != "" {
			c.Projects.Root = projectsOverride
		}
	}
	overrides(&cfg)

	app := New(cfg, version)
	app.SetRuntimeOverrides(overrides)
	app.SetStartupConfigError(cfgErr)
	// Mouse cell-motion mode is enabled so wheel events reach the
	// program (the Notes preview viewport, the Agents browser preview,
	// and other scrollable regions forward them to their
	// bubbles/viewport handlers). Tradeoff: most terminal clients
	// capture mouse events themselves, so native click-drag text
	// selection no longer works as-is — users hold Shift while
	// selecting (iTerm, Terminal.app, Blink, kitty, wezterm all honor
	// this) to bypass the program's mouse reporting and copy text the
	// usual way. Inside tmux, the regular tmux copy-mode keybindings
	// still work without any selection workaround.
	p := tea.NewProgram(app, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		return err
	}
	return nil
}
