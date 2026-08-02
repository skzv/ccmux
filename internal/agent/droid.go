package agent

import (
	"path/filepath"
	"time"
)

// Droid is Factory.ai's Droid CLI. Binary: `droid`. Config root:
// ~/.factory (settings.json + persisted sessions). Reads AGENTS.md for
// project context.
type Droid struct{}

func (Droid) ID() ID              { return IDDroid }
func (Droid) DisplayName() string { return "Droid" }
func (Droid) Binary() string      { return "droid" }

func (Droid) LaunchCmd(continueFlag bool) string {
	if continueFlag {
		return "droid --continue || droid || zsh || bash || sh"
	}
	return "droid"
}

func (Droid) ConfigRoot(home string) string      { return filepath.Join(home, ".factory") }
func (Droid) TranscriptsRoot(home string) string { return filepath.Join(home, ".factory") }

func (Droid) InitialPrompt(name, description string) string {
	return agentsMdInitialPrompt(name, description)
}

func (Droid) Classify(pane string, lastChange time.Time, idleThreshold time.Duration) State {
	return engineClassify(IDDroid, pane, "", lastChange, idleThreshold)
}

func (Droid) ClassifyWithTitle(pane, title string, lastChange time.Time, idleThreshold time.Duration) State {
	return engineClassify(IDDroid, pane, title, lastChange, idleThreshold)
}
