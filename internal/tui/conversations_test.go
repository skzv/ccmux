package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/conversations"
	"github.com/skzv/ccmux/internal/tui/components"
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

func setFocusedConversationSection(t *testing.T, m *conversationsModel, id agent.ID) {
	t.Helper()
	idx, ok := conversationAgentSectionIndex(id)
	if !ok {
		t.Fatalf("unknown conversation section for agent %q", id)
	}
	m.activeSection = idx
	m.cursor = m.sectionCursors[idx]
}

func sameAgentConversations(id agent.ID, n int) []conversations.Conversation {
	now := time.Now()
	out := make([]conversations.Conversation, n)
	for i := range out {
		out[i] = conversations.Conversation{
			ID:           fmt.Sprintf("%s-%02d", id, i),
			Agent:        id,
			Project:      "/Users/skz/Projects/auth-redesign",
			LastActivity: now.Add(-time.Duration(i) * time.Hour),
			Preview:      fmt.Sprintf("%s preview %02d", id, i),
		}
	}
	return out
}

// TestConversationsModel_SetList_PreservesCursorByID — refreshes
// happen often (re-entering the tab, hitting Refresh). The cursor
// must follow the previously-highlighted row even when its index in
// the focused agent section changes — otherwise a new conversation
// appearing above it would silently shift the user's selection.
func TestConversationsModel_SetList_PreservesCursorByID(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	list := fakeConversations()
	m.SetList(list)
	setFocusedConversationSection(t, &m, agent.IDCodex)
	m.cursor = 0 // select codex-yesterday

	// Refresh with the same data plus a new codex row inserted above
	// the selected row inside the Codex section.
	newer := append([]conversations.Conversation{{
		ID:           "codex-just-now",
		Agent:        agent.IDCodex,
		LastActivity: time.Now(),
	}}, list...)
	m.SetList(newer)

	if m.cursor != 1 {
		t.Errorf("cursor = %d, want 1 (codex-yesterday shifted from section idx 0 to 1)", m.cursor)
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
	m.SetList(sameAgentConversations(agent.IDClaude, 3))
	m.cursor = 2

	// Drop the row at cursor; previously-selected ID disappears.
	m.SetList(sameAgentConversations(agent.IDClaude, 2))
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

func TestConversationsModel_View_GroupsKnownAgentsInOrder(t *testing.T) {
	now := time.Now()
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList([]conversations.Conversation{
		{ID: "codex-new", Agent: agent.IDCodex, Project: "/p", LastActivity: now, Preview: "codex newest"},
		{ID: "claude-one", Agent: agent.IDClaude, Project: "/p", LastActivity: now.Add(-time.Hour), Preview: "claude row"},
		{ID: "cursor-one", Agent: agent.IDCursor, Project: "/p", LastActivity: now.Add(-2 * time.Hour), Preview: "cursor row"},
		{ID: "agy-one", Agent: agent.IDAntigravity, Project: "/p", LastActivity: now.Add(-3 * time.Hour), Preview: "agy row"},
		{ID: "codex-old", Agent: agent.IDCodex, Project: "/p", LastActivity: now.Add(-4 * time.Hour), Preview: "codex older"},
	})

	out := m.View(140, 50)
	claudeIdx := strings.Index(out, "Claude")
	codexIdx := strings.Index(out, "Codex")
	cursorIdx := strings.Index(out, "Cursor")
	agyIdx := strings.Index(out, "Agy")
	if claudeIdx < 0 || codexIdx < 0 || cursorIdx < 0 || agyIdx < 0 {
		t.Fatalf("missing one or more section headings:\n%s", out)
	}
	if !(claudeIdx < codexIdx && codexIdx < cursorIdx && cursorIdx < agyIdx) {
		t.Fatalf("section order = Claude:%d Codex:%d Cursor:%d Agy:%d, want Claude < Codex < Cursor < Agy\n%s",
			claudeIdx, codexIdx, cursorIdx, agyIdx, out)
	}
	if !strings.Contains(out, "claude row") {
		t.Fatalf("focused Claude section row missing:\n%s", out)
	}
	for _, hidden := range []string{"codex newest", "cursor row", "agy row"} {
		if strings.Contains(out, hidden) {
			t.Fatalf("inactive agent row %q should not render until its section is focused:\n%s", hidden, out)
		}
	}

	m, _ = m.Update(keyMsg("tab"))
	out = m.View(140, 50)
	if strings.Index(out, "codex newest") > strings.Index(out, "codex older") {
		t.Fatalf("Codex conversations are not newest-first within the section:\n%s", out)
	}
	if strings.Contains(out, "claude row") {
		t.Fatalf("Claude row should not render while Codex is focused:\n%s", out)
	}
}

func TestConversationsModel_View_HidesUnknownAgentsAndShowsEmptySections(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList([]conversations.Conversation{
		{
			ID:           "claude-one",
			Agent:        agent.IDClaude,
			Project:      "/p",
			LastActivity: time.Now(),
			Preview:      "known row",
		},
		{
			ID:           "future-one",
			Agent:        agent.ID("future-agent"),
			Project:      "/p",
			LastActivity: time.Now(),
			Preview:      "unknown row",
		},
	})

	out := m.View(140, 50)
	if strings.Contains(out, "unknown row") || strings.Contains(out, "Other") {
		t.Fatalf("unknown agent conversation should be hidden with no Other section:\n%s", out)
	}
	for _, tc := range []struct {
		agent agent.ID
		want  string
	}{
		{agent.IDCodex, "No conversations for Codex."},
		{agent.IDCursor, "No conversations for Cursor."},
		{agent.IDAntigravity, "No conversations for Agy."},
	} {
		setFocusedConversationSection(t, &m, tc.agent)
		out = m.View(140, 50)
		if !strings.Contains(out, tc.want) {
			t.Fatalf("missing empty state %q:\n%s", tc.want, out)
		}
	}
}

// TestConversationsModel_SetProjectFilter_PreservesSelection — when
// the filter changes, the selected conversation should stick when it
// is still visible in the focused section.
func TestConversationsModel_SetProjectFilter_PreservesSelection(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList(fakeConversations())
	setFocusedConversationSection(t, &m, agent.IDCodex)

	m.SetProjectFilter("/Users/skz/Projects/parser")
	if sel := m.Selected(); sel == nil || sel.ID != "codex-yesterday" {
		t.Errorf("selected = %+v, want codex-yesterday after filter change", sel)
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
	m.SetList(sameAgentConversations(agent.IDClaude, 3))

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

func TestConversationsModel_Update_RowNavigationStaysInFocusedSection(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList([]conversations.Conversation{
		{ID: "claude-0", Agent: agent.IDClaude, Project: "/p", LastActivity: time.Now(), Preview: "claude 0"},
		{ID: "claude-1", Agent: agent.IDClaude, Project: "/p", LastActivity: time.Now().Add(-time.Hour), Preview: "claude 1"},
		{ID: "codex-0", Agent: agent.IDCodex, Project: "/p", LastActivity: time.Now().Add(-2 * time.Hour), Preview: "codex 0"},
	})
	m.cursor = 1 // last row in Claude

	m, _ = m.Update(keyMsg("j"))
	if m.activeSection != 0 {
		t.Fatalf("down moved section focus to %d, want Claude section", m.activeSection)
	}
	if sel := m.Selected(); sel == nil || sel.ID != "claude-1" {
		t.Fatalf("down at section boundary selected %+v, want claude-1", sel)
	}
}

func TestConversationsModel_Update_SwitchesFocusedSections(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList(fakeConversations())

	m, _ = m.Update(keyMsg("tab"))
	if m.activeSection != 1 {
		t.Fatalf("tab activeSection = %d, want Codex", m.activeSection)
	}
	if sel := m.Selected(); sel == nil || sel.ID != "codex-yesterday" {
		t.Fatalf("tab selected %+v, want codex-yesterday", sel)
	}
	m, _ = m.Update(keyMsg("right"))
	if m.activeSection != 2 {
		t.Fatalf("right activeSection = %d, want Cursor", m.activeSection)
	}
	if sel := m.Selected(); sel != nil {
		t.Fatalf("Cursor section should be empty in fixture, selected %+v", sel)
	}
	m, _ = m.Update(keyMsg("shift+tab"))
	if m.activeSection != 1 {
		t.Fatalf("shift+tab activeSection = %d, want Codex", m.activeSection)
	}
	m, _ = m.Update(keyMsg("left"))
	if m.activeSection != 0 {
		t.Fatalf("left activeSection = %d, want Claude", m.activeSection)
	}
}

func TestConversationsModel_Update_SectionSwitchDisarmsDelete(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList(fakeConversations())
	m, _ = m.Update(keyMsg("x"))
	if m.pendingDelete == "" {
		t.Fatal("precondition: delete should be armed")
	}

	m, _ = m.Update(keyMsg("tab"))
	if m.pendingDelete != "" {
		t.Fatalf("section switch should disarm pending delete, got %q", m.pendingDelete)
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
	setFocusedConversationSection(t, &m, agent.IDCodex)
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
	m.SetList(sameAgentConversations(agent.IDClaude, 2))

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
	if m.pendingDelete != "claude-01" {
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
// width. Narrow drops the detail pane; wide keeps it. The "ID  …"
// row only appears in the detail pane, so its presence is a stable
// proxy for "wide layout with detail pane rendered."
func TestConversations_UsesSharedBreakpoint(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList(fakeConversations())
	const detailMarker = "ID"
	narrow := m.View(119, 40)
	wide := m.View(120, 40)
	// Both layouts contain rows with the agent labels and so on; the
	// detail-pane's "ID         <id>" row is the unique marker.
	narrowHasDetail := strings.Contains(narrow, "ID         ")
	wideHasDetail := strings.Contains(wide, "ID         ")
	if narrowHasDetail {
		t.Errorf("width 119 should render the narrow layout (no %s row from detail pane):\n%s", detailMarker, narrow)
	}
	if !wideHasDetail {
		t.Errorf("width 120 should render the wide layout (with detail pane %s row):\n%s", detailMarker, wide)
	}
}

// TestConversations_NarrowLayout — at phone width the conversation
// rows survive (T0) with no overflow, while the inline hint line (T2)
// is dropped.
func TestConversations_NarrowLayout(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList(fakeConversations())
	m, _ = m.Update(keyMsg("tab"))
	out := m.View(50, 40)
	assertNoOverflow(t, out, 50)
	if !strings.Contains(out, "[codex]") {
		t.Errorf("narrow conversations dropped the conversation rows:\n%s", out)
	}
	if strings.Contains(out, "enter resume") {
		t.Errorf("narrow conversations still shows the inline hint (T2):\n%s", out)
	}
}

// TestConversationsModel_ToggleHeadless_FlipsAndPreservesSelection —
// the H keybind flips the headless-visibility flag and keeps the
// selected conversation stable until the refresh result proves it is
// no longer visible.
func TestConversationsModel_ToggleHeadless_FlipsAndPreservesSelection(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList(sameAgentConversations(agent.IDClaude, 3))
	m.cursor = 2

	if m.ShowHeadless() {
		t.Fatal("precondition: ShowHeadless should default to false")
	}
	now := m.ToggleHeadless()
	if !now || !m.ShowHeadless() {
		t.Errorf("ToggleHeadless() = %v, ShowHeadless() = %v, want both true after first toggle", now, m.ShowHeadless())
	}
	if m.cursor != 2 {
		t.Errorf("cursor = %d, want 2 (preserved after toggle)", m.cursor)
	}
	if sel := m.Selected(); sel == nil || sel.ID != "claude-02" {
		t.Errorf("selected = %+v, want claude-02 after toggle", sel)
	}
	// Second toggle flips back.
	now = m.ToggleHeadless()
	if now || m.ShowHeadless() {
		t.Errorf("second toggle should flip back to hidden; got = %v / ShowHeadless = %v", now, m.ShowHeadless())
	}
}

// TestConversationsModel_ToggleHeadless_DisarmsPendingDelete — a
// pending delete is implicitly armed against a specific row in a
// specific filter view. Toggling the filter changes which rows are
// visible, so the prior arm is stale and must clear, just like
// SetList / cursor-move do.
func TestConversationsModel_ToggleHeadless_DisarmsPendingDelete(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList(fakeConversations())
	m, _ = m.Update(keyMsg("x")) // arm row 0
	if m.pendingDelete == "" {
		t.Fatal("precondition: armed")
	}
	m.ToggleHeadless()
	if m.pendingDelete != "" {
		t.Errorf("toggling headless filter should disarm, pendingDelete = %q", m.pendingDelete)
	}
}

// TestConversationsModel_SetShowHeadless_SeedsFromConfig — App.New
// pushes the config value here at startup; the user's H keybind then
// owns the flag for the rest of the session. This pins the entry
// point so a config-only change still works without a TUI toggle.
func TestConversationsModel_SetShowHeadless_SeedsFromConfig(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	if m.ShowHeadless() {
		t.Fatal("default: ShowHeadless should be false")
	}
	m.SetShowHeadless(true)
	if !m.ShowHeadless() {
		t.Errorf("SetShowHeadless(true) did not stick")
	}
}

// TestConversationsModel_HelpBarShowsToggleStatus — the HelpBar
// produced for the Conversations screen has to surface the current
// headless visibility (and the H keybind that flips it). Otherwise
// the toggle is invisible and discoverable only via the help overlay.
// The hint moved from the inline View row into HelpBarProps when the
// app footer was unified onto components.HelpBar.
func TestConversationsModel_HelpBarShowsToggleStatus(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList(fakeConversations())

	hints := m.HelpBarProps(200).Hints
	if got := findHint(hints, "H"); got == nil || !strings.Contains(got.Label, "hidden") {
		t.Errorf("default HelpBar should carry an H hint showing 'hidden', got: %+v", hints)
	}
	m.ToggleHeadless()
	hints = m.HelpBarProps(200).Hints
	if got := findHint(hints, "H"); got == nil || !strings.Contains(got.Label, "shown") {
		t.Errorf("after toggle, HelpBar's H hint should show 'shown', got: %+v", hints)
	}
}

// findHint is a small test helper that walks a HelpBarProps Hints
// slice and returns the entry whose Key matches `key`, or nil.
func findHint(hints []components.KeyHint, key string) *components.KeyHint {
	for i := range hints {
		if hints[i].Key == key {
			return &hints[i]
		}
	}
	return nil
}

// TestConversationsModel_View_DetailMarksHeadlessRow — when a headless
// row IS visible (user has flipped the toggle to inspect their
// automation), the detail pane has to call it out with a mode-specific
// label so the user knows which automation flavour they're about to
// resume. Otherwise a `sdk-cli` or `codex exec` resume comes as a
// surprise.
func TestConversationsModel_View_DetailMarksHeadlessRow(t *testing.T) {
	cases := []struct {
		name      string
		agent     agent.ID
		ep        string
		wantLabel string
	}{
		{"claude sdk-cli", agent.IDClaude, "sdk-cli", "headless / SDK"},
		{"codex codex_exec", agent.IDCodex, "codex_exec", "headless / exec"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newConversations(styles.Default(), DefaultKeymap())
			m.SetList([]conversations.Conversation{
				{
					ID:           "headless-1",
					Agent:        tc.agent,
					Project:      "/p",
					LastActivity: time.Now(),
					Preview:      "automated prompt",
					Entrypoint:   tc.ep,
				},
			})
			m.SetShowHeadless(true) // so the row is visible
			setFocusedConversationSection(t, &m, tc.agent)

			out := m.View(120, 40)
			if !strings.Contains(out, tc.wantLabel) {
				t.Errorf("detail pane should flag headless rows with %q, got:\n%s", tc.wantLabel, out)
			}
		})
	}
}

// TestConversations_CursorVisibleWhenScrolledPastWindow — regression
// for the "scroll down far enough and the cursor row disappears" bug.
// renderList used to walk from index 0 and break at len(rows) >= height,
// so when the cursor was past the row budget the highlighted row was
// never emitted. Windowing should keep the cursor in frame.
func TestConversations_CursorVisibleWhenScrolledPastWindow(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	many := make([]conversations.Conversation, 30)
	for i := range many {
		many[i] = conversations.Conversation{
			ID:           fmt.Sprintf("conv-%02d", i),
			Agent:        agent.IDClaude,
			Project:      "/p",
			LastActivity: time.Now(),
			Preview:      fmt.Sprintf("preview-%02d", i),
		}
	}
	m.SetList(many)
	m.cursor = 28

	out := m.View(120, 15)
	if !strings.Contains(out, "preview-28") {
		t.Errorf("cursor row preview-28 missing from rendered view (clipped at bottom?):\n%s", out)
	}
	// Top-of-list rows should have scrolled out of frame.
	if strings.Contains(out, "preview-00") {
		t.Errorf("preview-00 still visible when cursor is at row 28 — windowing didn't shift")
	}
}
