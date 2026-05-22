package agent

import (
	"path/filepath"
	"strings"
	"time"
)

// Codex is the OpenAI Codex CLI agent. Binary: `codex`. Config root:
// ~/.codex. Transcripts: ~/.codex/sessions (the upstream layout as of
// late 2025; subject to change, hence the override hook below).
//
// State classification is a v1 best-effort stub. Codex's TUI uses a
// different prompt frame than Claude's box-drawing art, so reusing
// internal/claude's heuristic would mis-classify Codex panes. Until we
// capture real Codex pane samples into testdata/, the conservative
// classifier just looks at "did the pane go quiet?" — same shape as
// Claude's tail-line check but with looser prompt detection.
type Codex struct{}

func (Codex) ID() ID              { return IDCodex }
func (Codex) DisplayName() string { return "Codex" }
func (Codex) Binary() string      { return "codex" }

func (Codex) LaunchCmd(continueFlag bool) string {
	if continueFlag {
		// Codex's --continue flag re-attaches the last session in the
		// cwd; the zsh→bash→sh fallback keeps the pane alive when codex
		// is missing. sh is the POSIX guarantee — minimal Linux hosts
		// without zsh would otherwise drop a dead pane.
		return "codex --continue || codex || zsh || bash || sh"
	}
	return "codex"
}

func (Codex) ConfigRoot(home string) string      { return filepath.Join(home, ".codex") }
func (Codex) TranscriptsRoot(home string) string { return filepath.Join(home, ".codex", "sessions") }

// InitialPrompt is the bootstrap message ccmux types into a fresh
// Codex session. Codex doesn't have a /init equivalent that writes a
// project memory file; it reads README/AGENTS.md instead. So the
// prompt asks for the same concept-clarification ritual the Claude
// flow does, plus a one-time AGENTS.md write so the project has
// persistent context the next time Codex attaches.
func (Codex) InitialPrompt(name, description string) string {
	if description == "" {
		description = "(no description yet — please ask me what I'm building)"
	}
	return `I'm starting a new project called "` + name + `". ` + description + ` ` +
		`Please: (1) Ask me 2-3 targeted questions about the concept, stack, and immediate goals. ` +
		`(2) From my answers, write AGENTS.md at the project root so you have persistent context next time. ` +
		`(3) The project already has docs/01_Specs/ (specs/PRDs), docs/02_Architecture/ (ADRs), and docs/03_Agent_Logs/ (daily scratchpad). Use them. ` +
		`(4) Pick the right source-code layout for the language/stack we choose and create those directories.`
}

// Classify is a deliberately conservative heuristic for Codex panes.
// Without testdata-pinned samples we can't reliably distinguish "Codex
// is rendering its prompt" from "Codex is mid-output", so we fall back
// to time-based idleness: a quiet pane for >= idleThreshold goes to
// needs_input, anything else is active. The same fallback the daemon's
// old code used for non-Claude sessions.
//
// TODO(multi-agent): capture real Codex pane fixtures into
// internal/agent/testdata/codex_*.txt and tighten this to recognize
// the prompt frame. Tracked in docs/01_Specs/02_Multi_Agent.md
// Phase 4.
func (Codex) Classify(pane string, lastChange time.Time, idleThreshold time.Duration) State {
	if strings.TrimSpace(pane) == "" {
		return StateUnknown
	}
	if time.Since(lastChange) >= idleThreshold {
		return StateNeedsInput
	}
	return StateActive
}
