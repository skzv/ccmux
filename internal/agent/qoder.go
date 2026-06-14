package agent

import (
	"path/filepath"
	"time"
)

// Qoder is Alibaba's Qoder CLI. Binary: `qoder`. Config root: ~/.qoder
// (override QODER_CONFIG_DIR). Reads AGENTS.md for project context.
type Qoder struct{}

func (Qoder) ID() ID              { return IDQoder }
func (Qoder) DisplayName() string { return "Qoder" }
func (Qoder) Binary() string      { return "qoder" }

func (Qoder) LaunchCmd(continueFlag bool) string {
	if continueFlag {
		return "qoder --continue || qoder || zsh || bash || sh"
	}
	return "qoder"
}

func (Qoder) ConfigRoot(home string) string      { return filepath.Join(home, ".qoder") }
func (Qoder) TranscriptsRoot(home string) string { return filepath.Join(home, ".qoder") }

func (Qoder) InitialPrompt(name, description string) string {
	return agentsMdInitialPrompt(name, description)
}

func (Qoder) Classify(pane string, lastChange time.Time, idleThreshold time.Duration) State {
	return engineClassify(IDQoder, pane, "", lastChange, idleThreshold)
}

func (Qoder) ClassifyWithTitle(pane, title string, lastChange time.Time, idleThreshold time.Duration) State {
	return engineClassify(IDQoder, pane, title, lastChange, idleThreshold)
}
