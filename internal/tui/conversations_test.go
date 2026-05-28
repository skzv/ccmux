package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

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
// conversations exist." A centered "Scanning transcripts" panel
// surfaces, anchored by the bubbles/spinner bar frame and a legend
// of every agent root the walker is touching.
func TestConversationsModel_View_LoadingPlaceholder(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetLoading(true)
	out := m.View(120, 40)
	if !strings.Contains(out, "Scanning transcripts") {
		t.Errorf("loading view should show 'Scanning transcripts', got:\n%s", out)
	}
	for _, agentLabel := range []string{"claude", "codex", "cursor", "agy"} {
		if !strings.Contains(out, agentLabel) {
			t.Errorf("loading legend missing %q line, got:\n%s", agentLabel, out)
		}
	}
	spinnerFrame := strings.TrimSpace(m.spinner.View())
	if spinnerFrame == "" {
		t.Fatalf("spinner.View() produced no frame to assert on")
	}
	if !strings.Contains(out, spinnerFrame) {
		t.Errorf("loading view should render the bubbles spinner frame %q, got:\n%s", spinnerFrame, out)
	}
}

// TestConversationsModel_View_SpinnerReplacedByContent — once data
// arrives (SetList lands), the loading panel must give way to the
// conversation rows. A spinner that lingers after load completes
// would suggest a stuck scan that isn't happening.
func TestConversationsModel_View_SpinnerReplacedByContent(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetLoading(true)
	loadingOut := m.View(120, 40)
	if !strings.Contains(loadingOut, "Scanning transcripts") {
		t.Fatalf("precondition: loading view should show the scan panel, got:\n%s", loadingOut)
	}

	m.SetList(fakeConversations())
	loadedOut := m.View(120, 40)
	if strings.Contains(loadedOut, "Scanning transcripts") {
		t.Errorf("post-load view should NOT carry the scan panel, got:\n%s", loadedOut)
	}
	if !strings.Contains(loadedOut, "claude") {
		t.Errorf("post-load view should render the conversation rows, got:\n%s", loadedOut)
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

// TestConversationsModel_View_AgentAccentColoursRowLabel — every
// agent's row label column must render in that agent's accent colour
// (Claude=mauve, Codex=sky, Antigravity=peach, Cursor=teal) sourced
// from the design-system helper. The colour distinguishes which agent
// owns a row at a glance, without changing the rest of the row.
//
// The assertion compares the rendered substring for the agent label
// against an independently-rendered probe from the same helper, so
// the test stays correct regardless of the active color profile.
func TestConversationsModel_View_AgentAccentColoursRowLabel(t *testing.T) {
	s := styles.Default()
	cases := []struct {
		name  string
		id    agent.ID
		label string
	}{
		{"claude", agent.IDClaude, "claude"},
		{"codex", agent.IDCodex, "codex"},
		{"cursor", agent.IDCursor, "cursor"},
		{"antigravity", agent.IDAntigravity, "agy"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newConversations(s, DefaultKeymap())
			m.SetList([]conversations.Conversation{
				{
					ID:           "row-1",
					Agent:        tc.id,
					Project:      "/p",
					LastActivity: time.Now(),
					Preview:      "row preview",
				},
			})
			setFocusedConversationSection(t, &m, tc.id)
			row := m.renderConversationRowContent(m.list[0], 120)
			want := s.AgentAccent(tc.id).Render(tc.label)
			if !strings.Contains(row, want) {
				t.Fatalf("row label for %s missing the agent accent treatment\nwant substring: %q\ngot row: %q", tc.id, want, row)
			}
			// And confirm the muted style is NOT the one used for the
			// label — that's the regression we'd hit if a refactor
			// silently reverted to st.Muted.Render(agentLabel).
			mutedProbe := s.Muted.Render(tc.label)
			if mutedProbe != want && strings.Contains(row, mutedProbe) {
				t.Fatalf("row label for %s rendered with the muted style:\n%s", tc.id, row)
			}
		})
	}
}

// TestConversationsModel_View_AgentAccentColoursActiveSection — the
// active section heading in the agent nav row must wear the same
// accent colour as the matching agent's row labels, so the user can
// confirm at a glance which agent's conversations they are looking at.
func TestConversationsModel_View_AgentAccentColoursActiveSection(t *testing.T) {
	s := styles.Default()
	m := newConversations(s, DefaultKeymap())
	m.SetList(fakeConversations())
	setFocusedConversationSection(t, &m, agent.IDCodex)
	nav := m.renderAgentNav(m.sections())
	wantLabel := s.AgentAccent(agent.IDCodex).Bold(true).Render("Codex 1")
	wantDot := s.AgentAccent(agent.IDCodex).Render("•")
	if !strings.Contains(nav, wantDot) || !strings.Contains(nav, wantLabel) {
		t.Fatalf("active Codex heading missing accent treatment\nwant dot substring: %q\nwant label substring: %q\ngot nav: %q", wantDot, wantLabel, nav)
	}
}

// TestConversationsModel_View_ArmedRowShowsConfirm — the armed row
// must render a visible confirm prompt so the user knows x-again
// deletes. A silent armed state would make the second x a surprise.
// The prompt is a bracketed chip at the row's trailing edge so the
// agent label + timestamp + a truncated preview stay visible on the
// same row.
func TestConversationsModel_View_ArmedRowShowsConfirm(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList(fakeConversations())
	m, _ = m.Update(keyMsg("x")) // arm row 0

	out := m.View(120, 40)
	if !strings.Contains(out, "[delete? x to confirm · esc]") {
		t.Errorf("armed row should render the bracketed delete-confirm chip:\n%s", out)
	}
}

// TestConversationsModel_View_ArmedRowKeepsIdentityVisible — the chip
// replaces the row's tail, NOT its identifying columns. The agent
// label, timestamp, and a truncated preview must all stay readable
// alongside the chip so the user can confirm they're targeting the
// right conversation.
func TestConversationsModel_View_ArmedRowKeepsIdentityVisible(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList(fakeConversations())
	m, _ = m.Update(keyMsg("x")) // arm the Claude row

	out := m.View(160, 40) // wide enough to keep preview alongside the chip
	if !strings.Contains(out, "claude") {
		t.Errorf("armed-row layout dropped the agent label:\n%s", out)
	}
	if !strings.Contains(out, "rebuild login with passkeys") {
		t.Errorf("armed-row layout dropped the preview (truncated form expected):\n%s", out)
	}
	if !strings.Contains(out, "[delete? x to confirm · esc]") {
		t.Errorf("armed-row layout missing the chip:\n%s", out)
	}
}

// TestConversations_UsesSharedBreakpoint — the screen branches on the
// shared isNarrow (width < 120). Narrow drops the detail pane; wide
// keeps it. The detail pane's "last active" field is the marker — it
// only appears in the detail pane, so its presence is a stable proxy
// for "wide layout with detail pane rendered."
func TestConversations_UsesSharedBreakpoint(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList(fakeConversations())
	const detailMarker = "last active"
	narrow := m.View(119, 40)
	wide := m.View(120, 40)
	narrowHasDetail := strings.Contains(narrow, detailMarker)
	wideHasDetail := strings.Contains(wide, detailMarker)
	if narrowHasDetail {
		t.Errorf("width 119 should render the narrow layout (no %q row from detail pane):\n%s", detailMarker, narrow)
	}
	if !wideHasDetail {
		t.Errorf("width 120 should render the wide layout (with detail pane %q row):\n%s", detailMarker, wide)
	}
}

func TestConversations_WideLayoutFramesColumnsWithGutter(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList(fakeConversations())

	out := m.View(140, 40)
	if !strings.Contains(out, "╮   ╭") {
		t.Fatalf("wide layout should render framed columns with a 3-cell gutter:\n%s", out)
	}
	topLine := strings.Split(stripANSI(out), "\n")[0]
	parts := strings.SplitN(topLine, strings.Repeat(" ", conversationColumnGap), 2)
	if len(parts) != 2 {
		t.Fatalf("wide layout top border missing gutter split: %q", topLine)
	}
	if got, wantMin := lipgloss.Width(parts[1]), 70; got < wantMin {
		t.Fatalf("detail pane width = %d, want at least %d cells:\n%s", got, wantMin, out)
	}
	assertNoOverflow(t, out, 140)
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
	if !strings.Contains(out, "codex") {
		t.Errorf("narrow conversations dropped the conversation rows:\n%s", out)
	}
	if strings.Contains(out, "enter resume") {
		t.Errorf("narrow conversations still shows the inline hint (T2):\n%s", out)
	}
}

func TestConversationsModel_RowColumnsAreCompactAndUnbracketed(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	row := stripANSI(m.renderConversationRowContent(conversations.Conversation{
		ID:           "row-1",
		Agent:        agent.IDCodex,
		Project:      "/p",
		LastActivity: time.Now(),
		Preview:      "compact prompt",
	}, 80))
	if strings.Contains(row, "[codex]") {
		t.Fatalf("row should render bare agent names, got %q", row)
	}
	if !strings.Contains(row, "codex  now  compact prompt") {
		t.Fatalf("row columns are not compact enough, got %q", row)
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

// TestConversationsModel_View_DetailDropsAgentUUID — the compressed
// detail pane shows the agent's accent name + project path, but the
// agent's UUID (c.ID) is debugging-only and must NOT bleed into the
// pane. A regression that re-added the ID would clutter the side
// section and make it harder to scan.
func TestConversationsModel_View_DetailDropsAgentUUID(t *testing.T) {
	prev := homeDirForDisplay
	homeDirForDisplay = func() string { return "/home/user" }
	t.Cleanup(func() { homeDirForDisplay = prev })

	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList([]conversations.Conversation{
		{
			ID:           "uuid-that-must-not-render",
			Agent:        agent.IDClaude,
			Project:      "/home/user/Projects/auth-redesign",
			LastActivity: time.Now(),
			Preview:      "rebuild login",
		},
	})

	out := m.View(140, 40) // wide enough for the detail pane
	if strings.Contains(out, "uuid-that-must-not-render") {
		t.Fatalf("compressed detail pane must NOT render c.ID:\n%s", out)
	}
}

// TestConversationsModel_View_DetailRendersTildePath — the detail
// pane's project line should collapse $HOME to "~" so the path is
// human-readable instead of cluttered by personal cwd.
func TestConversationsModel_View_DetailRendersTildePath(t *testing.T) {
	prev := homeDirForDisplay
	homeDirForDisplay = func() string { return "/home/user" }
	t.Cleanup(func() { homeDirForDisplay = prev })

	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList([]conversations.Conversation{
		{
			ID:           "row-1",
			Agent:        agent.IDClaude,
			Project:      "/home/user/Projects/auth-redesign",
			LastActivity: time.Now(),
			// Non-empty Preview so the list-row fallback ("(" + path
			// + ")") doesn't shadow the assertion below.
			Preview: "rebuild login with passkeys",
		},
	})
	out := m.View(140, 40)
	if !strings.Contains(out, "~/Projects/auth-redesign") {
		t.Fatalf("detail pane should tilde-collapse the project path:\n%s", out)
	}
	if strings.Contains(out, "/home/user/Projects/auth-redesign") {
		t.Fatalf("detail pane should NOT carry the absolute home prefix:\n%s", out)
	}
}

// TestConversationsModel_View_DetailNoKeybindHints — the side detail
// pane should NOT duplicate keybind hints (`enter resume`, `p preview`,
// `x delete`); the screen-wide HelpBar at the bottom owns them. The
// armed-delete prompt is communicated by the row chip itself.
func TestConversationsModel_View_DetailNoKeybindHints(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList(fakeConversations())
	out := m.View(140, 40)
	for _, phrase := range []string{
		"resume this conversation",
		"preview recent messages",
		"delete this conversation",
	} {
		if strings.Contains(out, phrase) {
			t.Errorf("detail pane should not duplicate the HelpBar hint %q, got:\n%s", phrase, out)
		}
	}
}

// TestConversationsModel_View_DetailStripsXMLNoise — the first-prompt
// block in the detail pane should reflect what the user actually
// typed, not the CLI-injected XML wrappers. environment_context dumps
// are dropped entirely; system-reminder content keeps its body but
// loses the brackets.
func TestConversationsModel_View_DetailStripsXMLNoise(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList([]conversations.Conversation{
		{
			ID:           "row-1",
			Agent:        agent.IDClaude,
			Project:      "/p",
			LastActivity: time.Now(),
			Preview:      "please refactor the parser",
		},
	})
	out := m.View(140, 40)
	if !strings.Contains(out, "please refactor the parser") {
		t.Fatalf("detail pane should show the cleaned preview text:\n%s", out)
	}
	for _, bad := range []string{
		"<environment_context>",
		"<system-reminder>",
		"</system-reminder>",
		"<command-message>",
	} {
		if strings.Contains(out, bad) {
			t.Errorf("detail pane leaked XML wrapper %q:\n%s", bad, out)
		}
	}
}

// TestConversationsModel_View_BannerInSidePanel — when the App pushes
// a banner into the model (via SetBanner), the wide-layout detail
// pane should surface it at the top of the side panel so the user
// sees the notification near the action that produced it. Empty
// banner is a no-op.
func TestConversationsModel_View_BannerInSidePanel(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList(fakeConversations())

	// No banner → no banner text in the output.
	baseline := m.View(140, 40)
	if strings.Contains(baseline, "killed claude") {
		t.Fatalf("precondition: no banner should produce no banner text:\n%s", baseline)
	}

	m.SetBanner("killed claude-most-recent")
	out := m.View(140, 40)
	if !strings.Contains(out, "killed claude-most-recent") {
		t.Fatalf("wide-layout View should surface SetBanner text in the side pane:\n%s", out)
	}

	// Clearing the banner removes it again on the next render.
	m.SetBanner("")
	cleared := m.View(140, 40)
	if strings.Contains(cleared, "killed claude-most-recent") {
		t.Fatalf("SetBanner(\"\") should remove the banner:\n%s", cleared)
	}
}

// TestConversationsModel_View_BannerSkippedInNarrow — narrow layout
// has no detail pane, so the banner has nowhere sensible to land. The
// App keeps the screen footer toast in that case; the model just
// ignores the banner.
func TestConversationsModel_View_BannerSkippedInNarrow(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList(fakeConversations())
	m.SetBanner("killed claude-most-recent")
	out := m.View(80, 40) // narrow
	if strings.Contains(out, "killed claude-most-recent") {
		t.Fatalf("narrow layout should not render the side-pane banner:\n%s", out)
	}
}

// TestConversationsModel_View_DetailDoesNotOverflowIntoList — the
// regression test for the "right pane bleeds into the left column"
// bug. A project path or first prompt with very long unbroken tokens
// (URL/path strings) used to soft-wrap past the detail pane width and
// reflow under the list rows. Each rendered row must stay within
// terminal width so the terminal doesn't have to soft-wrap anything.
func TestConversationsModel_View_DetailDoesNotOverflowIntoList(t *testing.T) {
	const longPath = "/Users/mchoi/repos/ccmux-redesign-tui-agents/.claude/skills/openspec-apply-change"
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList([]conversations.Conversation{
		{
			ID:           "row-1",
			Agent:        agent.IDClaude,
			Project:      "/Users/mchoi/repos/calendar-alertbar-menu-bar-upcoming-and-alerts",
			LastActivity: time.Now(),
			Preview:      "Base directory for this skill: " + longPath,
		},
	})
	for _, termW := range []int{140, 192} {
		out := m.View(termW, 40)
		assertNoOverflow(t, out, termW)
		for _, line := range strings.Split(out, "\n") {
			if lipgloss.Width(line) > termW {
				t.Fatalf("rendered line exceeds terminal width (%d > %d): %q",
					lipgloss.Width(line), termW, line)
			}
		}
	}
}

func TestConversationsModel_RenderListDoesNotOverflowColumn(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList([]conversations.Conversation{
		{
			ID:           "row-1",
			Agent:        agent.IDClaude,
			Project:      "/p",
			LastActivity: time.Now(),
			Preview:      "Base directory for this skill: /Users/mchoi/repos/calendar-alertbar-menu-bar-upcoming-and-alerts/.claude/skills/openspec-apply-change",
		},
	})
	sections := m.sections()
	const listW = 116
	out := m.renderList(sections, listW, 10)
	assertNoOverflow(t, out, listW)
}

// TestConversationsModel_View_DetailRendersFirstPrompt — surfacing
// c.Preview under a "First prompt" label gives the user a one-line
// recap of what the thread is about. Free signal — Preview is already
// loaded by the walker.
func TestConversationsModel_View_DetailRendersFirstPrompt(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList([]conversations.Conversation{
		{
			ID:           "row-1",
			Agent:        agent.IDClaude,
			Project:      "/p",
			LastActivity: time.Now(),
			Preview:      "rebuild login with passkeys",
		},
	})
	out := m.View(140, 40)
	if !strings.Contains(out, "First prompt") {
		t.Fatalf("detail pane should label the first-prompt block:\n%s", out)
	}
	if !strings.Contains(out, "rebuild login with passkeys") {
		t.Fatalf("detail pane should render c.Preview text:\n%s", out)
	}
}

// TestConversationsModel_View_DetailRendersMessageCount — once the
// lazy load lands, the messages row shows the count. While loading
// (cache miss), the row shows a muted "…" so the slot is reserved
// rather than appearing and disappearing as cursor moves.
func TestConversationsModel_View_DetailRendersMessageCount(t *testing.T) {
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList([]conversations.Conversation{
		{
			ID:           "row-1",
			Agent:        agent.IDClaude,
			Project:      "/p",
			LastActivity: time.Now(),
		},
	})
	loadingOut := m.View(140, 40)
	if !strings.Contains(loadingOut, "messages") {
		t.Fatalf("detail pane should label the messages row:\n%s", loadingOut)
	}
	if !strings.Contains(loadingOut, "…") {
		t.Fatalf("messages row should show a loading placeholder before the count lands:\n%s", loadingOut)
	}

	m.SetMessageCount("row-1", 42)
	loadedOut := m.View(140, 40)
	if !strings.Contains(loadedOut, "42") {
		t.Fatalf("after SetMessageCount, detail pane should render the count:\n%s", loadedOut)
	}
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
