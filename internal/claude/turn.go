package claude

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// Turn is what one capture of a Claude Code pane shows about the
// agent's turn, beyond the state Classify derives: where Claude's own
// output ends and the user's input begins, and whether Claude says it
// is working.
//
// The daemon needs this to tell Claude working from the user typing.
// Both change the pane body, but only Claude's work changes the part
// above its input box: typing is confined to the box (which grows
// upward with a multi-line prompt, leaving the lines above it as they
// were), and a footer or statusline repaint happens below it.
type Turn struct {
	// Output is the tail of what Claude printed: up to outputTailLines
	// non-empty lines above its input area, each right-trimmed. Empty
	// when HasInput is false.
	Output string
	// HasInput reports that an input area — the v2 input box, a v2
	// dialog that replaced it (a tool permission prompt, the workspace
	// trust check), or the v1 rounded frame — was found at the bottom
	// of the pane.
	HasInput bool
	// Busy reports Claude's working status line — `✻ Cogitating… (12s ·
	// ↓ 1.2k tokens · esc to interrupt)`, shown while a turn runs, just
	// above the input box (a todo list may sit between them) or as an
	// `esc to interrupt` hint in the footer — with the input box as the
	// live bottom of the pane. Typing can't produce it.
	Busy bool
}

const (
	// inputAreaLines is how far above the pane's last line Claude's
	// input area may start: the box with a long typed prompt, or a
	// dialog, plus the footer.
	inputAreaLines = 40
	// outputTailLines is how much of the output above the input area
	// Turn.Output keeps. Only the tail: when a growing input box scrolls
	// the screen, the capture window loses a line at its top, but the
	// lines right above the box stay the same.
	outputTailLines = 20
	// statusSearchLines is how far above the input box the working
	// status line may sit: the todo list Claude shows under it.
	statusSearchLines = 12
)

// statusLineRE matches Claude's working status line: a spinner glyph,
// a gerund with an ellipsis, and a parenthesised hint that ends the
// line and names the interrupt key — `✻ Cogitating… (12s · ↓ 1.2k
// tokens · esc to interrupt)`, `✽ Thinking… (esc to interrupt · ctrl+t
// to show todos)`. Anchored on that whole shape so a line of Claude's
// answer that merely mentions the key doesn't read as work.
var statusLineRE = regexp.MustCompile(`^[ \t]*\S+[ \t]+[^\n]*(?:…|\.\.\.)[^\n]*\([^\n]*\besc to interrupt\b[^\n]*\)[ \t]*$`)

// footerInterruptRE matches the interrupt hint when a Claude version
// shows it in the footer under the input box instead.
var footerInterruptRE = regexp.MustCompile(`\besc to interrupt\b`)

// v1FrameTopRE is the top border of Claude Code v1's rounded input
// frame (`╭──────╮`), the v1 counterpart of the v2 box's opening rule.
var v1FrameTopRE = regexp.MustCompile(`^[ \t]*╭(?:─{3,}|─*╮)`)

// LooksLikeClaude reports whether a pane whose foreground process is a
// generic interpreter (`node` runs Claude Code installed from npm, and
// many other tools) shows Claude Code: its `✳` idle title, its v2 input
// box at the bottom of the pane, or its `Claude Code v…` banner. The v1
// rounded frame doesn't count — Gemini CLI, also a node program, draws
// the same shape.
func LooksLikeClaude(pane, title string) bool {
	if strings.HasPrefix(strings.TrimSpace(title), "✳") {
		return true
	}
	if looksLikeClaudeV2Prompt(lastNonEmptyLines(pane, promptRegionLines)) {
		return true
	}
	return strings.Contains(pane, "Claude Code v")
}

// ReadTurn reads a Claude Code pane for its Turn.
func ReadTurn(pane string) Turn {
	lines := strings.Split(pane, "\n")
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	lines = lines[:end]
	start, closing, below, ok := inputArea(lines)
	if !ok || !footerOnly(lines[below:]) {
		// Nothing, or a frozen frame: Claude was killed and the shell
		// it fell back to prints under its last screen. That input box
		// is no one's; reading the output "above" it as Claude's would
		// take Claude relaunched in that shell (redrawing the screen)
		// for a turn.
		return Turn{}
	}
	out := tailNonEmpty(lines[:start], outputTailLines)
	t := Turn{Output: strings.Join(out, "\n"), HasInput: true}
	if closing < 0 {
		return t // a dialog or the v1 frame: no status line over it
	}
	// The status line sits right above the box; only indented lines (the
	// todo list under it) may come between them. Anything else in
	// between is older output — a status line of an earlier screen, left
	// in the scrollback when Claude was killed and relaunched.
	for i := len(out) - 1; i >= 0 && i >= len(out)-statusSearchLines; i-- {
		if statusLineRE.MatchString(out[i]) {
			t.Busy = true
			return t
		}
		if !indented(out[i]) {
			break
		}
	}
	for _, l := range lines[closing+1:] {
		if footerInterruptRE.MatchString(l) {
			t.Busy = true
			return t
		}
	}
	return t
}

// inputArea finds where Claude's input area starts among lines (the
// pane with trailing blank lines dropped), looking no further up than
// inputAreaLines:
//
//   - the v2 input box: a `────` rule, the `❯` line and whatever is
//     typed after it, a closing rule. start is the opening rule and
//     closing the closing one.
//   - a v2 dialog in place of the box (it opens with a single rule),
//     or the v1 rounded frame: start is that rule or the frame's top
//     border, and closing is -1.
//
// below is where what follows the input area starts — the lines that
// must be Claude's footer (or the dialog's own indented lines) for it
// to be the live bottom of the pane (see footerOnly).
//
// ok is false when neither is on screen: a shell after Claude exited,
// or a full-screen view such as the transcript.
func inputArea(lines []string) (start, closing, below int, ok bool) {
	lo := max(0, len(lines)-inputAreaLines)
	last := -1
	for i := len(lines) - 1; i >= lo; i-- {
		if isRuleLine(lines[i]) || v1FrameTopRE.MatchString(lines[i]) {
			last = i
			break
		}
	}
	switch {
	case last < 0:
		return 0, -1, 0, false
	case !isRuleLine(lines[last]):
		// v1 frame: what follows its bottom border.
		for i := last + 1; i < len(lines); i++ {
			if v1FrameBottomRE.MatchString(lines[i]) {
				return last, -1, i + 1, true
			}
		}
		return last, -1, len(lines), true // drawn no further yet
	case last+1 < len(lines) && isInputLine(lines[last+1]):
		return last, -1, len(lines), true // a box whose closing rule isn't drawn yet
	}
	for i := last - 1; i >= lo; i-- {
		if isRuleLine(lines[i]) {
			if isInputLine(lines[i+1]) {
				return i, last, last + 1, true
			}
			break
		}
	}
	return last, -1, last + 1, true // a dialog
}

// v1FrameBottomRE is the bottom border of the v1 frame (`╰──────╯`).
var v1FrameBottomRE = regexp.MustCompile(`^[ \t]*╰(?:─{3,}|─*╯)`)

// footerOnly reports whether lines — what follows the input box's
// closing rule, or a dialog's opening one — are Claude's own: every
// line indented, as Claude draws its mode line, hints, statusline and
// dialog text. A line at column 0 is something else printed after
// Claude's last frame — the shell prompt and error of a Claude that was
// killed — so the frame above it, status line included, is frozen, not
// live.
func footerOnly(lines []string) bool {
	for _, l := range lines {
		if strings.TrimSpace(l) != "" && !indented(l) {
			return false
		}
	}
	return true
}

// indented reports whether l starts with a space, tab or non-breaking
// space.
func indented(l string) bool {
	r, _ := utf8.DecodeRuneInString(l)
	return r == ' ' || r == '\t' || r == ' '
}

// tailNonEmpty returns up to n trailing non-blank lines of lines, in
// order, each right-trimmed.
func tailNonEmpty(lines []string, n int) []string {
	out := make([]string, 0, n)
	for i := len(lines) - 1; i >= 0 && len(out) < n; i-- {
		l := strings.TrimRight(lines[i], " \t ")
		if strings.TrimSpace(l) == "" {
			continue
		}
		out = append(out, l)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}
