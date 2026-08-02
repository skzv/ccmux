package agent

import (
	"path/filepath"
	"time"
)

// Kiro is AWS's Kiro CLI (the agent formerly known as Amazon Q
// Developer CLI). Binary: `kiro-cli`. Config root: ~/.kiro (agents live
// under ~/.kiro/agents globally and .kiro/agents per project). Reads
// AGENTS.md for project context.
type Kiro struct{}

func (Kiro) ID() ID              { return IDKiro }
func (Kiro) DisplayName() string { return "Kiro" }
func (Kiro) Binary() string      { return "kiro-cli" }

func (Kiro) LaunchCmd(continueFlag bool) string {
	if continueFlag {
		return "kiro-cli --continue || kiro-cli || zsh || bash || sh"
	}
	return "kiro-cli"
}

func (Kiro) ConfigRoot(home string) string      { return filepath.Join(home, ".kiro") }
func (Kiro) TranscriptsRoot(home string) string { return filepath.Join(home, ".kiro") }

func (Kiro) InitialPrompt(name, description string) string {
	return agentsMdInitialPrompt(name, description)
}

func (Kiro) Classify(pane string, lastChange time.Time, idleThreshold time.Duration) State {
	return engineClassify(IDKiro, pane, "", lastChange, idleThreshold)
}

func (Kiro) ClassifyWithTitle(pane, title string, lastChange time.Time, idleThreshold time.Duration) State {
	return engineClassify(IDKiro, pane, title, lastChange, idleThreshold)
}
