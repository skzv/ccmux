package agent

import (
	"path/filepath"
	"time"
)

// Copilot is GitHub Copilot CLI (npm @github/copilot). Binary:
// `copilot`. Config root: ~/.copilot (override COPILOT_HOME) — history
// and logs live under it. Reads AGENTS.md for project context.
type Copilot struct{}

func (Copilot) ID() ID              { return IDCopilot }
func (Copilot) DisplayName() string { return "Copilot" }
func (Copilot) Binary() string      { return "copilot" }

func (Copilot) LaunchCmd(continueFlag bool) string {
	if continueFlag {
		return "copilot --continue || copilot || zsh || bash || sh"
	}
	return "copilot"
}

func (Copilot) ConfigRoot(home string) string      { return filepath.Join(home, ".copilot") }
func (Copilot) TranscriptsRoot(home string) string { return filepath.Join(home, ".copilot") }

func (Copilot) InitialPrompt(name, description string) string {
	return agentsMdInitialPrompt(name, description)
}

func (Copilot) Classify(pane string, lastChange time.Time, idleThreshold time.Duration) State {
	return engineClassify(IDCopilot, pane, "", lastChange, idleThreshold)
}

func (Copilot) ClassifyWithTitle(pane, title string, lastChange time.Time, idleThreshold time.Duration) State {
	return engineClassify(IDCopilot, pane, title, lastChange, idleThreshold)
}
