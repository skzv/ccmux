package agent

import (
	"path/filepath"
	"time"
)

// Amp is Sourcegraph's Amp CLI (npm @sourcegraph/amp). Binary: `amp`.
// Amp has no fixed config dotdir — it reads AMP_SETTINGS_FILE, and its
// threads sync server-side (ampcode.com) rather than to local session
// files. ConfigRoot returns a conventional ~/.config/amp for the Agents
// tab's "where does this agent live" display; TranscriptsRoot mirrors it
// (there's no local transcript tree to walk, so the usage panel finds
// nothing there, which is correct for a server-synced agent). Reads
// AGENTS.md for project context.
type Amp struct{}

func (Amp) ID() ID              { return IDAmp }
func (Amp) DisplayName() string { return "Amp" }
func (Amp) Binary() string      { return "amp" }

func (Amp) LaunchCmd(continueFlag bool) string {
	if continueFlag {
		return "amp --continue || amp || zsh || bash || sh"
	}
	return "amp"
}

func (Amp) ConfigRoot(home string) string      { return filepath.Join(home, ".config", "amp") }
func (Amp) TranscriptsRoot(home string) string { return filepath.Join(home, ".config", "amp") }

func (Amp) InitialPrompt(name, description string) string {
	return agentsMdInitialPrompt(name, description)
}

func (Amp) Classify(pane string, lastChange time.Time, idleThreshold time.Duration) State {
	return engineClassify(IDAmp, pane, "", lastChange, idleThreshold)
}

func (Amp) ClassifyWithTitle(pane, title string, lastChange time.Time, idleThreshold time.Duration) State {
	return engineClassify(IDAmp, pane, title, lastChange, idleThreshold)
}
