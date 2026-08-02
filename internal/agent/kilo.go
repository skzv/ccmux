package agent

import (
	"path/filepath"
	"time"
)

// Kilo is the Kilo Code CLI (npm @kilocode/cli) — an OpenCode-compatible
// fork, so it shares the XDG config/data split. Binary: `kilo`. Config:
// ~/.config/kilo; data (sessions) under ~/.local/share/kilo. Reads
// AGENTS.md for project context.
type Kilo struct{}

func (Kilo) ID() ID              { return IDKilo }
func (Kilo) DisplayName() string { return "Kilo" }
func (Kilo) Binary() string      { return "kilo" }

func (Kilo) LaunchCmd(continueFlag bool) string {
	if continueFlag {
		return "kilo --continue || kilo || zsh || bash || sh"
	}
	return "kilo"
}

func (Kilo) ConfigRoot(home string) string { return filepath.Join(home, ".config", "kilo") }
func (Kilo) TranscriptsRoot(home string) string {
	return filepath.Join(home, ".local", "share", "kilo")
}

func (Kilo) InitialPrompt(name, description string) string {
	return agentsMdInitialPrompt(name, description)
}

func (Kilo) Classify(pane string, lastChange time.Time, idleThreshold time.Duration) State {
	return engineClassify(IDKilo, pane, "", lastChange, idleThreshold)
}

func (Kilo) ClassifyWithTitle(pane, title string, lastChange time.Time, idleThreshold time.Duration) State {
	return engineClassify(IDKilo, pane, title, lastChange, idleThreshold)
}
