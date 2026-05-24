package tui

import (
	"os"
	"os/exec"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

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

// openEditorCmd builds the tea.Cmd that suspends the TUI, exec's
// `editor path`, and dispatches `onSuccess` when the editor returns
// cleanly. An editor failure emits an error toast instead. The
// callback is a tea.Msg, not a tea.Cmd, so each caller picks the
// reload message its screen listens for (e.g. notesReloadMsg,
// claudeReloadMsg, configReloadMsg).
func openEditorCmd(editor, path string, onSuccess tea.Msg) tea.Cmd {
	c := exec.Command(editor, path)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		if err != nil {
			return toastMsg{Text: "editor: " + err.Error(), Kind: toastError, Until: nowPlus(5)}
		}
		return onSuccess
	})
}

// keyMatches is a small wrapper because we use the binding-style API but
// don't want every callsite to do a `for _, k := range b.Keys()` loop.
func keyMatches(msg tea.KeyMsg, b key.Binding) bool {
	return key.Matches(msg, b)
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
