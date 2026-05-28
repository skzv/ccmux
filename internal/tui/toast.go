package tui

import (
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/tui/styles"
)

// toastController owns transient-notification state on behalf of App.
// Pulled out so the App struct stops carrying four loose fields
// (`toast`, `toastKind`, `toastUntil`, `toastLog`) and the call sites
// just say `a.toasts.Set(...)` / `a.toasts.Clear()` / `a.toasts.Log()`.
//
// State:
//   - current/kind/until: the toast presently displayed in the footer
//     (rendered until `until` falls in the past).
//   - log: small ring buffer (capped at toastLogSize) for the help
//     overlay's "Recent activity" section.
type toastController struct {
	current string
	kind    toastKind
	until   time.Time
	log     []toastEntry
}

const toastLogSize = 10

// Set updates the active toast. ttl<=0 falls back to 3s; errors are
// floored at 8s so they're not blink-and-miss. Appends to the log
// ring buffer.
func (t *toastController) Set(kind toastKind, text string, ttl time.Duration) {
	t.current = text
	t.kind = kind
	if ttl <= 0 {
		ttl = 3 * time.Second
	}
	if kind == toastError && ttl < 8*time.Second {
		ttl = 8 * time.Second
	}
	t.until = time.Now().Add(ttl)
	t.log = append([]toastEntry{{At: time.Now(), Kind: kind, Text: text}}, t.log...)
	if len(t.log) > toastLogSize {
		t.log = t.log[:toastLogSize]
	}
	if dbg := debugLogger(); dbg != nil {
		dbg.Printf("toast[%d] %s", kind, text)
	}
}

// Clear blanks the active toast without touching the log ring.
func (t *toastController) Clear() {
	t.current = ""
}

// Active reports whether a toast should be drawn right now.
func (t *toastController) Active() bool {
	return t.current != "" && time.Now().Before(t.until)
}

// Render returns the styled toast — a rounded-border bubble in the
// kind's accent color with a glyph prefix (✓ ! ✗ ⓘ). Caller is
// responsible for line-truncating to the terminal width and placing
// the bubble in the frame (currently overlaid at the top via
// app.renderTopToast).
func (t *toastController) Render(st styles.Styles) string {
	var fg lipgloss.Color
	var glyph string
	switch t.kind {
	case toastError:
		fg, glyph = st.Semantic.Danger, "✗"
	case toastSuccess:
		fg, glyph = st.Semantic.Success, "✓"
	case toastWarning:
		fg, glyph = st.Semantic.Warning, "!"
	default:
		fg, glyph = st.Semantic.Info, "ⓘ"
	}
	box := lipgloss.NewStyle().
		Border(st.Radius.Soft).
		BorderForeground(fg).
		Foreground(st.P.FG).
		Padding(st.Spacing.XS, st.Spacing.SM)
	prefix := lipgloss.NewStyle().Foreground(fg).Bold(true).Render(glyph)
	return box.Render(prefix + " " + t.current)
}

// Log returns the ring buffer (newest first). Read-only — callers
// should not mutate the slice.
func (t *toastController) Log() []toastEntry {
	return t.log
}
