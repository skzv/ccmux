package tui

import (
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

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
