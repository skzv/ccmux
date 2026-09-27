// Package claude classifies the live state of a Claude Code session
// based on the visible content of its tmux pane.
package claude

import (
	"context"
	"regexp"
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
	// The bottom of the pane is where every prompt shape lives: the
	// v1 rounded frame on the last line, the v2 ruled input box plus
	// its footer, a v2 dialog, or a shell prompt after a crash.
	bottom := lastNonEmptyLines(trimmed, promptRegionLines)
	if len(bottom) == 0 {
		return StateUnknown
	}
	tail := strings.TrimSpace(bottom[len(bottom)-1])
	switch {
	case looksLikeClaudePrompt(tail), looksLikeClaudeV2Prompt(bottom), looksLikeClaudeV2Dialog(bottom):
		if time.Since(lastChange) >= idleNeedsInput {
			return StateNeedsInput
		}
		return StateActive
	case looksLikeShellPrompt(tail) && !hasClaudeChrome(lastN(bottom, shellRegionLines)):
		return StateError
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

// looksLikeClaudePrompt reports whether line is a border of Claude Code
// v1's rounded input frame (`╭──────╮` / `╰──────╯`) — the shape of the
// last pane line while v1 waits on the user. Mirrors the
// claude_prompt_frame rule in internal/agentdetect/rules/claude.toml.
//
// History of the heuristic:
//
//   - Counting hits across {╭╮╰╯│─>} and matching at ≥2 false-positived
//     on output that emits two of {│,─,>}: `tree` output, `gh`/`bat`
//     headers, the ccmux tmux status bar itself. Each fired a spurious
//     bell + APNs push.
//   - Requiring a rounded corner ╭╮╰╯ plus a second glyph from that set
//     still matched modern shell prompts left behind after Claude
//     crashed: powerlevel10k's framed prompt ends in `╰─❯ ` and
//     oh-my-zsh's bira theme in `╰─$ ` — a false needs_input and bell
//     instead of the crash (error) state.
//
// So now the line must begin (after indentation) with a real border: a
// left corner `╭`/`╰` followed by a run of three or more `─`, or by a
// closing corner `╮`/`╯` joined to it by nothing but `─`. A lone `╰`
// (a capture racing a partial redraw) never matches.
func looksLikeClaudePrompt(line string) bool {
	return v1FrameBorderRE.MatchString(line)
}

// v1FrameBorderRE is the claude_prompt_frame rule's regex, verbatim.
var v1FrameBorderRE = regexp.MustCompile(`^[ \t]*[╭╰](?:─{3,}|─*[╮╯])`)

// promptRegionLines is how many trailing non-empty lines the v2 input
// box and dialog checks scan: a multi-line typed prompt plus the
// footer. Mirrors bottom_non_empty_lines(12) on the v2 rules in
// internal/agentdetect/rules/claude.toml — the engine consults this
// package only when no rule matched, so the two must agree.
const promptRegionLines = 12

// shellRegionLines mirrors claude_shell_prompt's
// bottom_non_empty_lines(4): the lines that must be free of Claude
// chrome before a shell-looking tail is believed to be a shell prompt.
const shellRegionLines = 4

// ruleLinePrefix is the start of a v2 input-box border: a run of at
// least ten box-drawing horizontals.
var ruleLinePrefix = strings.Repeat("─", 10)

// numberedChoiceRE matches the focused row of a v2 selector dialog
// (`❯ 1. Yes`), the shape of every tool-permission prompt.
var numberedChoiceRE = regexp.MustCompile(`^[\s\x{00A0}]*❯[\s\x{00A0}]*\d+\.[\s\x{00A0}]`)

// looksLikeClaudeV2Prompt reports whether the bottom lines show Claude
// Code v2's input box: a `❯` (or `>`) line sandwiched between two
// `────` rules, with any number of typed continuation lines before the
// closing rule. v2 has no rounded corners, and its last line is a
// footer (`⏵⏵ auto mode on …`, `? for shortcuts`), so the single-line
// looksLikeClaudePrompt never sees it.
func looksLikeClaudeV2Prompt(lines []string) bool {
	for i := 0; i+2 < len(lines); i++ {
		if !isRuleLine(lines[i]) || !isInputLine(lines[i+1]) {
			continue
		}
		for _, l := range lines[i+2:] {
			if isRuleLine(l) {
				return true
			}
		}
	}
	return false
}

// looksLikeClaudeV2Dialog reports whether the bottom lines show a v2
// dialog that replaced the input box: a tool-permission prompt or a
// confirm dialog such as the workspace trust check.
func looksLikeClaudeV2Dialog(lines []string) bool {
	joined := strings.ToLower(strings.Join(lines, "\n"))
	if strings.Contains(joined, "do you want to proceed?") {
		return true
	}
	if strings.Contains(joined, "enter to confirm") && strings.Contains(joined, "esc to cancel") {
		return true
	}
	for _, l := range lines {
		if numberedChoiceRE.MatchString(l) {
			return true
		}
	}
	return false
}

// hasClaudeChrome reports whether any line carries Claude UI furniture —
// a v1 frame border, a v2 `────` input-box rule, or the `⏵⏵` mode
// footer. Claude's own footer or a statusline can end in `%`
// (`Context left until auto-compact: 7%`), so a shell-looking tail only
// means a crash when none of this is on screen. Mirrors the `not` block
// of claude_shell_prompt.
//
// The checks are line shapes, not bare glyphs: a crashed session's
// fallback shell can draw rounded corners and `─` runs of its own
// (powerlevel10k's `╭─ ~/demo  main ───── ✔` / `╰─❯ `), and the `❯`
// prompt char is shared by starship, pure and p10k. Claude's v2 `❯`
// input line always sits between two rules, so the rule check covers it.
func hasClaudeChrome(lines []string) bool {
	for _, l := range lines {
		if looksLikeClaudePrompt(l) || isRuleLine(l) || strings.ContainsRune(l, '⏵') {
			return true
		}
	}
	return false
}

func isRuleLine(line string) bool {
	return strings.HasPrefix(strings.TrimLeft(line, " \t"), ruleLinePrefix)
}

func isInputLine(line string) bool {
	l := strings.TrimLeft(line, " \t")
	for _, glyph := range []string{"❯", ">"} {
		if rest, ok := strings.CutPrefix(l, glyph); ok {
			return rest == "" || strings.HasPrefix(rest, " ") || strings.HasPrefix(rest, "\t") || strings.HasPrefix(rest, " ")
		}
	}
	return false
}

// lastNonEmptyLines returns up to n trailing lines of s that aren't
// blank, in order — the same region the engine's
// bottom_non_empty_lines(N) extracts.
func lastNonEmptyLines(s string, n int) []string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, n)
	for i := len(lines) - 1; i >= 0 && len(out) < n; i-- {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		out = append(out, lines[i])
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// lastN returns the final n entries of lines (all of them when shorter).
func lastN(lines []string, n int) []string {
	if len(lines) <= n {
		return lines
	}
	return lines[len(lines)-n:]
}

// shellPromptREs are the prompt shapes of claude_shell_prompt's `any`
// block, verbatim: a `$`/`#`/`%` terminator (bash, root, stock zsh); the
// `❯` prompt char of starship, pure and powerlevel10k, bare or behind
// p10k's `╰─` frame; and oh-my-zsh's default `➜  demo git:(main) ✗ `.
var shellPromptREs = []*regexp.Regexp{
	regexp.MustCompile(`[\$#%]\s*\z`),
	regexp.MustCompile(`❯[\s\x{00A0}]*\z`),
	regexp.MustCompile(`(?m)^[ \t]*➜[ \t]+(?:\S+(?:[ \t]+git:\([^)\n]*\))?(?:[ \t]+✗)?)?\s*\z`),
}

// looksLikeShellPrompt heuristically matches a bare shell prompt (Claude has
// exited or crashed and we're sitting at zsh/bash).
func looksLikeShellPrompt(line string) bool {
	// Make sure it doesn't look like Claude's prompt either.
	if looksLikeClaudePrompt(line) {
		return false
	}
	for _, re := range shellPromptREs {
		if re.MatchString(line) {
			return true
		}
	}
	return false
}
