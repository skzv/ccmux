package components

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/tui/styles"
)

type listFixture struct {
	primary, secondary, trailing string
}

func newFixtures() []listFixture {
	return []listFixture{
		{"session-a", "active · myproj", "2m"},
		{"session-b", "idle · otherproj", "1h"},
		{"session-c", "needs input · root", "5s"},
	}
}

func renderFixture(f listFixture) ListItem {
	return ListItem{Primary: f.primary, Secondary: f.secondary, Trailing: f.trailing}
}

func TestList_SelectionShowsAccentBar(t *testing.T) {
	st := styles.Default()
	out := List(st, ListProps[listFixture]{
		Items:  newFixtures(),
		Render: renderFixture,
		Cursor: 1,
		Width:  80,
	})
	if !strings.Contains(out, selectionBar) {
		t.Fatalf("selection bar %q not in render:\n%s", selectionBar, out)
	}
	// Bar prefixes both lines of the multi-line selected row
	// (primary + secondary continuation) but no other rows. One
	// selected row with secondary = 2 bar glyphs; nothing more.
	if got := strings.Count(out, selectionBar); got != 2 {
		t.Fatalf("selection bar count = %d, want 2 (primary+secondary of one selected row):\n%s", got, out)
	}
	// Verify no other row picked up the bar by checking the first
	// and last fixtures' primary lines.
	for _, line := range strings.Split(out, "\n") {
		plain := stripAnsi(line)
		if strings.Contains(plain, "session-a") || strings.Contains(plain, "session-c") {
			if strings.Contains(plain, selectionBar) {
				t.Errorf("non-selected row carries selection bar: %q", plain)
			}
		}
	}
}

func TestList_NoSelectionAtCursorNegative(t *testing.T) {
	st := styles.Default()
	out := List(st, ListProps[listFixture]{
		Items:  newFixtures(),
		Render: renderFixture,
		Cursor: -1,
		Width:  80,
	})
	if strings.Contains(out, selectionBar) {
		t.Fatalf("selection bar present with cursor=-1:\n%s", out)
	}
}

func TestList_HideSecondaryHidesContinuationLine(t *testing.T) {
	st := styles.Default()
	out := List(st, ListProps[listFixture]{
		Items:         newFixtures(),
		Render:        renderFixture,
		Cursor:        0,
		Width:         80,
		HideSecondary: true,
	})
	plain := stripAnsi(out)
	for _, sec := range []string{"active · myproj", "idle · otherproj", "needs input · root"} {
		if strings.Contains(plain, sec) {
			t.Errorf("secondary text %q rendered despite HideSecondary:\n%s", sec, plain)
		}
	}
}

func TestList_SecondaryRendersByDefault(t *testing.T) {
	st := styles.Default()
	out := List(st, ListProps[listFixture]{
		Items:  newFixtures(),
		Render: renderFixture,
		Cursor: 0,
		Width:  80,
	})
	plain := stripAnsi(out)
	for _, sec := range []string{"active · myproj", "idle · otherproj"} {
		if !strings.Contains(plain, sec) {
			t.Errorf("secondary text %q missing:\n%s", sec, plain)
		}
	}
}

func TestList_TrailingAlignsRight(t *testing.T) {
	st := styles.Default()
	out := List(st, ListProps[listFixture]{
		Items:  newFixtures(),
		Render: renderFixture,
		Cursor: -1,
		Width:  60,
	})
	plain := stripAnsi(out)
	// Find the "session-a" primary line and the "2m" trailing on it.
	lines := strings.Split(plain, "\n")
	var primaryLine string
	for _, l := range lines {
		if strings.Contains(l, "session-a") && strings.Contains(l, "2m") {
			primaryLine = l
			break
		}
	}
	if primaryLine == "" {
		t.Fatalf("session-a primary line with 2m not found:\n%s", plain)
	}
	// Trailing should appear AFTER primary, with whitespace between.
	pi := strings.Index(primaryLine, "session-a")
	ti := strings.Index(primaryLine, "2m")
	if !(pi < ti) {
		t.Fatalf("trailing not right of primary: pi=%d ti=%d in %q", pi, ti, primaryLine)
	}
	if !strings.Contains(primaryLine[pi:ti], " ") {
		t.Fatalf("no whitespace gap between primary and trailing: %q", primaryLine)
	}
}

func TestList_PrimaryVisibleAtAllWidths(t *testing.T) {
	st := styles.Default()
	for _, w := range []int{120, 100, 80, 60, 40} {
		out := List(st, ListProps[listFixture]{
			Items:  newFixtures(),
			Render: renderFixture,
			Cursor: 0,
			Width:  w,
		})
		plain := stripAnsi(out)
		// At narrow widths Primary may truncate but the prefix
		// (session-) should always survive.
		if !strings.Contains(plain, "session-") {
			t.Errorf("width=%d: primary text missing:\n%s", w, plain)
		}
	}
}

func TestList_NeverExceedsBodyWidth(t *testing.T) {
	st := styles.Default()
	for _, w := range []int{120, 100, 80, 60, 40, 30, 20, 12} {
		out := List(st, ListProps[listFixture]{
			Items:  newFixtures(),
			Render: renderFixture,
			Cursor: 1,
			Width:  w,
		})
		for _, line := range strings.Split(out, "\n") {
			if got := lipgloss.Width(line); got > w {
				t.Errorf("width=%d: line render width %d > budget; line=%q", w, got, line)
			}
		}
	}
}

func TestList_EmptyItemsRendersEmpty(t *testing.T) {
	st := styles.Default()
	out := List(st, ListProps[listFixture]{
		Items:  nil,
		Render: renderFixture,
		Cursor: 0,
		Width:  80,
	})
	if out != "" {
		t.Fatalf("empty list render = %q, want empty", out)
	}
}
