package agent

import (
	"path/filepath"
	"strings"
	"time"
)

// Pi is the pi coding agent (https://pi.dev) — a "minimal terminal
// coding harness" from Earendil Works. Binary: `pi`.
//
// Layout (verified against the installed CLI + bundled docs):
//   - Config root:  ~/.pi/agent       (settings.json, npm/, sessions/)
//   - Sessions:     ~/.pi/agent/sessions/--<cwd>--/<ts>_<uuid>.jsonl
//     where <cwd> is the working directory with `/` replaced by `-`.
//   - Context:      pi reads AGENTS.md + CLAUDE.md (its
//     `--no-context-files` flag disables exactly that discovery).
//
// Resume model: `pi --continue` resumes the most recent session in
// the cwd and `pi --session <id>` resumes a specific one by partial
// UUID — so pi takes the same `--continue` shape as Claude / Codex.
type Pi struct{}

func (Pi) ID() ID              { return IDPi }
func (Pi) DisplayName() string { return "Pi" }
func (Pi) Binary() string      { return "pi" }

func (Pi) LaunchCmd(continueFlag bool) string {
	if continueFlag {
		return "pi --continue || pi || zsh || bash || sh"
	}
	return "pi"
}

// ConfigRoot is ~/.pi/agent — the global scope pi reads settings.json
// from. (The bare ~/.pi is just the parent; pi's own files live under
// the agent/ subdir.)
func (Pi) ConfigRoot(home string) string { return filepath.Join(home, ".pi", "agent") }

// TranscriptsRoot is ~/.pi/agent/sessions, where pi writes one JSONL
// session file per conversation under a per-cwd subdirectory.
func (Pi) TranscriptsRoot(home string) string {
	return filepath.Join(home, ".pi", "agent", "sessions")
}

// InitialPrompt mirrors the AGENTS.md-centered bootstrap used by Codex,
// Antigravity, and Cursor — pi reads AGENTS.md, so the persistent
// project context lives in the same cross-agent file.
func (Pi) InitialPrompt(name, description string) string {
	if description == "" {
		description = "(no description yet — please ask me what I'm building)"
	}
	return `I'm starting a new project called "` + name + `". ` + description + ` ` +
		`Please: (1) Ask me 2-3 targeted questions about the concept, stack, and immediate goals. ` +
		`(2) From my answers, write AGENTS.md at the project root so you have persistent context next time. ` +
		`(3) The project already has docs/01_Specs/ (specs/PRDs), docs/02_Architecture/ (ADRs), and docs/03_Agent_Logs/ (daily scratchpad). Use them. ` +
		`(4) Pick the right source-code layout for the language/stack we choose and create those directories.`
}

// Classify uses the same conservative quiet-pane heuristic as the
// other non-Claude agents until we have real pi pane fixtures.
func (Pi) Classify(pane string, lastChange time.Time, idleThreshold time.Duration) State {
	if strings.TrimSpace(pane) == "" {
		return StateUnknown
	}
	if time.Since(lastChange) >= idleThreshold {
		return StateNeedsInput
	}
	return StateActive
}
