package tui

import (
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// padLabel right-pads a field label to `width` display columns so
// mixed-width labels (CJK vs Latin) align in the same column. The
// detail panes use fixed-width field labels ("session", "agent",
// "detected"); Chinese translations are shorter but double-width, so a
// byte/rune count would misalign them. lipgloss.Width measures display
// columns, which is what the terminal actually shows.
func padLabel(label string, width int) string {
	if n := width - lipgloss.Width(label); n > 0 {
		return label + strings.Repeat(" ", n)
	}
	return label
}

// pickEditor picks the editor to suspend ccmux into. Order: $VISUAL,
// $EDITOR, then the first of nvim/vim/nano found on PATH; falls back
// to "vi" as the POSIX baseline. Lives in helpers so notes.go,
// claudeconfig.go, app.go, and codexconfig.go all agree.
func pickEditor() string {
	for _, env := range []string{"VISUAL", "EDITOR"} {
		if v := os.Getenv(env); v != "" {
			return v
		}
	}
	for _, bin := range []string{"nvim", "vim", "nano"} {
		if _, err := exec.LookPath(bin); err == nil {
			return bin
		}
	}
	return "vi"
}

// openEditorCmd builds the tea.Cmd that suspends the TUI, runs the
// editor on path, and dispatches `onSuccess` when the editor returns
// cleanly. An editor failure emits an error toast instead. The
// callback is a tea.Msg, not a tea.Cmd, so each caller picks the
// reload message its screen listens for (e.g. notesReloadMsg,
// claudeReloadMsg, configReloadMsg). editor is a command line, as in
// $EDITOR ("code --wait"); empty means pickEditor's choice.
func openEditorCmd(editor, path string, onSuccess tea.Msg) tea.Cmd {
	return tea.Exec(&editorProcess{editor: editor, path: path}, func(err error) tea.Msg { return editorExited(err, onSuccess) })
}

// editorProcess runs the editor for tea.Exec. The command line is
// resolved in Run — once Bubble Tea has handed over the terminal — so
// the PATH lookups stay off the Update goroutine.
type editorProcess struct {
	editor, path   string
	stdin          io.Reader
	stdout, stderr io.Writer
}

func (e *editorProcess) SetStdin(r io.Reader)  { e.stdin = r }
func (e *editorProcess) SetStdout(w io.Writer) { e.stdout = w }
func (e *editorProcess) SetStderr(w io.Writer) { e.stderr = w }

func (e *editorProcess) Run() error {
	argv := editorArgv(e.editor)
	// nocontext: the user's $EDITOR in the foreground; it ends when they quit it.
	c := exec.Command(argv[0], append(argv[1:], e.path)...)
	c.Stdin, c.Stdout, c.Stderr = e.stdin, e.stdout, e.stderr
	return c.Run()
}

// editorArgv turns an editor command line into argv. It used to be run
// as a single program name, so EDITOR="code --wait" looked for a binary
// called "code --wait". When the program isn't on PATH (a stale
// `editor = "nvim"` on a machine without nvim), or editor is empty,
// pickEditor's choice is used instead.
func editorArgv(editor string) []string {
	if argv := splitCommandLine(editor); len(argv) > 0 {
		if _, err := exec.LookPath(argv[0]); err == nil {
			return argv
		}
	}
	if argv := splitCommandLine(pickEditor()); len(argv) > 0 {
		return argv
	}
	return []string{"vi"}
}

// splitCommandLine splits a command line such as an $EDITOR value into
// words the way a POSIX shell would for the simple cases: whitespace
// separates words, single quotes group literally, double quotes group
// with backslash escapes, and a backslash outside quotes escapes the
// next character. No expansion of any kind — just enough for editor
// settings like `code --wait` or `"/Applications/Sublime Text.app/…/subl" -w`.
func splitCommandLine(s string) []string {
	var (
		words   []string
		cur     strings.Builder
		inWord  bool
		quote   rune // 0, '\'', or '"'
		escaped bool
	)
	for _, r := range s {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case quote == '"':
			switch r {
			case '"':
				quote = 0
			case '\\':
				escaped = true
			default:
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, inWord = r, true
		case r == '\\':
			escaped, inWord = true, true
		case r == ' ' || r == '\t' || r == '\n':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words
}

// editorExited is openEditorCmd's return path. Bubble Tea turns mouse
// reporting off while the editor owns the terminal and doesn't turn it
// back on afterwards, so it is re-enabled here alongside the result —
// the reload message on success, an error toast on failure.
func editorExited(err error, onSuccess tea.Msg) tea.Msg {
	result := onSuccess
	if err != nil {
		result = toastMsg{Text: "editor: " + err.Error(), Kind: toastError, Until: nowPlus(5)}
	}
	return tea.BatchMsg{tea.EnableMouseCellMotion, func() tea.Msg { return result }}
}

// keyMatches is a small wrapper because we use the binding-style API but
// don't want every callsite to do a `for _, k := range b.Keys()` loop.
func keyMatches(msg tea.KeyMsg, b key.Binding) bool {
	return key.Matches(msg, b)
}

// isWheelMsg reports whether a mouse event is a scroll-wheel
// notification. Bubble Tea defines IsWheel on tea.MouseEvent, not
// tea.MouseMsg (the two are distinct types via Go's type-definition
// rules), so the convenient receiver-method form isn't reachable
// from a *MouseMsg. We check the Button explicitly for the four
// wheel directions instead.
func isWheelMsg(m tea.MouseMsg) bool {
	switch m.Button {
	case tea.MouseButtonWheelUp,
		tea.MouseButtonWheelDown,
		tea.MouseButtonWheelLeft,
		tea.MouseButtonWheelRight:
		return true
	}
	return false
}

// tickEvery is the Bubble Tea pattern for "send tickMsg in d, then again,
// and again." Each tickMsg arrival reschedules itself.
func tickEvery(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg{At: t} })
}

// nowPlus returns time.Now() + n seconds. Tiny helper for toast TTLs.
func nowPlus(seconds int) time.Time {
	return time.Now().Add(time.Duration(seconds) * time.Second)
}

// windowAroundCursor returns the [start, end) slice indices of a list of
// `total` rows that should be rendered into a pane with capacity `budget`
// rows, ensuring `cursor` is always inside the window. Used by every list
// screen so a cursor that scrolls past the visible window doesn't fall off
// the bottom of the pane invisibly.
//
// Behavior: the window stays anchored at 0 until the cursor reaches the
// last row of the window; then it shifts down one row at a time as the
// cursor advances. The cursor sits on the bottom row of the window
// once scrolled — minimum motion, predictable for the eye.
func windowAroundCursor(cursor, total, budget int) (start, end int) {
	if budget < 1 {
		budget = 1
	}
	if total <= 0 {
		return 0, 0
	}
	if total <= budget {
		return 0, total
	}
	start = 0
	if cursor >= budget {
		start = cursor - budget + 1
	}
	end = start + budget
	if end > total {
		end = total
		start = end - budget
		if start < 0 {
			start = 0
		}
	}
	return start, end
}

// fitPaneLines word-wraps lines to width and returns at most rows of the
// result: the window that keeps unwrapped line `focus` in view (-1 keeps
// the top). A pane rendered from the result never outgrows its height —
// taller content used to be clipped from the bottom of the frame, taking
// the pane's bottom border (and every row below the fold) with it.
func fitPaneLines(lines []string, width, rows, focus int) []string {
	if rows < 1 {
		rows = 1
	}
	wrap := lipgloss.NewStyle().Width(maxInt(1, width))
	var out []string
	focusStart, focusEnd := -1, -1
	for i, line := range lines {
		if i == focus {
			focusStart = len(out)
		}
		out = append(out, strings.Split(wrap.Render(line), "\n")...)
		if i == focus {
			focusEnd = len(out) - 1
		}
	}
	if len(out) <= rows {
		return out
	}
	start := 0
	if focusStart >= 0 {
		// Keep some context above the focused line, and all of it in view.
		start = maxInt(0, minInt(focusStart-rows/3, len(out)-rows))
		if focusEnd >= start+rows {
			start = focusEnd - rows + 1
		}
	}
	return out[start : start+rows]
}
