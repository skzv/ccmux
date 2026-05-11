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
func Run(version string) error {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
	}

	app := New(cfg, version)
	p := tea.NewProgram(app, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		return err
	}
	return nil
}

// nowFunc is the time source used for relative-time renderings. Override in
// tests.
var nowFunc = func() time.Time { return time.Now() }
