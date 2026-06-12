// Package agent abstracts over Claude Code, Codex (OpenAI),
// Antigravity CLI (Google), Cursor, and pi (Earendil Works) — the
// interactive AI coding agents ccmux can supervise inside a tmux
// session. Adding another agent later is a matter of dropping a new
// `Agent` implementation alongside the existing ones and registering
// it in All().
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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ID is the canonical agent identifier. The string values match what
// gets written into <project>/.ccmux/agent so they are *load-bearing*
// — don't rename them without a migration.
type ID string

const (
	IDClaude      ID = "claude"
	IDCodex       ID = "codex"
	IDAntigravity ID = "antigravity"
	IDCursor      ID = "cursor"
	IDPi          ID = "pi"
	IDGrok        ID = "grok"
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
//   - ConfigRoot: ~/.claude, ~/.codex, ~/.gemini/antigravity-cli. The
//     Agents config tab uses this to pick which file to display.
//   - TranscriptsRoot: where per-project JSONL transcripts live. The
//     usage panel walks this tree to total tokens / prompts.
//   - InitialPrompt: the first message ccmux types into the agent's
//     prompt after the new session is up. Per-agent because the
//     bootstrapping rituals differ (Claude runs /init; Codex/Antigravity
//     have their own conventions).
//   - Classify: per-agent heuristic for "needs input" detection. Each
//     CLI has a different prompt frame, so this is the most agent-
//     specific method.
//
// Implementations live in this package next to this file so an audit
// can read all three (claude.go, codex.go, antigravity.go) without
// jumping around.
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

// Commands holds user-configured executable paths for agents. Empty
// fields preserve the default binary-on-PATH behavior for that agent.
//
// ClaudeModel is the optional default model ccmux pins for new
// Claude sessions it launches. Non-empty values are exported as
// `ANTHROPIC_MODEL` in front of the shell-command chain so the value
// survives the `claude --continue || claude || zsh` fallback (a
// flag-based `--model X` would only apply to the first invocation).
// Empty inherits Claude Code's own settings.json / built-in default.
type Commands struct {
	Claude      string
	Codex       string
	Antigravity string
	Cursor      string
	Pi          string
	Grok        string
	ClaudeModel string
}

// All returns every supported agent in canonical order
// (claude → codex → antigravity → cursor). Order matters: pickers default to
// the first installed entry, and that lets us bias new users toward
// Claude without making it special-case in UI code.
func All() []Agent {
	return []Agent{Claude{}, Codex{}, Antigravity{}, Cursor{}, Pi{}, Grok{}}
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
	case IDAntigravity, "gemini":
		// "gemini" alias: projects scaffolded against the Gemini CLI
		// before the Antigravity rebrand wrote "gemini" into their
		// sidecar. Map it to Antigravity so those projects keep working
		// without a migration step.
		return Antigravity{}
	case IDCursor:
		return Cursor{}
	case IDPi:
		return Pi{}
	case IDGrok:
		return Grok{}
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
	case IDAntigravity, "gemini":
		// "gemini" alias retained for back-compat — see ByID.
		return IDAntigravity, true
	case IDCursor:
		return IDCursor, true
	case IDPi:
		return IDPi, true
	case IDGrok:
		return IDGrok, true
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

// AllAvailable returns every supported agent ccmux can launch from the
// current process: either the default binary resolves on PATH, or setup
// pinned an executable command override in config.
func AllAvailable(ctx context.Context, commands Commands) []Agent {
	out := []Agent{}
	for _, a := range All() {
		if isInstalled(ctx, a.Binary()) || commandAvailable(ctx, commandOverride(a.ID(), commands)) {
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

func commandAvailable(ctx context.Context, command string) bool {
	command = strings.TrimSpace(command)
	if command == "" {
		return false
	}
	if filepath.IsAbs(command) || strings.ContainsRune(command, os.PathSeparator) {
		return Executable(command)
	}
	return isInstalled(ctx, command)
}

// ExecutableCandidates returns every executable named bin found on a
// PATH-like string, preserving PATH order and deduplicating repeated
// absolute paths. It is intentionally pure with respect to PATH reads
// so setup/doctor tests can inject a fixture path without mutating the
// process environment.
func ExecutableCandidates(bin, pathEnv string) []string {
	if bin == "" {
		return nil
	}
	seen := map[string]bool{}
	out := []string{}
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, bin)
		if !Executable(candidate) {
			continue
		}
		abs, err := filepath.Abs(candidate)
		if err != nil {
			abs = candidate
		}
		if seen[abs] {
			continue
		}
		seen[abs] = true
		out = append(out, abs)
	}
	return out
}

// Executable reports whether path names an executable file.
func Executable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}

// Candidates returns every executable for an agent on the current PATH.
func Candidates(a Agent) []string {
	return ExecutableCandidates(a.Binary(), os.Getenv("PATH"))
}

// LaunchCmd resolves the tmux shell command for an agent, honoring a
// configured executable path when present. The configured value is used
// only as the argv[0] command token; flags and shell fallbacks keep the
// agent-specific shape from the built-in implementations.
func LaunchCmd(id ID, continueFlag bool, commands Commands) string {
	a := ByID(id)
	return launchCmdWithBinary(a, configuredBinary(a.ID(), a.Binary(), commands), continueFlag, commands.ClaudeModel)
}

// ResumeArgs resolves the argv vector for resuming one specific
// conversation with optional configured command substitution.
func ResumeArgs(id ID, conversationID string, commands Commands) []string {
	if conversationID == "" {
		return nil
	}
	switch id {
	case IDClaude:
		return []string{configuredBinary(IDClaude, "claude", commands), "--resume", conversationID}
	case IDCodex:
		return []string{configuredBinary(IDCodex, "codex", commands), "resume", conversationID}
	case IDAntigravity:
		return []string{configuredBinary(IDAntigravity, "agy", commands), "--conversation", conversationID}
	case IDCursor:
		return []string{configuredBinary(IDCursor, "cursor-agent", commands), "--resume", conversationID}
	case IDPi:
		// pi resumes a specific session by partial UUID via
		// `--session <id>` (`pi --help`).
		return []string{configuredBinary(IDPi, "pi", commands), "--session", conversationID}
	case IDGrok:
		// grok resumes a specific session via `-r, --resume <ID>`
		// (docs.x.ai/build/cli/headless-scripting).
		return []string{configuredBinary(IDGrok, "grok", commands), "--resume", conversationID}
	}
	return nil
}

func configuredBinary(id ID, fallback string, commands Commands) string {
	if command := commandOverride(id, commands); command != "" {
		return command
	}
	return fallback
}

func commandOverride(id ID, commands Commands) string {
	switch id {
	case IDClaude:
		if strings.TrimSpace(commands.Claude) != "" {
			return strings.TrimSpace(commands.Claude)
		}
	case IDCodex:
		if strings.TrimSpace(commands.Codex) != "" {
			return strings.TrimSpace(commands.Codex)
		}
	case IDAntigravity:
		if strings.TrimSpace(commands.Antigravity) != "" {
			return strings.TrimSpace(commands.Antigravity)
		}
	case IDCursor:
		if strings.TrimSpace(commands.Cursor) != "" {
			return strings.TrimSpace(commands.Cursor)
		}
	case IDPi:
		if strings.TrimSpace(commands.Pi) != "" {
			return strings.TrimSpace(commands.Pi)
		}
	case IDGrok:
		if strings.TrimSpace(commands.Grok) != "" {
			return strings.TrimSpace(commands.Grok)
		}
	}
	return ""
}

func launchCmdWithBinary(a Agent, binary string, continueFlag bool, claudeModel string) string {
	cmd := shellQuote(binary)
	// Pin ANTHROPIC_MODEL for Claude only; other agents read their
	// model selection from their own config files. Use `export` (not
	// a per-command FOO=bar prefix) so the value persists across the
	// `claude || claude || zsh` fallback chain — a per-command prefix
	// would only apply to the first invocation, leaving the user's
	// retry shell or the bare shell fallback running un-pinned.
	prefix := ""
	if a.ID() == IDClaude {
		if model := strings.TrimSpace(claudeModel); model != "" {
			prefix = "export ANTHROPIC_MODEL=" + shellQuote(model) + "; "
		}
	}
	if !continueFlag {
		return prefix + cmd
	}
	switch a.ID() {
	case IDCursor:
		return prefix + cmd + " resume || " + cmd + " || zsh || bash || sh"
	}
	// claude / codex / antigravity / pi all take `--continue`.
	return prefix + cmd + " --continue || " + cmd + " || zsh || bash || sh"
}

// ShellQuote quotes one shell token using POSIX single-quote rules.
func ShellQuote(s string) string {
	return shellQuote(s)
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'A' && r <= 'Z') &&
			!(r >= 'a' && r <= 'z') &&
			!(r >= '0' && r <= '9') &&
			!strings.ContainsRune("@%_+=:,./-", r)
	}) == -1 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
