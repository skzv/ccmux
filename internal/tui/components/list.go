package components

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/skzv/ccmux/internal/tui/styles"
)

// selectionBar is the 1-cell accent indicator drawn at the left edge
// of the selected row. Combined with a slightly elevated background,
// it's the unified selection treatment across Sessions, Conversations,
// Projects, and Notes.
const selectionBar = "▌"

// ListProps configures a selectable-list render.
//
// The list is a stateless render helper: callers own selection state
// (Cursor) and pass in the slice of items each frame. Each item maps
// to a ListItem via Render. Selection draws an accent bar + elevated
// background; secondary text renders muted under the primary line;
// trailing metadata aligns to the right of the primary line.
type ListProps[T any] struct {
	Items  []T
	Render func(T) ListItem

	// Cursor is the index of the selected item, or -1 for "no row
	// selected." Out-of-range values render no selection.
	Cursor int

	// Width is the total render width available, in terminal cells.
	Width int

	// HideSecondary forces single-line rows even when items carry
	// secondary text. Used by adaptive collapse on narrow widths.
	HideSecondary bool
}

// ListItem is the visual content of one row.
type ListItem struct {
	// Primary is the main row text. Required.
	Primary string

	// Secondary is an optional muted continuation line drawn under
	// Primary. Hidden when ListProps.HideSecondary is true.
	Secondary string

	// Trailing is optional right-aligned metadata drawn on the same
	// line as Primary (e.g. a timestamp, count, host tag).
	Trailing string
}

// List renders the selectable list to a single string ready for the
// caller to place in a pane. The string has one logical entry per
// item, multi-line when Secondary is present. Selection styling is
// fixed by the design-system contract and is identical across every
// screen that consumes this helper.
func List[T any](s styles.Styles, p ListProps[T]) string {
	if p.Width < 4 {
		p.Width = 4
	}
	if len(p.Items) == 0 || p.Render == nil {
		return ""
	}

	const prefixW = 2 // "▌ " or "  "
	bodyW := p.Width - prefixW
	if bodyW < 1 {
		bodyW = 1
	}

	selectedBG := lipgloss.NewStyle().Background(s.P.Selected)
	barStyle := lipgloss.NewStyle().Foreground(s.Semantic.Accent)

	rows := make([]string, 0, len(p.Items)*2)
	for i, item := range p.Items {
		li := p.Render(item)
		isSel := i == p.Cursor

		var prefix string
		if isSel {
			prefix = barStyle.Render(selectionBar) + " "
		} else {
			prefix = "  "
		}

		primary := composePrimary(s, li.Primary, li.Trailing, bodyW)

		lines := []string{prefix + primary}
		if li.Secondary != "" && !p.HideSecondary {
			secondary := truncateANSI(s.Muted.Render(li.Secondary), bodyW)
			lines = append(lines, prefix+secondary)
		}

		if isSel {
			for j := range lines {
				lines[j] = selectedBG.Render(padToWidth(lines[j], p.Width))
			}
		}

		rows = append(rows, strings.Join(lines, "\n"))
	}
	return strings.Join(rows, "\n")
}

// RenderListRow applies the unified design-system row treatment to a
// single already-rendered content string. Use this when a screen's
// list structure (interleaved subheaders, host groups, etc.) doesn't
// fit the simple Items+Render shape of List. Selected rows get the
// accent bar prefix and the elevated-background fill across the
// pane width; unselected rows get a 2-space prefix.
func RenderListRow(s styles.Styles, content string, selected bool, width int) string {
	var prefix string
	if selected {
		prefix = lipgloss.NewStyle().Foreground(s.Semantic.Accent).Render(selectionBar) + " "
	} else {
		prefix = "  "
	}
	line := prefix + content
	if selected {
		line = lipgloss.NewStyle().Background(s.P.Selected).Render(padToWidth(line, width))
	}
	return line
}

// padToWidth right-extends a styled line to exactly `width` cells
// with trailing spaces. Used before applying a background style so
// the bg paints across the full row, not just the content.
func padToWidth(line string, width int) string {
	w := lipgloss.Width(line)
	if w >= width {
		return line
	}
	return line + strings.Repeat(" ", width-w)
}

// composePrimary lays out the primary line: Primary text followed by
// at least 1 cell of gap and the right-aligned Trailing (muted). When
// there's no room for Trailing, Primary is truncated to width and
// Trailing is dropped.
func composePrimary(s styles.Styles, primary, trailing string, bodyW int) string {
	if trailing == "" {
		return truncateANSI(primary, bodyW)
	}
	pw := lipgloss.Width(primary)
	trailing = s.Muted.Render(trailing)
	tw := lipgloss.Width(trailing)
	if pw+tw+1 > bodyW {
		// Not enough room for trailing — drop it; truncate primary.
		return truncateANSI(primary, bodyW)
	}
	gap := bodyW - pw - tw
	if gap < 1 {
		gap = 1
	}
	return primary + strings.Repeat(" ", gap) + trailing
}

func truncateANSI(s string, n int) string {
	if lipgloss.Width(s) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	return ansi.Truncate(s, n, "…")
}
