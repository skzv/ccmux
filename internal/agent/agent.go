// Package agent abstracts over Claude Code, Codex (OpenAI), and Gemini
// CLI (Google) — the three interactive AI coding agents ccmux can
// supervise inside a tmux session. Adding a fourth agent later is a
// matter of dropping a new `Agent` implementation alongside the
// existing three and registering it in All().
//
// Why this exists: ccmux's first cut was Claude-only. Every layer
// (session command, state classifier, usage panel, config tab,
// initial-prompt template) shelled out to "claude" or read
// ~/.claude/. Multi-agent support hangs everything off the single
// strategy interface below so callers stop hardcoding the agent.
//
// See docs/01_Specs/02_Multi_Agent.md for the spec and the three
// locked decisions (per-project + switchable, c- prefix retained,
// BEL-only notifications for non-Claude in v1).
package agent

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// ID is the canonical agent identifier. The string values match what
// gets written into <project>/.ccmux/agent so they are *load-bearing*
// — don't rename them without a migration.
type ID string

const (
	IDClaude ID = "claude"
	IDCodex  ID = "codex"
	IDGemini ID = "gemini"
)

// State enumerates the high-level lifecycle of an agent session, mirrored
// from internal/claude for back-compat with existing callers and for
// the dashboard's row-ordering invariant. The string values are part
// of the protocol (daemon.SessionState.State) so they must not drift.
type State string

const (
	StateUnknown    State = "unknown"
	StateActive     State = "active"
	StateIdle       State = "idle"
	StateNeedsInput State = "needs_input"
	StateError      State = "error"
)

// Agent is the strategy interface for one supervised coding agent.
//
// Method intent:
//
//   - ID, DisplayName, Binary: identity.
//   - LaunchCmd: what we hand to `tmux new-session` so the agent starts
//     in the new pane. `continueFlag=true` means the user is resuming an
//     existing project; in Claude's case that flips to
//     `claude --continue || claude || zsh` (the zsh fallback lets the
//     user inspect the project if Claude is broken).
//   - ConfigRoot: ~/.claude, ~/.codex, ~/.gemini. The Agents config tab
//     uses this to pick which file to display.
//   - TranscriptsRoot: where per-project JSONL transcripts live. The
//     usage panel walks this tree to total tokens / prompts.
//   - InitialPrompt: the first message ccmux types into the agent's
//     prompt after the new session is up. Per-agent because the
//     bootstrapping rituals differ (Claude runs /init; Codex/Gemini
//     have their own conventions).
//   - Classify: per-agent heuristic for "needs input" detection. Each
//     CLI has a different prompt frame, so this is the most agent-
//     specific method.
//
// Implementations live in this package next to this file so an audit
// can read all three (claude.go, codex.go, gemini.go) without jumping
// around.
type Agent interface {
	ID() ID
	DisplayName() string
	Binary() string
	LaunchCmd(continueFlag bool) string
	ConfigRoot(home string) string
	TranscriptsRoot(home string) string
	InitialPrompt(name, description string) string
	Classify(pane string, lastChange time.Time, idleThreshold time.Duration) State
}

// All returns every supported agent in canonical order
// (claude → codex → gemini). Order matters: pickers default to the
// first installed entry, and that lets us bias new users toward Claude
// without making it special-case in UI code.
func All() []Agent {
	return []Agent{Claude{}, Codex{}, Gemini{}}
}

// ByID returns the Agent for a known ID. Falls back to Default() for
// the empty string (back-compat with projects scaffolded before the
// sidecar) and panics for genuinely unknown IDs — callers should use
// ParseID first if they're reading user input.
func ByID(id ID) Agent {
	switch id {
	case "", IDClaude:
		return Claude{}
	case IDCodex:
		return Codex{}
	case IDGemini:
		return Gemini{}
	}
	panic("agent: unknown ID " + string(id))
}

// ParseID normalizes a free-form string (TOML config, sidecar file,
// CLI flag) into a known ID. Whitespace is trimmed and the result is
// lowercased. The bool reports whether the parse succeeded; callers
// that want a back-compat default should fall back to Default() on
// false.
func ParseID(s string) (ID, bool) {
	switch ID(strings.ToLower(strings.TrimSpace(s))) {
	case IDClaude:
		return IDClaude, true
	case IDCodex:
		return IDCodex, true
	case IDGemini:
		return IDGemini, true
	}
	return "", false
}

// Default is what missing / invalid agent identifiers resolve to.
// Locked at claude for back-compat: every project that existed before
// the sidecar is implicitly Claude.
func Default() Agent { return Claude{} }

// AllInstalled returns the subset of All() whose Binary() resolves on
// $PATH. Used by:
//
//   - The new-project form's agent picker, so the user only sees
//     options they can actually run.
//   - `ccmux doctor` to confirm "at least one agent is installed".
//
// Context is honored so a slow PATH probe (NFS mounts, network FS)
// can be canceled by the caller.
func AllInstalled(ctx context.Context) []Agent {
	out := []Agent{}
	for _, a := range All() {
		if isInstalled(ctx, a.Binary()) {
			out = append(out, a)
		}
	}
	return out
}

// isInstalled is a thin wrapper over exec.LookPath. Split out so tests
// can swap the hook (see installLookupHook in agent_test.go).
var installLookupHook = func(_ context.Context, bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

func isInstalled(ctx context.Context, bin string) bool {
	return installLookupHook(ctx, bin)
}
