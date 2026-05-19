package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/conversations"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// fakeConversations builds three rows spanning all three agents so
// tests don't have to repeat the boilerplate. The IDs are stable so
// the cursor-preservation tests can re-find rows by ID.
func fakeConversations() []conversations.Conversation {
	now := time.Now()
	return []conversations.Conversation{
		{
			ID:           "claude-most-recent",
			Agent:        agent.IDClaude,
			Project:      "/Users/skz/Projects/auth-redesign",
			LastActivity: now,
			Preview:      "rebuild login with passkeys",
		},
		{
			ID:           "codex-yesterday",
			Agent:        agent.IDCodex,
			Project:      "/Users/skz/Projects/parser",
			LastActivity: now.Add(-24 * time.Hour),
			Preview:      "refactor the rollout walker",
		},
		{
			ID:           "antigravity-last-week",
			Agent:        agent.IDAntigravity,
			Project:      "/Users/skz/Projects/auth-redesign",
			LastActivity: now.Add(-7 * 24 * time.Hour),
			// Antigravity rows have empty Preview by design.
		},
	}
}

// TestConversationsModel_SetList_PreservesCursorByID — refreshes
// happen often (re-entering the tab, hitting Refresh). The cursor
// must follow the previously-highlighted row even when its index
// changes — otherwise a new conversation appearing at position 0
// would silently shift the user's selection.
func TestConversationsModel_SetList_PreservesCursorByID(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	list := fakeConversations()
	m.SetList(list)
	m.cursor = 1 // select codex-yesterday

	// Refresh with the same data plus a new claude row inserted at top.
	newer := append([]conversations.Conversation{{
		ID:           "claude-just-now",
		Agent:        agent.IDClaude,
		LastActivity: time.Now(),
	}}, list...)
	m.SetList(newer)

	if m.cursor != 2 {
		t.Errorf("cursor = %d, want 2 (codex-yesterday shifted from idx 1 to 2)", m.cursor)
	}
	if sel := m.Selected(); sel == nil || sel.ID != "codex-yesterday" {
		t.Errorf("selected = %+v, want id=codex-yesterday", sel)
	}
}

// TestConversationsModel_SetList_ClampsOnShrinkage — when the list
// shrinks below the cursor index AND the previously-selected ID is
// gone, the cursor must clamp to the last valid row instead of
// pointing into the void.
func TestConversationsModel_SetList_ClampsOnShrinkage(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList(fakeConversations())
	m.cursor = 2

	// Drop the row at cursor; previously-selected ID disappears.
	m.SetList(fakeConversations()[:2])
	if m.cursor != 1 {
		t.Errorf("cursor = %d, want 1 (clamped to last)", m.cursor)
	}
}

// TestConversationsModel_Filtered_SubstringProject pins the filter
// semantics: case-insensitive substring on Project. A drill-down from
// the Projects tab passes the full absolute path; partial-substring
// behavior also lets `:filter foo` (future) do the right thing.
func TestConversationsModel_Filtered_SubstringProject(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList(fakeConversations())

	// "auth-redesign" matches the claude and antigravity rows.
	m.SetProjectFilter("/Users/skz/Projects/auth-redesign")
	visible := m.filtered()
	if len(visible) != 2 {
		t.Fatalf("filter len = %d, want 2 (claude + antigravity)", len(visible))
	}
	for _, c := range visible {
		if !strings.Contains(c.Project, "auth-redesign") {
			t.Errorf("filter returned non-matching row: %+v", c)
		}
	}

	// Case-insensitive: AUTH-REDESIGN should match.
	m.SetProjectFilter("AUTH-REDESIGN")
	if got := len(m.filtered()); got != 2 {
		t.Errorf("uppercase filter len = %d, want 2", got)
	}

	// Empty filter returns the full list.
	m.SetProjectFilter("")
	if got := len(m.filtered()); got != 3 {
		t.Errorf("empty filter len = %d, want 3", got)
	}
}

// TestConversationsModel_SetProjectFilter_ResetsCursor — when the
// filter changes, a stale cursor pointing into the old filtered slice
// would be confusing (selection appears to skip rows). Reset to top.
func TestConversationsModel_SetProjectFilter_ResetsCursor(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList(fakeConversations())
	m.cursor = 2

	m.SetProjectFilter("/Users/skz/Projects/auth-redesign")
	if m.cursor != 0 {
		t.Errorf("cursor = %d, want 0 (reset after filter change)", m.cursor)
	}
}

// TestConversationsModel_SetProjectFilter_NoChangeKeepsCursor — re-
// applying the same filter shouldn't bounce the cursor to the top.
// Important: when the App re-enters the screen via the same Projects
// drill-down, the user's last selection should stick.
func TestConversationsModel_SetProjectFilter_NoChangeKeepsCursor(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList(fakeConversations())
	m.SetProjectFilter("/Users/skz/Projects/auth-redesign")
	m.cursor = 1

	m.SetProjectFilter("/Users/skz/Projects/auth-redesign") // re-apply
	if m.cursor != 1 {
		t.Errorf("cursor = %d, want 1 (filter unchanged)", m.cursor)
	}
}

// TestConversationsModel_Update_NavigatesCursor — j/k and arrow keys
// move the cursor through the filtered list, respecting bounds.
func TestConversationsModel_Update_NavigatesCursor(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList(fakeConversations())

	// Down from 0 → 1.
	m, _ = m.Update(keyMsg("j"))
	if m.cursor != 1 {
		t.Errorf("after j cursor = %d, want 1", m.cursor)
	}
	// Down to last (2).
	m, _ = m.Update(keyMsg("j"))
	if m.cursor != 2 {
		t.Errorf("after second j cursor = %d, want 2", m.cursor)
	}
	// Down past last must NOT overshoot.
	m, _ = m.Update(keyMsg("j"))
	if m.cursor != 2 {
		t.Errorf("after third j cursor = %d, want 2 (clamped)", m.cursor)
	}
	// Up.
	m, _ = m.Update(keyMsg("k"))
	if m.cursor != 1 {
		t.Errorf("after k cursor = %d, want 1", m.cursor)
	}
	// Up past 0 must NOT undershoot.
	m, _ = m.Update(keyMsg("k"))
	m, _ = m.Update(keyMsg("k"))
	m, _ = m.Update(keyMsg("k"))
	if m.cursor != 0 {
		t.Errorf("after k×4 cursor = %d, want 0 (clamped)", m.cursor)
	}
}

// TestConversationsModel_Update_EscClearsFilter — Esc on the screen
// drops the project filter and returns to the global view. The
// per-project drill-down would be a trap without this exit.
func TestConversationsModel_Update_EscClearsFilter(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList(fakeConversations())
	m.SetProjectFilter("/Users/skz/Projects/auth-redesign")
	if len(m.filtered()) != 2 {
		t.Fatalf("precondition: filter should be active")
	}

	m, _ = m.Update(keyMsg("esc"))
	if m.projectFilter != "" {
		t.Errorf("projectFilter = %q after esc, want empty", m.projectFilter)
	}
	if len(m.filtered()) != 3 {
		t.Errorf("after esc filter len = %d, want 3 (all rows)", len(m.filtered()))
	}
}

// TestConversationsModel_Selected_RespectsFilter — Selected must
// return the row at the cursor in the FILTERED view, not the absolute
// list. Otherwise pressing Enter after filtering would resume an
// unrelated conversation.
func TestConversationsModel_Selected_RespectsFilter(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList(fakeConversations())
	m.SetProjectFilter("/Users/skz/Projects/parser") // only codex-yesterday matches

	sel := m.Selected()
	if sel == nil {
		t.Fatal("Selected returned nil under filter")
	}
	if sel.ID != "codex-yesterday" {
		t.Errorf("Selected.ID = %q, want codex-yesterday", sel.ID)
	}
}

// TestConversationsModel_Selected_EmptyList — defensive: cursor on
// empty list returns nil rather than panicking. Bubble Tea models can
// get key events before any data lands.
func TestConversationsModel_Selected_EmptyList(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	if sel := m.Selected(); sel != nil {
		t.Errorf("Selected on empty list = %+v, want nil", sel)
	}
}

// TestConversationsModel_View_LoadingPlaceholder — while a refresh is
// in flight, the user shouldn't see an empty list and assume "no
// conversations exist." A "Loading…" placeholder must surface.
func TestConversationsModel_View_LoadingPlaceholder(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetLoading(true)
	out := m.View(120, 40)
	if !strings.Contains(out, "Loading") {
		t.Errorf("loading view should mention Loading, got:\n%s", out)
	}
}

// TestConversationsModel_View_EmptyEmpty — empty list (not loading,
// no error) shows the install-hint rather than a silent void.
func TestConversationsModel_View_EmptyEmpty(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList(nil)
	out := m.View(120, 40)
	if !strings.Contains(out, "No conversations") {
		t.Errorf("empty view should say 'No conversations', got:\n%s", out)
	}
}

// TestConversationsModel_View_FilteredEmpty — filter that matches
// nothing should surface a more specific empty-state message that
// mentions the filter, so the user can act on it (clear filter vs.
// install an agent).
func TestConversationsModel_View_FilteredEmpty(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList(fakeConversations())
	m.SetProjectFilter("/path/that/matches/nothing")
	out := m.View(120, 40)
	// Either a "no conversations for project" message or an empty
	// list is acceptable — we just want to NOT show the wrong
	// "install agent" hint when conversations DO exist (just not for
	// this project).
	if strings.Contains(out, "Run claude / codex / agy") {
		t.Errorf("filtered-empty view should not show install hint, got:\n%s", out)
	}
}

// TestConversationsModel_View_ErrorState — walker errors must surface
// visibly. Silent failure on the file-walker would hide install
// problems and confuse the user.
func TestConversationsModel_View_ErrorState(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetLoadErr("permission denied: ~/.claude/projects")
	out := m.View(120, 40)
	if !strings.Contains(out, "permission denied") {
		t.Errorf("error view should surface the error text, got:\n%s", out)
	}
}
