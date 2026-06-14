package agent

import (
	"path/filepath"
	"time"
)

// Hermes is Nous Research's Hermes agent. Binary: `hermes`. Config
// root: ~/.hermes. Reads AGENTS.md for project context.
//
// Launch follows the uniform agent pattern (bare binary on a fresh
// project, `--continue` chain on resume) so it round-trips through the
// canonical launchCmdWithBinary builder that the daemon and pickers
// use — a per-agent launch flag (e.g. `--tui`) would diverge from that
// builder, which the agent test suite enforces must agree.
type Hermes struct{}

func (Hermes) ID() ID              { return IDHermes }
func (Hermes) DisplayName() string { return "Hermes" }
func (Hermes) Binary() string      { return "hermes" }

func (Hermes) LaunchCmd(continueFlag bool) string {
	if continueFlag {
		return "hermes --continue || hermes || zsh || bash || sh"
	}
	return "hermes"
}

func (Hermes) ConfigRoot(home string) string      { return filepath.Join(home, ".hermes") }
func (Hermes) TranscriptsRoot(home string) string { return filepath.Join(home, ".hermes") }

func (Hermes) InitialPrompt(name, description string) string {
	return agentsMdInitialPrompt(name, description)
}

func (Hermes) Classify(pane string, lastChange time.Time, idleThreshold time.Duration) State {
	return engineClassify(IDHermes, pane, "", lastChange, idleThreshold)
}

func (Hermes) ClassifyWithTitle(pane, title string, lastChange time.Time, idleThreshold time.Duration) State {
	return engineClassify(IDHermes, pane, title, lastChange, idleThreshold)
}
