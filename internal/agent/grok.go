package agent

import (
	"path/filepath"
	"strings"
	"time"
)

// Grok is xAI's Grok Build CLI (https://x.ai/cli) — a terminal coding
// agent invoked as `grok`, in early beta as of 2026-05-25 (SuperGrok /
// X Premium+). Binary: `grok`.
//
// Layout (verified against grok 0.2.3 — `grok --help`, ~/.grok on disk):
//   - Config root:  ~/.grok          (config.toml; layered with
//     ~/.grok/managed_config.toml and /etc/grok/*).
//   - Sessions:     ~/.grok/sessions. Each is a *directory*, not a single
//     file: ~/.grok/sessions/<url-encoded-cwd>/<uuidv7>/ holding
//     chat_history.jsonl (the transcript) + events.jsonl, updates.jsonl,
//     summary.json, system_prompt.txt, … plus a session_search.sqlite
//     FTS index at the sessions root. (cwd is URL-encoded — "/private/tmp"
//     → "%2Fprivate%2Ftmp" — unlike the dash-replacement claude/pi use.)
//     This multi-file/SQLite shape is why ccmux doesn't yet parse grok
//     into the Conversations list; a future parser should prefer shelling
//     `grok sessions` (list/search/restore) or `grok export`.
//   - Context:      AGENTS.md is a first-class, native Grok feature and is
//     confirmed in grok's own system prompt (alongside Claude.md — Grok is
//     Claude-Code compatible). AGENTS.md keeps ccmux's non-Claude agents
//     on one shared context file.
//
// Resume model: `grok -c, --continue` resumes the most recent session
// in the cwd, and `grok -r, --resume [<ID>]` resumes a specific one (or
// the most recent if omitted) — so Grok takes the same `--continue`
// shape as Claude / Codex / pi.
type Grok struct{}

func (Grok) ID() ID              { return IDGrok }
func (Grok) DisplayName() string { return "Grok" }
func (Grok) Binary() string      { return "grok" }

func (Grok) LaunchCmd(continueFlag bool) string {
	if continueFlag {
		return "grok --continue || grok || zsh || bash || sh"
	}
	return "grok"
}

// ConfigRoot is ~/.grok — where grok reads config.toml.
func (Grok) ConfigRoot(home string) string { return filepath.Join(home, ".grok") }

// TranscriptsRoot is ~/.grok/sessions (verified on grok 0.2.3) — the
// root that holds per-cwd/<uuid> session directories. Not consumed by
// any walker until the deferred Conversations parser lands.
func (Grok) TranscriptsRoot(home string) string {
	return filepath.Join(home, ".grok", "sessions")
}

// InitialPrompt mirrors the AGENTS.md-centered bootstrap used by Codex,
// Antigravity, Cursor, and pi — Grok reads AGENTS.md natively, so the
// persistent project context lives in the same cross-agent file.
func (Grok) InitialPrompt(name, description string) string {
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
// non-Claude agents until we have real grok pane fixtures.
func (Grok) Classify(pane string, lastChange time.Time, idleThreshold time.Duration) State {
	if strings.TrimSpace(pane) == "" {
		return StateUnknown
	}
	if time.Since(lastChange) >= idleThreshold {
		return StateNeedsInput
	}
	return StateActive
}
