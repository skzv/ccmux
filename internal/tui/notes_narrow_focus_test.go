package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/notes"
)

func narrowNotesFixture() notesModel {
	return notesWith([]notes.Entry{
		{Rel: "a.md", Dir: "", Display: "a", Path: "/tmp/ccmux/a.md"},
		{Rel: "b.md", Dir: "", Display: "b", Path: "/tmp/ccmux/b.md"},
		{Rel: "c.md", Dir: "", Display: "c", Path: "/tmp/ccmux/c.md"},
	}, 0)
}

// TestNotes_PhoneWidthNeverFocusesHiddenPreview — regression: below
// 100 columns the Notes screen renders the list only, yet → and Tab on
// a file still moved focus to the (invisible) preview; j/k then
// scrolled that hidden viewport and the list looked frozen.
func TestNotes_PhoneWidthNeverFocusesHiddenPreview(t *testing.T) {
	for _, key := range []string{"tab", "right", "l"} {
		t.Run(key, func(t *testing.T) {
			m := narrowNotesFixture()
			m.SetSize(60, 40)
			m, _ = m.Update(keyMsg(key))
			if m.focus != focusList {
				t.Fatalf("%q at width 60 moved focus to the hidden preview", key)
			}
			m, _ = m.Update(keyMsg("j"))
			if m.cursor != 1 {
				t.Errorf("after %q then j, cursor = %d, want 1 (the list must still navigate)", key, m.cursor)
			}
		})
	}
}

// TestNotes_PhoneWidthWheelScrollsList — at phone width the whole
// screen is the list, so a wheel event right of the old 1/3 split must
// move the list cursor, not the hidden preview.
func TestNotes_PhoneWidthWheelScrollsList(t *testing.T) {
	m := narrowNotesFixture()
	m.SetSize(60, 40)
	m, _ = m.Update(tea.MouseMsg{X: 50, Y: 5, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	if m.cursor != 1 {
		t.Errorf("wheel at x=50 on a 60-col screen: cursor = %d, want 1", m.cursor)
	}
}

// TestNotes_WideWidthStillFocusesPreview — the split layout keeps the
// existing behaviour, and shrinking to phone width while the preview
// has focus hands focus back to the list.
func TestNotes_WideWidthStillFocusesPreview(t *testing.T) {
	m := narrowNotesFixture()
	m.SetSize(120, 40)
	m, _ = m.Update(keyMsg("tab"))
	if m.focus != focusPreview {
		t.Fatal("tab at width 120 should focus the preview pane")
	}
	m.SetSize(60, 40)
	if m.focus != focusList {
		t.Error("resizing to phone width left focus on the hidden preview")
	}
}
