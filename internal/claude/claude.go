// Package claude classifies the live state of a Claude Code session
// based on the visible content of its tmux pane.
package claude

import (
	"context"
	"strings"
	"time"

	"github.com/skzv/ccmux/internal/tmux"
)

// State enumerates the high-level lifecycle of a Claude session.
type State string

const (
	StateUnknown    State = "unknown"
	StateActive     State = "active"      // recent output, Claude is doing work
	StateIdle       State = "idle"        // pane has been quiet for a bit, no input prompt
	StateNeedsInput State = "needs_input" // Claude's prompt is showing and the pane is quiet
	StateError      State = "error"       // pane shows an error or shell prompt (Claude crashed)
)

// Snapshot is a derived view of one Claude session at one point in time.
type Snapshot struct {
	State    State
	Pane     string    // last captured pane content
	Captured time.Time // when we ran capture-pane
}

// ClassifyWithTitle is Classify augmented with the pane's OSC-set
// title (#{pane_title}). Agent CLIs broadcast their state in the
// title far more reliably than in the body — a braille spinner while
// working, explicit strings like "Action Required" when blocked — so
// title evidence is consulted FIRST and overrides body heuristics
// when conclusive. An empty title falls through to legacy body-only
// classification, identical to before this signal existed.
//
// The title is treated as a conclusive override only for unambiguous
// patterns we control (the working-spinner shape). Body-derived
// signals still win when the title is empty or generic.
func ClassifyWithTitle(pane, title string, lastChange time.Time, idleNeedsInput time.Duration) State {
	if state, ok := classifyTitle(title); ok {
		return state
	}
	return Classify(pane, lastChange, idleNeedsInput)
}

// classifyTitle inspects the OSC title and returns a confident state
// when the title carries an unambiguous signal. The boolean reports
// whether the title was conclusive — false means "no opinion, fall
// through to body classification."
func classifyTitle(title string) (State, bool) {
	t := strings.TrimSpace(title)
	if t == "" {
		return StateUnknown, false
	}
	// Braille spinner glyph at the start of the title is the
	// canonical "I am working" broadcast. The unicode block
	// U+2800..U+28FF covers every braille pattern; any of them in
	// the leading position is a working-spinner frame, full stop.
	for _, r := range t {
		if r >= 0x2800 && r <= 0x28FF {
			return StateActive, true
		}
		break // only inspect the first rune
	}
	return StateUnknown, false
}

// Classify decides what State a session is in based on its pane content.
// `lastChange` is when this session's pane content last changed (the caller
// tracks this — typically the daemon's poll loop).
func Classify(pane string, lastChange time.Time, idleNeedsInput time.Duration) State {
	if pane == "" {
		return StateUnknown
	}
	trimmed := strings.TrimRight(pane, " \n\t")
	if trimmed == "" {
		return StateUnknown
	}
	// Look at the last non-empty line for prompt detection.
	lines := strings.Split(trimmed, "\n")
	tail := ""
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			tail = l
			break
		}
	}
	switch {
	case looksLikeShellPrompt(tail) && !looksLikeClaudePrompt(tail):
		return StateError
	case looksLikeClaudePrompt(tail):
		if time.Since(lastChange) >= idleNeedsInput {
			return StateNeedsInput
		}
		return StateActive
	default:
		if time.Since(lastChange) >= idleNeedsInput {
			return StateIdle
		}
		return StateActive
	}
}

// SnapshotSession captures the pane and classifies the session.
// The caller is responsible for storing `lastChange` across calls.
func SnapshotSession(ctx context.Context, session string, lastChange time.Time, idleNeedsInput time.Duration) (Snapshot, error) {
	pane, err := tmux.CapturePane(ctx, session, 200)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{
		State:    Classify(pane, lastChange, idleNeedsInput),
		Pane:     pane,
		Captured: time.Now(),
	}, nil
}

// looksLikeClaudePrompt is a heuristic for "Claude is currently rendering its
// input box waiting on the user." Claude Code uses rounded box-drawing
// characters in its TUI; matching is intentionally loose because the exact
// art changes across versions.
//
// Previously the heuristic counted hits across {╭╮╰╯│─>} and matched
// at ≥2 — which false-positived on output that emits two of {│,─,>}:
// `tree` output, `gh`/`bat` headers, the ccmux tmux status bar
// itself. Each false positive fired a spurious bell + APNs push.
//
// The fix: require one of the rounded corner glyphs ╭╮╰╯. Those
// appear in Claude's input frame and rarely anywhere else (`tree` uses
// the sharp variants ┌┐└┘, status bars use bare verticals, ASCII art
// uses straight lines). We still require a second hit from the
// extended set so a stray rune in user-typed content doesn't trigger.
func looksLikeClaudePrompt(line string) bool {
	if line == "" {
		return false
	}
	if !strings.ContainsAny(line, "╭╮╰╯") {
		return false
	}
	hits := 0
	for _, ch := range "╭╮╰╯│─>" {
		if strings.ContainsRune(line, ch) {
			hits++
		}
	}
	return hits >= 2
}

// looksLikeShellPrompt heuristically matches a bare shell prompt (Claude has
// exited or crashed and we're sitting at zsh/bash).
func looksLikeShellPrompt(line string) bool {
	// Common terminators of a shell prompt.
	if strings.HasSuffix(line, "$") || strings.HasSuffix(line, "#") || strings.HasSuffix(line, "%") {
		// Make sure it doesn't look like Claude's prompt either.
		if !looksLikeClaudePrompt(line) {
			return true
		}
	}
	return false
}
