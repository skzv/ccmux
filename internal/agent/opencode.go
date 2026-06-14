package agent

import (
	"path/filepath"
	"time"
)

// OpenCode is the OpenCode terminal coding agent (opencode.ai). Binary:
// `opencode`. Config lives at ~/.config/opencode; sessions and message
// history live under ~/.local/share/opencode (the XDG data dir, not the
// config dir — a split worth remembering when a future Conversations
// parser walks transcripts). Reads AGENTS.md for project context.
type OpenCode struct{}

func (OpenCode) ID() ID              { return IDOpenCode }
func (OpenCode) DisplayName() string { return "OpenCode" }
func (OpenCode) Binary() string      { return "opencode" }

func (OpenCode) LaunchCmd(continueFlag bool) string {
	if continueFlag {
		return "opencode --continue || opencode || zsh || bash || sh"
	}
	return "opencode"
}

// ConfigRoot is ~/.config/opencode (the XDG config home).
func (OpenCode) ConfigRoot(home string) string {
	return filepath.Join(home, ".config", "opencode")
}

// TranscriptsRoot is ~/.local/share/opencode — where OpenCode persists
// sessions/messages/projects (XDG data home), distinct from its config.
func (OpenCode) TranscriptsRoot(home string) string {
	return filepath.Join(home, ".local", "share", "opencode")
}

func (OpenCode) InitialPrompt(name, description string) string {
	return agentsMdInitialPrompt(name, description)
}

func (OpenCode) Classify(pane string, lastChange time.Time, idleThreshold time.Duration) State {
	return engineClassify(IDOpenCode, pane, "", lastChange, idleThreshold)
}

func (OpenCode) ClassifyWithTitle(pane, title string, lastChange time.Time, idleThreshold time.Duration) State {
	return engineClassify(IDOpenCode, pane, title, lastChange, idleThreshold)
}
