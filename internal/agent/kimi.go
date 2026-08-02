package agent

import (
	"path/filepath"
	"time"
)

// Kimi is Moonshot's Kimi Code CLI. Binary: `kimi`. Config root:
// ~/.kimi-code (override KIMI_CODE_HOME); sessions, logs, and
// credentials live under it. Reads AGENTS.md for project context.
type Kimi struct{}

func (Kimi) ID() ID              { return IDKimi }
func (Kimi) DisplayName() string { return "Kimi" }
func (Kimi) Binary() string      { return "kimi" }

func (Kimi) LaunchCmd(continueFlag bool) string {
	if continueFlag {
		return "kimi --continue || kimi || zsh || bash || sh"
	}
	return "kimi"
}

func (Kimi) ConfigRoot(home string) string { return filepath.Join(home, ".kimi-code") }

// TranscriptsRoot is ~/.kimi-code/sessions, where Kimi writes per-session
// history.
func (Kimi) TranscriptsRoot(home string) string {
	return filepath.Join(home, ".kimi-code", "sessions")
}

func (Kimi) InitialPrompt(name, description string) string {
	return agentsMdInitialPrompt(name, description)
}

func (Kimi) Classify(pane string, lastChange time.Time, idleThreshold time.Duration) State {
	return engineClassify(IDKimi, pane, "", lastChange, idleThreshold)
}

func (Kimi) ClassifyWithTitle(pane, title string, lastChange time.Time, idleThreshold time.Duration) State {
	return engineClassify(IDKimi, pane, title, lastChange, idleThreshold)
}
