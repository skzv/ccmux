package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/tui/styles"
)

// These tests pin the unified arrow-key navigation across the Claude
// settings rows. The routing (key → App → agentsM → claude.Update) is
// proven by the agents_picker_e2e tests; here we exercise the nav logic
// itself, plus one full-App render to confirm the selection marker
// actually moves on screen.

func newClaudeNav(t *testing.T) claudeModel {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	return newClaude(styles.Default(), DefaultKeymap())
}

// seedBrowser gives the claude model a browser with N selectable items
// so the top-zone↔browser handoff can be exercised. Without items the
// handoff correctly refuses (nowhere to go).
func seedBrowser(m *claudeModel, n int) {
	items := make([]agentBrowserItem, n)
	for i := range items {
		items[i] = agentBrowserItem{Label: "item", Preview: "preview"}
	}
	m.browser.SetSections("Configured", []agentBrowserSection{
		{Title: "Hooks", Color: lipgloss.Color("#888888"), Items: items},
	})
}

func claudeKey(m claudeModel, s string) claudeModel {
	m2, _ := m.Update(keyMsg(s))
	return m2
}

// TestClaudeNav_DefaultsToTopModelRow — a fresh Claude screen starts
// with the top zone focused and the cursor on the first settings row
// (model), so the first arrow-down moves to effort, not into the
// browser.
func TestClaudeNav_DefaultsToTopModelRow(t *testing.T) {
	m := newClaudeNav(t)
	if !m.focusTop {
		t.Fatal("Claude screen should start with the settings rows focused")
	}
	if m.rowCursor != claudeRowModel {
		t.Errorf("rowCursor = %d, want claudeRowModel(%d)", m.rowCursor, claudeRowModel)
	}
}

// TestClaudeNav_DownUpWalksSettingsRows — down/up move the cursor
// through every settings row and clamp at the ends (top clamps; bottom
// only leaves the zone when there's a browser to enter).
func TestClaudeNav_DownUpWalksSettingsRows(t *testing.T) {
	m := newClaudeNav(t) // empty config dir → browser likely has no items

	want := []int{claudeRowEffort, claudeRowAlwaysThinking, claudeRowYolo, claudeRowClaudeMd, claudeRowSettings}
	for i, w := range want {
		m = claudeKey(m, "down")
		if !m.focusTop || m.rowCursor != w {
			t.Fatalf("after %d downs: focusTop=%v rowCursor=%d, want focusTop=true rowCursor=%d", i+1, m.focusTop, m.rowCursor, w)
		}
	}
	// Walk back up to the top; cursor clamps at model.
	for i := 0; i < 10; i++ {
		m = claudeKey(m, "up")
	}
	if !m.focusTop || m.rowCursor != claudeRowModel {
		t.Errorf("after walking up: focusTop=%v rowCursor=%d, want top/model", m.focusTop, m.rowCursor)
	}
}

// TestClaudeNav_HandoffIntoBrowserAndBack — when the browser has items,
// arrowing down past the last settings row enters the browser, and
// arrowing up from the browser's first item hands focus back to the
// last settings row.
func TestClaudeNav_HandoffIntoBrowserAndBack(t *testing.T) {
	m := newClaudeNav(t)
	seedBrowser(&m, 3)

	// Drive to the last settings row.
	for m.rowCursor < claudeRowSettings {
		m = claudeKey(m, "down")
	}
	if !m.focusTop {
		t.Fatal("should still be in the top zone at the last settings row")
	}
	// One more down → into the browser.
	m = claudeKey(m, "down")
	if m.focusTop {
		t.Fatal("down past the last settings row should enter the browser")
	}
	if !m.browser.AtFirstItem() {
		t.Error("entering the browser should land on its first item")
	}
	// Up from the browser's first item → back to the last settings row.
	m = claudeKey(m, "up")
	if !m.focusTop || m.rowCursor != claudeRowSettings {
		t.Errorf("up from browser top should return to last settings row; focusTop=%v rowCursor=%d", m.focusTop, m.rowCursor)
	}
}

// TestClaudeNav_NoBrowserItemsStaysInTopZone — with an empty browser,
// down at the last settings row must NOT silently lose focus into an
// empty browser; it clamps.
func TestClaudeNav_NoBrowserItemsStaysInTopZone(t *testing.T) {
	m := newClaudeNav(t)
	m.browser.SetSections("Configured", nil) // explicitly empty

	for i := 0; i < 10; i++ {
		m = claudeKey(m, "down")
	}
	if !m.focusTop {
		t.Error("with no browser items, focus must stay in the settings zone")
	}
	if m.rowCursor != claudeRowSettings {
		t.Errorf("cursor should clamp at the last settings row; got %d", m.rowCursor)
	}
}

// TestClaudeNav_EnterOnModelOpensPicker — Enter on the model row opens
// the model picker, same as pressing `m`.
func TestClaudeNav_EnterOnModelOpensPicker(t *testing.T) {
	m := newClaudeNav(t)
	if m.rowCursor != claudeRowModel {
		t.Fatal("precondition: cursor on model row")
	}
	m = claudeKey(m, "enter")
	if m.picker != pickerModel {
		t.Errorf("Enter on model row should open pickerModel, got picker=%d", m.picker)
	}
}

// TestClaudeNav_EnterOnSettingsRowOpensEditor — Enter on the
// settings.json row returns a command (the $EDITOR launch). We can't
// run the editor in a test, but a non-nil cmd proves the row is wired.
func TestClaudeNav_EnterOnSettingsRowOpensEditor(t *testing.T) {
	m := newClaudeNav(t)
	for m.rowCursor < claudeRowSettings {
		m = claudeKey(m, "down")
	}
	_, cmd := m.Update(keyMsg("enter"))
	if cmd == nil {
		t.Error("Enter on the settings.json row should return an editor command")
	}
}

// TestClaudeNav_LetterShortcutMovesCursor — pressing `e` opens the
// effort picker AND moves the row cursor to the effort row so the
// highlight follows the action.
func TestClaudeNav_LetterShortcutMovesCursor(t *testing.T) {
	m := newClaudeNav(t)
	m = claudeKey(m, "e")
	if m.picker != pickerEffort {
		t.Fatalf("`e` should open the effort picker, got %d", m.picker)
	}
	if m.rowCursor != claudeRowEffort {
		t.Errorf("`e` should move the cursor to the effort row; got %d", m.rowCursor)
	}
}

// TestClaudeNav_MarkerRendersOnSelectedRow — the ▌ selection marker
// renders on the focused settings row and moves when the cursor does.
func TestClaudeNav_MarkerRendersOnSelectedRow(t *testing.T) {
	m := newClaudeNav(t)
	body := m.ViewBody(120, 40)
	// The marker (▌) should be present and on the model row by default.
	if !strings.Contains(body, "▌") {
		t.Fatal("no selection marker rendered on the Claude settings rows")
	}
	modelLine := lineContaining(body, "model")
	if !strings.Contains(modelLine, "▌") {
		t.Errorf("selection marker not on the model row by default:\n%q", modelLine)
	}

	// Move down to effort; the marker should follow.
	m = claudeKey(m, "down")
	body = m.ViewBody(120, 40)
	effortLine := lineContaining(body, "effort")
	if !strings.Contains(effortLine, "▌") {
		t.Errorf("marker did not move to the effort row:\n%q", effortLine)
	}
	modelLine = lineContaining(body, "model")
	if strings.Contains(modelLine, "▌") {
		t.Errorf("marker still on the model row after moving down:\n%q", modelLine)
	}
}

// TestE2E_ClaudeArrowNavThroughApp — the full wired path: on the Agents
// screen, an arrow-down then Enter through App.Update opens the right
// picker, and the rendered Agents body shows the marker move. This is
// the end-to-end proof that arrow nav reaches the Claude sub-model and
// renders.
func TestE2E_ClaudeArrowNavThroughApp(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	a := buildAgentsApp(t) // ScreenAgents, Claude active

	// Down once → cursor moves to the effort row; the body marker moves.
	a, _ = updateApp(t, a, keyMsg("down"))
	if a.agentsM.claude.rowCursor != claudeRowEffort {
		t.Fatalf("arrow-down through App did not move the Claude row cursor; got %d", a.agentsM.claude.rowCursor)
	}
	body := agentsBody(a)
	if !strings.Contains(lineContaining(body, "effort"), "▌") {
		t.Error("Agents screen did not render the moved selection marker on the effort row")
	}

	// Enter → opens the effort picker, and the screen renders it.
	a, _ = updateApp(t, a, keyMsg("enter"))
	if !a.agentsM.claude.PickerOpen() {
		t.Fatal("Enter on the effort row did not open a picker through the App")
	}
	if !strings.Contains(agentsBody(a), "Pick reasoning effort") {
		t.Error("effort picker did not render after Enter on its row")
	}
}

// lineContaining returns the first line of s that contains sub, or "".
func lineContaining(s, sub string) string {
	for _, ln := range strings.Split(s, "\n") {
		if strings.Contains(ln, sub) {
			return ln
		}
	}
	return ""
}
