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

// TestConversationsModel_DeleteArmsThenConfirms — the core of the
// destructive-action guard. First `x` arms (no command fired, the row
// just enters the pending state). Second `x` on the SAME row fires the
// delete command. A single press must never delete.
func TestConversationsModel_DeleteArmsThenConfirms(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList(fakeConversations())

	// First x → arm. No command should fire.
	m, cmd := m.Update(keyMsg("x"))
	if cmd != nil {
		t.Fatal("first x produced a command — delete must require a second press")
	}
	if m.pendingDelete != "claude-most-recent" {
		t.Errorf("first x should arm the selected row, pendingDelete = %q", m.pendingDelete)
	}

	// Second x on the same row → fire.
	m, cmd = m.Update(keyMsg("x"))
	if cmd == nil {
		t.Fatal("second x on the armed row produced no command")
	}
	if m.pendingDelete != "" {
		t.Errorf("pendingDelete should clear when the delete fires, got %q", m.pendingDelete)
	}
	msg := cmd()
	del, ok := msg.(conversationDeletedMsg)
	if !ok {
		t.Fatalf("second x produced %T, want conversationDeletedMsg", msg)
	}
	if del.ID != "claude-most-recent" {
		t.Errorf("delete fired for %q, want claude-most-recent", del.ID)
	}
}

// TestConversationsModel_DeleteDisarmsOnCursorMove — arming a delete
// then moving the cursor must disarm. Otherwise a stale arm on row 1
// plus an `x` meant for row 3 would delete the wrong conversation.
func TestConversationsModel_DeleteDisarmsOnCursorMove(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList(fakeConversations())

	m, _ = m.Update(keyMsg("x")) // arm row 0
	if m.pendingDelete == "" {
		t.Fatal("precondition: row should be armed")
	}
	m, _ = m.Update(keyMsg("j")) // move down
	if m.pendingDelete != "" {
		t.Errorf("cursor move should disarm, pendingDelete = %q", m.pendingDelete)
	}
	// And now an x on the new row arms IT, not fires.
	m, cmd := m.Update(keyMsg("x"))
	if cmd != nil {
		t.Error("x after a disarm should re-arm, not fire a delete")
	}
	if m.pendingDelete != "codex-yesterday" {
		t.Errorf("x should arm the new row, pendingDelete = %q", m.pendingDelete)
	}
}

// TestConversationsModel_DeleteDisarmsOnEsc — esc on an armed row
// backs out of the delete WITHOUT also clearing the project filter.
// The filter-clear is esc's secondary job; a pending delete takes
// precedence so one esc = "never mind."
func TestConversationsModel_DeleteDisarmsOnEsc(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList(fakeConversations())
	m.SetProjectFilter("/Users/skz/Projects/auth-redesign")
	m, _ = m.Update(keyMsg("x")) // arm
	if m.pendingDelete == "" {
		t.Fatal("precondition: armed")
	}

	m, _ = m.Update(keyMsg("esc"))
	if m.pendingDelete != "" {
		t.Errorf("esc should disarm, pendingDelete = %q", m.pendingDelete)
	}
	// The filter must STILL be set — esc consumed by the disarm.
	if m.projectFilter == "" {
		t.Error("esc cleared the filter too; a pending delete should take precedence")
	}
}

// TestConversationsModel_DeleteDisarmsOnRefresh — SetList (a refresh)
// invalidates any armed delete. The list the user armed against is no
// longer on screen, so confirming would be against stale state.
func TestConversationsModel_DeleteDisarmsOnRefresh(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList(fakeConversations())
	m, _ = m.Update(keyMsg("x"))
	if m.pendingDelete == "" {
		t.Fatal("precondition: armed")
	}
	m.SetList(fakeConversations()) // refresh
	if m.pendingDelete != "" {
		t.Errorf("refresh should disarm, pendingDelete = %q", m.pendingDelete)
	}
}

// TestConversationsModel_DeleteEmptyList_NoOp — `x` on an empty list
// must not panic and must not arm anything.
func TestConversationsModel_DeleteEmptyList_NoOp(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m, cmd := m.Update(keyMsg("x"))
	if cmd != nil {
		t.Error("x on empty list should produce no command")
	}
	if m.pendingDelete != "" {
		t.Errorf("x on empty list should not arm, pendingDelete = %q", m.pendingDelete)
	}
}

// TestConversationsModel_View_ArmedRowShowsConfirm — the armed row
// must render a visible confirm prompt so the user knows x-again
// deletes. A silent armed state would make the second x a surprise.
func TestConversationsModel_View_ArmedRowShowsConfirm(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList(fakeConversations())
	m, _ = m.Update(keyMsg("x")) // arm row 0

	out := m.View(120, 40)
	if !strings.Contains(out, "press x to confirm") {
		t.Errorf("armed row should show a confirm prompt:\n%s", out)
	}
}

// TestConversations_UsesSharedBreakpoint — the screen now branches on
// the shared isNarrow (width < 120), not its old derived detail-pane
// width. Narrow drops the detail pane and the inline hint; wide keeps
// both.
func TestConversations_UsesSharedBreakpoint(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList(fakeConversations())
	const hint = "enter resume"
	if narrow := m.View(119, 40); strings.Contains(narrow, hint) {
		t.Errorf("width 119 should render the narrow layout (no detail/hint):\n%s", narrow)
	}
	if wide := m.View(120, 40); !strings.Contains(wide, hint) {
		t.Errorf("width 120 should render the wide layout (detail + hint):\n%s", wide)
	}
}

// TestConversations_NarrowLayout — at phone width the conversation
// rows survive (T0) with no overflow, while the inline hint line (T2)
// is dropped.
func TestConversations_NarrowLayout(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList(fakeConversations())
	out := m.View(50, 40)
	assertNoOverflow(t, out, 50)
	if !strings.Contains(out, "[codex]") {
		t.Errorf("narrow conversations dropped the conversation rows:\n%s", out)
	}
	if strings.Contains(out, "enter resume") {
		t.Errorf("narrow conversations still shows the inline hint (T2):\n%s", out)
	}
}
