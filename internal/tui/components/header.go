package components

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/skzv/ccmux/internal/tui/styles"
)

// HeaderHeight is the fixed line count Header always renders. Two
// lines: the title + chip row and a 1-cell accent rule beneath it.
// Callers subtract this from their vertical height budget.
const HeaderHeight = 2

// HeaderProps configures a screen-level header bar.
//
// The Header renders a single content row split into a left slot
// (Title and optional Breadcrumb) and a right slot (Chips), followed
// by a 1-cell accent rule that anchors the screen frame.
type HeaderProps struct {
	// Title is the primary screen identifier (e.g. "Sessions").
	// Always rendered; truncated last when width is tight.
	Title string

	// Breadcrumb is optional secondary left-slot text (e.g. a
	// project name or current host). Rendered in muted style after
	// Title; dropped before Title is truncated.
	Breadcrumb string

	// Chips are right-slot indicators (status badges, counts, key
	// hints). Dropped entirely first when width is insufficient.
	Chips []Chip

	// Width is the available render width in terminal cells.
	Width int
}

// Chip is a single right-slot indicator. Style is applied to Text
// via lipgloss; callers typically pick a Semantic-colored style off
// styles.Styles (e.g. s.StatusGood, s.Muted).
type Chip struct {
	Text  string
	Style lipgloss.Style
}

// Header renders the screen-level header. The collapse order honors
// the design-system contract: chips drop first, then the breadcrumb,
// before the title is ever truncated. The title remains visible at
// any width >= 40 columns.
func Header(s styles.Styles, p HeaderProps) string {
	if p.Width < 1 {
		p.Width = 80
	}

	left := s.Type.Title.Render(p.Title)
	if p.Breadcrumb != "" {
		left = left + s.Muted.Render("  "+p.Breadcrumb)
	}

	right := renderChips(s, p.Chips)
	row := composeRow(left, right, p.Width, s)

	rule := lipgloss.NewStyle().Foreground(s.Semantic.Accent).Render(strings.Repeat("─", p.Width))
	return lipgloss.JoinVertical(lipgloss.Left, row, rule)
}

func renderChips(s styles.Styles, chips []Chip) string {
	if len(chips) == 0 {
		return ""
	}
	parts := make([]string, 0, len(chips))
	for _, c := range chips {
		parts = append(parts, c.Style.Render(c.Text))
	}
	sep := s.Muted.Render("  ·  ")
	return strings.Join(parts, sep)
}

// composeRow places left/right slots with at least 2 cells of gap.
// When right doesn't fit, it's dropped. If left+breadcrumb won't fit
// at all, the breadcrumb (after the double-space) is trimmed; if even
// the title alone overflows, it's truncated with an ellipsis.
func composeRow(left, right string, width int, s styles.Styles) string {
	leftW := lipgloss.Width(left)
	rightW := lipgloss.Width(right)

	if right != "" && leftW+rightW+2 <= width {
		gap := width - leftW - rightW
		if gap < 2 {
			gap = 2
		}
		return left + strings.Repeat(" ", gap) + right
	}

	if leftW <= width {
		return left
	}
	return ansi.Truncate(left, width, "…")
}
