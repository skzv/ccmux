package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/config"
)

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
func Run(version string, projectsOverride string) error {
	initDebugLog()
	defer closeDebugLog()
	if dbg := debugLogger(); dbg != nil {
		dbg.Printf("ccmux %s starting", version)
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
	}
	if projectsOverride != "" {
		abs, err := filepath.Abs(projectsOverride)
		if err != nil {
			return fmt.Errorf("resolve %q: %w", projectsOverride, err)
		}
		if fi, err := os.Stat(abs); err != nil {
			return fmt.Errorf("projects dir %q: %w", abs, err)
		} else if !fi.IsDir() {
			return fmt.Errorf("projects dir %q is not a directory", abs)
		}
		cfg.Projects.Root = abs
	}

	app := New(cfg, version)
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

// nowFunc is the time source used for relative-time renderings. Override in
// tests.
var nowFunc = func() time.Time { return time.Now() }
