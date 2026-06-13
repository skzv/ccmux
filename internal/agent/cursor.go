package agent

import (
	"path/filepath"
	"time"
)

// Cursor is the Cursor Agent CLI. Binary: `cursor-agent`. Config root:
// ~/.cursor. Cursor Agent writes JSONL transcripts beneath
// ~/.cursor/projects/<encoded-cwd>/agent-transcripts/. Aggregate
// analytics live at ~/.cursor/ai-tracking/ai-code-tracking.db and
// are read by internal/cursorusage for the Agents Cursor sub-tab.
type Cursor struct{}

func (Cursor) ID() ID              { return IDCursor }
func (Cursor) DisplayName() string { return "Cursor" }
func (Cursor) Binary() string      { return "cursor-agent" }

func (Cursor) LaunchCmd(continueFlag bool) string {
	if continueFlag {
		return "cursor-agent resume || cursor-agent || zsh || bash || sh"
	}
	return "cursor-agent"
}

func (Cursor) ConfigRoot(home string) string      { return filepath.Join(home, ".cursor") }
func (Cursor) TranscriptsRoot(home string) string { return filepath.Join(home, ".cursor", "projects") }

// InitialPrompt mirrors the AGENTS.md-centered bootstrap used by Codex
// and Antigravity. Cursor CLI reads AGENTS.md and .cursor/rules, so the
// persistent project context lives in the same cross-agent file.
func (Cursor) InitialPrompt(name, description string) string {
	if description == "" {
		description = "(no description yet — please ask me what I'm building)"
	}
	return `I'm starting a new project called "` + name + `". ` + description + ` ` +
		`Please: (1) Ask me 2-3 targeted questions about the concept, stack, and immediate goals. ` +
		`(2) From my answers, write AGENTS.md at the project root so you have persistent context next time. ` +
		`(3) The project already has docs/01_Specs/ (specs/PRDs), docs/02_Architecture/ (ADRs), and docs/03_Agent_Logs/ (daily scratchpad). Use them. ` +
		`(4) Pick the right source-code layout for the language/stack we choose and create those directories.`
}

// Classify uses the same conservative quiet-pane heuristic as the other
// non-Claude agents until we have real Cursor pane fixtures.
func (Cursor) Classify(pane string, lastChange time.Time, idleThreshold time.Duration) State {
	return engineClassify(IDCursor, pane, "", lastChange, idleThreshold)
}

// ClassifyWithTitle routes through the data-driven engine.
func (Cursor) ClassifyWithTitle(pane, title string, lastChange time.Time, idleThreshold time.Duration) State {
	return engineClassify(IDCursor, pane, title, lastChange, idleThreshold)
}
