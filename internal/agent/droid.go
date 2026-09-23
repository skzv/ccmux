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

// LaunchCmd resumes with `droid --resume --last` (`droid --continue`
// silently starts a fresh session). See launchChain.
func (Droid) LaunchCmd(continueFlag bool) string {
	return launchCmdWithBinary(Droid{}, "droid", continueFlag, Commands{})
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
