package agent

import (
	"path/filepath"
	"strings"
	"time"
)

// Gemini is the Google Gemini CLI agent. Binary: `gemini`. Config root:
// ~/.gemini. Transcripts: ~/.gemini/conversations (upstream layout as
// of late 2025).
//
// Same v1-stub story as Codex: state classification is the
// pane-quiet-for-N heuristic until we have real pane fixtures.
type Gemini struct{}

func (Gemini) ID() ID              { return IDGemini }
func (Gemini) DisplayName() string { return "Gemini CLI" }
func (Gemini) Binary() string      { return "gemini" }

func (Gemini) LaunchCmd(continueFlag bool) string {
	if continueFlag {
		// Gemini's --continue picks the last session from the cwd. The
		// zsh fallback mirrors the other two agents so the UX is
		// uniform when the underlying agent breaks.
		return "gemini --continue || gemini || zsh"
	}
	return "gemini"
}

func (Gemini) ConfigRoot(home string) string {
	return filepath.Join(home, ".gemini")
}

func (Gemini) TranscriptsRoot(home string) string {
	return filepath.Join(home, ".gemini", "conversations")
}

// InitialPrompt is the bootstrap message ccmux types into a fresh
// Gemini session. Gemini honors GEMINI.md at the project root for
// persistent context — same idea as CLAUDE.md / AGENTS.md but a
// different filename. We ask Gemini to write its own once the user
// has explained the concept.
func (Gemini) InitialPrompt(name, description string) string {
	if description == "" {
		description = "(no description yet — please ask me what I'm building)"
	}
	return `I'm starting a new project called "` + name + `". ` + description + ` ` +
		`Please: (1) Ask me 2-3 targeted questions about the concept, stack, and immediate goals. ` +
		`(2) From my answers, write GEMINI.md at the project root so you have persistent context next time. ` +
		`(3) The project already has docs/01_Specs/ (specs/PRDs), docs/02_Architecture/ (ADRs), and docs/03_Agent_Logs/ (daily scratchpad). Use them. ` +
		`(4) Pick the right source-code layout for the language/stack we choose and create those directories.`
}

// Classify uses the same conservative fallback as Codex — see that
// file's comment for why.
//
// TODO(multi-agent): capture real Gemini pane fixtures into
// internal/agent/testdata/gemini_*.txt and tighten this. Phase 4.
func (Gemini) Classify(pane string, lastChange time.Time, idleThreshold time.Duration) State {
	if strings.TrimSpace(pane) == "" {
		return StateUnknown
	}
	if time.Since(lastChange) >= idleThreshold {
		return StateNeedsInput
	}
	return StateActive
}
