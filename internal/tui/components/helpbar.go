package components

import (
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/tui/styles"
)

// HelpBarHeight is the fixed line count HelpBar always renders at.
const HelpBarHeight = 1

// HelpBarProps configures a screen-level footer hint line.
type HelpBarProps struct {
	// Hints are the ordered key bindings to display. Render order
	// matches the slice order; collapse order is by Priority
	// ascending (lowest drops first when width is tight).
	Hints []KeyHint

	// Width is the available render width in terminal cells.
	Width int
}

// KeyHint is one entry in the help bar. Key is the binding glyph
// (e.g. "?", "enter", "ctrl+c"); Label is the human-readable action.
// Priority orders collapse: higher values survive when width is
// insufficient, lowest values drop first.
type KeyHint struct {
	Key      string
	Label    string
	Priority int
}

// HelpBar renders the help line. Entries appear in input order; when
// the line overflows the available width, entries with the lowest
// Priority drop first until the remaining set fits.
func HelpBar(s styles.Styles, p HelpBarProps) string {
	if p.Width < 1 {
		p.Width = 80
	}
	if len(p.Hints) == 0 {
		return ""
	}

	sep := s.Muted.Render(" · ")
	sepW := lipgloss.Width(sep)

	rendered := make([]string, len(p.Hints))
	widths := make([]int, len(p.Hints))
	for i, h := range p.Hints {
		rendered[i] = s.Key.Render(h.Key) + " " + s.Muted.Render(h.Label)
		widths[i] = lipgloss.Width(rendered[i])
	}

	// Drop order: lowest priority first. Ties break by later-in-
	// slice (stable sort with reversed traversal would be cleaner,
	// but the canonical ordering is "drop the rightmost low-priority
	// entry first" which matches typical user expectations).
	dropOrder := make([]int, len(p.Hints))
	for i := range dropOrder {
		dropOrder[i] = i
	}
	sort.SliceStable(dropOrder, func(i, j int) bool {
		return p.Hints[dropOrder[i]].Priority < p.Hints[dropOrder[j]].Priority
	})

	keep := make([]bool, len(p.Hints))
	for i := range keep {
		keep[i] = true
	}

	fits := func() bool {
		total := 0
		first := true
		for i, k := range keep {
			if !k {
				continue
			}
			if !first {
				total += sepW
			}
			total += widths[i]
			first = false
		}
		return total <= p.Width
	}

	di := 0
	for !fits() && di < len(dropOrder) {
		keep[dropOrder[di]] = false
		di++
	}

	parts := make([]string, 0, len(p.Hints))
	for i, k := range keep {
		if k {
			parts = append(parts, rendered[i])
		}
	}
	return strings.Join(parts, sep)
}
