package agent

import (
	"path/filepath"
	"strings"
	"time"
)

// Antigravity is the Google Antigravity CLI agent (the rebrand of /
// successor to Gemini CLI). Binary: `agy`. Config root:
// ~/.gemini/antigravity-cli. Transcripts:
// ~/.gemini/antigravity-cli/conversations (as of agy 1.0.0).
//
// The config tree sits *under* ~/.gemini because the CLI is still
// Google's and shares the parent directory with the Antigravity IDE
// (which lives in ~/.gemini/antigravity/). Keeping the nested path
// matches what the CLI actually writes — we don't relocate it.
//
// Same v1-stub story as Codex: state classification is the
// pane-quiet-for-N heuristic until we have real pane fixtures.
type Antigravity struct{}

func (Antigravity) ID() ID              { return IDAntigravity }
func (Antigravity) DisplayName() string { return "Antigravity CLI" }
func (Antigravity) Binary() string      { return "agy" }

func (Antigravity) LaunchCmd(continueFlag bool) string {
	if continueFlag {
		// `agy --continue` picks the most recent conversation for the
		// cwd. The zsh fallback mirrors the other two agents so the UX
		// is uniform when the underlying agent breaks.
		return "agy --continue || agy || zsh"
	}
	return "agy"
}

func (Antigravity) ConfigRoot(home string) string {
	return filepath.Join(home, ".gemini", "antigravity-cli")
}

func (Antigravity) TranscriptsRoot(home string) string {
	return filepath.Join(home, ".gemini", "antigravity-cli", "conversations")
}

// InitialPrompt is the bootstrap message ccmux types into a fresh
// Antigravity session. Antigravity honors AGENTS.md (primary) and
// GEMINI.md (fallback) at the project root for persistent context —
// same idea as CLAUDE.md but a different filename. We ask the agent
// to write AGENTS.md so the project picks up that convention.
func (Antigravity) InitialPrompt(name, description string) string {
	if description == "" {
		description = "(no description yet — please ask me what I'm building)"
	}
	return `I'm starting a new project called "` + name + `". ` + description + ` ` +
		`Please: (1) Ask me 2-3 targeted questions about the concept, stack, and immediate goals. ` +
		`(2) From my answers, write AGENTS.md at the project root so you have persistent context next time. ` +
		`(3) The project already has docs/01_Specs/ (specs/PRDs), docs/02_Architecture/ (ADRs), and docs/03_Agent_Logs/ (daily scratchpad). Use them. ` +
		`(4) Pick the right source-code layout for the language/stack we choose and create those directories.`
}

// Classify uses the same conservative fallback as Codex — see that
// file's comment for why.
//
// TODO(multi-agent): capture real Antigravity pane fixtures into
// internal/agent/testdata/antigravity_*.txt and tighten this. Phase 4.
func (Antigravity) Classify(pane string, lastChange time.Time, idleThreshold time.Duration) State {
	if strings.TrimSpace(pane) == "" {
		return StateUnknown
	}
	if time.Since(lastChange) >= idleThreshold {
		return StateNeedsInput
	}
	return StateActive
}
