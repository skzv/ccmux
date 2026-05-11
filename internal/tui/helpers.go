package tui

import (
	"os/exec"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

// keyMatches is a small wrapper because we use the binding-style API but
// don't want every callsite to do a `for _, k := range b.Keys()` loop.
func keyMatches(msg tea.KeyMsg, b key.Binding) bool {
	return key.Matches(msg, b)
}

// cmdFor constructs an *exec.Cmd. Wrapper exists so tests can swap it.
var cmdFor = func(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}

// tickEvery is the Bubble Tea pattern for "send tickMsg in d, then again,
// and again." Each tickMsg arrival reschedules itself.
func tickEvery(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg{At: t} })
}
