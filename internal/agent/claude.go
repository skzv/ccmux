package agent

import (
	"path/filepath"
	"time"

	"github.com/skzv/ccmux/internal/claude"
)

// Claude is the Anthropic Claude Code agent. The default. Delegates
// state classification to internal/claude so we don't fork the heuristic
// (which has been tuned against real pane content over the lifetime of
// ccmux). Once internal/agent grows test coverage we can fold the
// classifier in directly and retire internal/claude.
type Claude struct{}

func (Claude) ID() ID              { return IDClaude }
func (Claude) DisplayName() string { return "Claude Code" }
func (Claude) Binary() string      { return "claude" }

func (Claude) LaunchCmd(continueFlag bool) string {
	if continueFlag {
		// Cascade through preferred → portable shells so the pane stays
		// alive when Claude itself can't run. zsh is the nicest UX where
		// available (macOS default); bash covers most Linux hosts; sh is
		// the POSIX guarantee — always present. Without the bash/sh tail
		// minimal Linux CI images (no zsh) silently fail the chain and
		// the tmux session dies the instant new-session returns.
		return "claude --continue || claude || zsh || bash || sh"
	}
	return "claude"
}

func (Claude) ConfigRoot(home string) string      { return filepath.Join(home, ".claude") }
func (Claude) TranscriptsRoot(home string) string { return filepath.Join(home, ".claude", "projects") }

// InitialPrompt is what ccmux types into a fresh Claude session as the
// first message. The wording asks /init to be run cleanly (no
// pre-existing CLAUDE.md so it's a one-write event) and tells Claude
// about the scaffold's docs/ directories. Kept verbatim from the
// pre-multi-agent scaffold so existing user-facing behavior is
// unchanged.
func (Claude) InitialPrompt(name, description string) string {
	if description == "" {
		description = "(no description yet — please ask me what I'm building)"
	}
	return `I'm starting a new project called "` + name + `". ` + description + ` ` +
		`Please: (1) Run /init to scaffold CLAUDE.md from scratch — there is no existing CLAUDE.md, so this should be one clean write. ` +
		`(2) The project already has these documentation directories: docs/01_Specs/ (specs/PRDs), docs/02_Architecture/ (ADRs), docs/03_Agent_Logs/ (daily scratchpad). Reflect this in CLAUDE.md's Directory Layout section. ` +
		`(3) Pick the right source-code layout for the language/stack we choose — e.g. cmd+internal for Go, src for Node/Python — and create those directories yourself. Don't assume src/+tests/. ` +
		`(4) Ask me 2-3 targeted questions about the concept, stack, and immediate goals, then write docs/01_Specs/00_Initial_Concept.md from my answers.`
}

// Classify converts the internal/claude classifier output to the
// agent.State type. They have identical string values so the cast is
// trivial; the conversion is explicit so a future divergence in either
// side's enum names is caught at the boundary rather than silently
// papered over.
func (Claude) Classify(pane string, lastChange time.Time, idleThreshold time.Duration) State {
	return State(claude.Classify(pane, lastChange, idleThreshold))
}

// ClassifyWithTitle routes claude through the data-driven engine,
// which combines the OSC title (braille spinner = working) with the
// existing claude.go body heuristics for the rounded-corner prompt
// frame. Falls back to the legacy body-only classifier when no rule
// matches, preserving the pre-engine behavior on every test fixture.
func (Claude) ClassifyWithTitle(pane, title string, lastChange time.Time, idleThreshold time.Duration) State {
	return engineClassify(IDClaude, pane, title, lastChange, idleThreshold)
}
