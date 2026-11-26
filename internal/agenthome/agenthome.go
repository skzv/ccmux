// Package agenthome resolves where an agent keeps its files when an
// environment variable can move them. Every reader and writer in ccmux
// that touches these directories (config tabs, conversation listing,
// usage walkers, transcript deletion) goes through here, so a
// relocated home is honoured everywhere or nowhere.
//
//	Claude Code  $CLAUDE_CONFIG_DIR, else ~/.claude  (transcripts in projects/)
//	Codex        $CODEX_HOME,        else ~/.codex   (transcripts in sessions/)
//	Cursor       $CURSOR_HOME,       else ~/.cursor  (transcripts in projects/)
//
// Claude Code and Codex document their variables and keep their
// transcripts under them. Cursor documents no variable that moves
// ~/.cursor (its CLI's CURSOR_CONFIG_DIR moves only cli-config.json),
// so CURSOR_HOME is ccmux's own: it says where to find the directory
// Cursor would otherwise use as ~/.cursor.
//
// The package is a leaf (standard library only) so the agent, config,
// usage and conversations packages can all share it without cycles.
package agenthome

import (
	"os"
	"path/filepath"
	"strings"
)

// The environment variables that relocate an agent's home.
const (
	ClaudeEnv      = "CLAUDE_CONFIG_DIR"
	CodexEnv       = "CODEX_HOME"
	CursorEnv      = "CURSOR_HOME"
	AntigravityEnv = "ANTIGRAVITY_HOME"
)

// Override returns the directory the environment variable name points
// at, or ok=false when it is unset or unusable, in which case callers
// use the agent's default under home.
//
// A leading "~" or "~/" expands against home (a service definition or a
// quoted shell assignment can hand ccmux the tilde unexpanded), and the
// result is cleaned, so a trailing slash or a "/./" changes nothing.
// A value that is still relative after that is ignored: it would
// resolve against whatever directory ccmux happens to run in, which is
// not where the agent put its files, and the transcript-deletion guard
// must never be anchored to such a root.
func Override(name, home string) (string, bool) {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return "", false
	}
	if v == "~" || strings.HasPrefix(v, "~/") || strings.HasPrefix(v, "~"+string(filepath.Separator)) {
		if home == "" {
			return "", false
		}
		v = filepath.Join(home, v[1:])
	}
	if !filepath.IsAbs(v) {
		return "", false
	}
	return filepath.Clean(v), true
}

// resolve is Override with the agent's default under home as fallback.
func resolve(name, home, def string) string {
	if dir, ok := Override(name, home); ok {
		return dir
	}
	return filepath.Join(home, def)
}

// Claude is Claude Code's config directory: $CLAUDE_CONFIG_DIR, else
// home/.claude. settings.json, CLAUDE.md, commands/ and skills/ live
// here, and so do the transcripts (ClaudeProjects).
func Claude(home string) string { return resolve(ClaudeEnv, home, ".claude") }

// ClaudeProjects is where Claude Code writes transcripts:
// <Claude>/projects/<encoded-cwd>/<uuid>.jsonl.
func ClaudeProjects(home string) string { return filepath.Join(Claude(home), "projects") }

// Codex is the Codex CLI's home: $CODEX_HOME, else home/.codex.
func Codex(home string) string { return resolve(CodexEnv, home, ".codex") }

// CodexSessions is where Codex writes rollouts:
// <Codex>/sessions/<yyyy>/<mm>/<dd>/rollout-*.jsonl.
func CodexSessions(home string) string { return filepath.Join(Codex(home), "sessions") }

// Cursor is the directory Cursor uses as ~/.cursor: $CURSOR_HOME, else
// home/.cursor. hooks.json, skills-cursor/ and ai-tracking/ live here,
// and so do the agent transcripts (CursorProjects).
func Cursor(home string) string { return resolve(CursorEnv, home, ".cursor") }

// CursorProjects is where Cursor Agent writes transcripts:
// <Cursor>/projects/<encoded-cwd>/agent-transcripts/<uuid>/<uuid>.jsonl.
func CursorProjects(home string) string { return filepath.Join(Cursor(home), "projects") }

// Antigravity is the Antigravity CLI home: $ANTIGRAVITY_HOME, else
// home/.gemini/antigravity-cli.
func Antigravity(home string) string {
	return resolve(AntigravityEnv, home, filepath.Join(".gemini", "antigravity-cli"))
}

// AntigravityConversations is where Antigravity stores .pb transcripts:
// <Antigravity>/conversations/<uuid>.pb.
func AntigravityConversations(home string) string {
	return filepath.Join(Antigravity(home), "conversations")
}

// AntigravityBrain is where Antigravity stores per-conversation
// workspace artifacts and plain JSONL transcripts:
// <Antigravity>/brain/<uuid>/.system_generated/logs/transcript.jsonl.
func AntigravityBrain(home string) string {
	return filepath.Join(Antigravity(home), "brain")
}

// AntigravitySummaryDB is Antigravity's SQLite conversation index
// (<Antigravity>/conversation_summaries.db) holding titles, previews,
// workspace URIs, timestamps, and parent/subagent relationships.
func AntigravitySummaryDB(home string) string {
	return filepath.Join(Antigravity(home), "conversation_summaries.db")
}

// AntigravityTranscript returns the compact JSONL transcript path for
// conversation id under <Antigravity>/brain/<id>/.system_generated/logs/.
func AntigravityTranscript(home, id string) string {
	return filepath.Join(AntigravityBrain(home), id, ".system_generated", "logs", "transcript.jsonl")
}
