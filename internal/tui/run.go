package tui

import (
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/config"
)

// Run is the main entrypoint called from cmd/ccmux. Loads config, builds
// the App, runs Bubble Tea, returns any program-level error.
//
// CCMUX_DEBUG=1 enables a per-run log at
// ~/.local/state/ccmux/ccmux.log so the user can tail bugs they
// couldn't otherwise capture interactively.
func Run(version string) error {
	initDebugLog()
	defer closeDebugLog()
	if dbg := debugLogger(); dbg != nil {
		dbg.Printf("ccmux %s starting", version)
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
	}

	app := New(cfg, version)
	// Important: do NOT enable mouse capture. Bubble Tea's mouse modes
	// intercept native terminal selection in most clients (iTerm, Blink,
	// Terminal.app), which breaks copy/paste — especially inside tmux+Mosh
	// where users expect to select text with the mouse or via tmux copy
	// mode. Keyboard navigation is enough; the TUI is intentionally
	// pointer-free.
	p := tea.NewProgram(app, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return err
	}
	return nil
}

// nowFunc is the time source used for relative-time renderings. Override in
// tests.
var nowFunc = func() time.Time { return time.Now() }
