package components

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/skzv/ccmux/internal/tui/styles"
)

// stripAnsi is a test helper: get the visible text out of a styled
// string so assertions can ignore color/bold escape sequences.
func stripAnsi(s string) string { return ansi.Strip(s) }

func TestHeader_ConsistentHeightAcrossWidths(t *testing.T) {
	st := styles.Default()
	widths := []int{120, 100, 80, 60, 40}
	for _, w := range widths {
		out := Header(st, HeaderProps{
			Title:      "Sessions",
			Breadcrumb: "myproject",
			Chips: []Chip{
				{Text: "5 sess", Style: st.Muted},
				{Text: "✓ daemon", Style: st.StatusGood},
			},
			Width: w,
		})
		lines := strings.Split(out, "\n")
		if got := len(lines); got != HeaderHeight {
			t.Fatalf("width=%d: header height = %d lines, want %d", w, got, HeaderHeight)
		}
	}
}

func TestHeader_TitleVisibleAt40Columns(t *testing.T) {
	st := styles.Default()
	out := Header(st, HeaderProps{
		Title:      "Sessions",
		Breadcrumb: "very-long-project-name-that-will-collapse",
		Chips: []Chip{
			{Text: "5 sess", Style: st.Muted},
			{Text: "✓ daemon", Style: st.StatusGood},
		},
		Width: 40,
	})
	plain := stripAnsi(out)
	if !strings.Contains(plain, "Sessions") {
		t.Fatalf("title 'Sessions' missing at width 40:\n%s", plain)
	}
}

func TestHeader_DropsChipsBeforeTitle(t *testing.T) {
	st := styles.Default()
	out := Header(st, HeaderProps{
		Title: "Sessions",
		Chips: []Chip{
			{Text: "5 sess", Style: st.Muted},
			{Text: "✓ daemon", Style: st.StatusGood},
		},
		Width: 20, // too narrow for chips
	})
	plain := stripAnsi(out)
	if !strings.Contains(plain, "Sessions") {
		t.Fatalf("title missing at narrow width:\n%s", plain)
	}
	if strings.Contains(plain, "daemon") || strings.Contains(plain, "5 sess") {
		t.Fatalf("chips should have been dropped at width 20:\n%s", plain)
	}
}

func TestHeader_AccentRuleSpansFullWidth(t *testing.T) {
	st := styles.Default()
	w := 80
	out := Header(st, HeaderProps{Title: "Notes", Width: w})
	lines := strings.Split(out, "\n")
	rule := stripAnsi(lines[1])
	if got := lipgloss.Width(rule); got != w {
		t.Fatalf("accent rule width = %d, want %d", got, w)
	}
}
