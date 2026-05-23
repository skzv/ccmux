package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/conversations"
	"github.com/skzv/ccmux/internal/tmux"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// TestProjectMenu_BuildsEntries — the modal lists every running session
// and past conversation, then a trailing "Start a new session" action,
// in that order.
func TestProjectMenu_BuildsEntries(t *testing.T) {
	sessions := []tmux.Session{{Name: "c-foo"}, {Name: "c-foo-2"}}
	convs := []conversations.Conversation{{ID: "abc", Agent: agent.IDClaude}}
	m := newProjectMenu(styles.Default(), "foo", "/p/foo", sessions, convs)

	if len(m.entries) != 4 {
		t.Fatalf("entries = %d, want 4 (2 sessions + 1 conv + new)", len(m.entries))
	}
	if m.entries[0].kind != menuSession || m.entries[1].kind != menuSession {
		t.Errorf("rows 0-1 should be sessions, got %+v", m.entries[:2])
	}
	if m.entries[2].kind != menuConversation {
		t.Errorf("row 2 should be a conversation, got %v", m.entries[2].kind)
	}
	if m.entries[3].kind != menuNewSession {
		t.Errorf("last row should be the new-session action, got %v", m.entries[3].kind)
	}
	if !m.hasContent() {
		t.Error("hasContent should be true when sessions/conversations exist")
	}
}

// TestProjectMenu_EmptyHasNoContent — a project with no sessions and no
// history yields just the new-session entry, and hasContent reports
// false so App can skip the modal.
func TestProjectMenu_EmptyHasNoContent(t *testing.T) {
	m := newProjectMenu(styles.Default(), "foo", "/p/foo", nil, nil)
	if len(m.entries) != 1 || m.entries[0].kind != menuNewSession {
		t.Fatalf("empty menu should hold only the new-session entry, got %+v", m.entries)
	}
	if m.hasContent() {
		t.Error("hasContent should be false when only the new-session entry exists")
	}
}

// TestProjectMenu_HistoryOnlyDefaultsToNewSession — a project with no
// live tmux session but with past conversations should still make
// "enter project" land on the action that creates the canonical
// project session. The history remains available with arrow navigation.
func TestProjectMenu_HistoryOnlyDefaultsToNewSession(t *testing.T) {
	convs := []conversations.Conversation{
		{ID: "conv1", Agent: agent.IDClaude},
		{ID: "conv2", Agent: agent.IDClaude},
	}
	m := newProjectMenu(styles.Default(), "foo", "/p/foo", nil, convs)

	if m.cursor != len(m.entries)-1 {
		t.Fatalf("cursor = %d, want last row %d", m.cursor, len(m.entries)-1)
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if pick := drainPick(t, cmd); pick.Entry.kind != menuNewSession {
		t.Errorf("default enter picked %v, want menuNewSession", pick.Entry.kind)
	}
}

// TestProjectMenu_EnterPicksEntry — Enter on each row emits a
// projectMenuPickMsg carrying that exact entry.
func TestProjectMenu_EnterPicksEntry(t *testing.T) {
	sessions := []tmux.Session{{Name: "c-foo"}}
	convs := []conversations.Conversation{{ID: "conv1", Agent: agent.IDCodex}}
	m := newProjectMenu(styles.Default(), "foo", "/p/foo", sessions, convs)

	// Row 0 — session.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if pick := drainPick(t, cmd); pick.Entry.kind != menuSession || pick.Entry.session.Name != "c-foo" {
		t.Errorf("row 0 should pick session c-foo, got %+v", pick.Entry)
	}

	// Row 1 — conversation.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if pick := drainPick(t, cmd); pick.Entry.kind != menuConversation || pick.Entry.conv.ID != "conv1" {
		t.Errorf("row 1 should pick conversation conv1, got %+v", pick.Entry)
	}

	// Row 2 — new session.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if pick := drainPick(t, cmd); pick.Entry.kind != menuNewSession {
		t.Errorf("row 2 should pick the new-session action, got %v", pick.Entry.kind)
	}
}

// TestProjectMenu_CursorClamps — up at the top and down past the bottom
// are no-ops, never indexing out of range.
func TestProjectMenu_CursorClamps(t *testing.T) {
	m := newProjectMenu(styles.Default(), "foo", "/p/foo", []tmux.Session{{Name: "c-foo"}}, nil)
	// 2 entries (1 session + new).
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.cursor != 0 {
		t.Errorf("up at top should clamp at 0, got %d", m.cursor)
	}
	for i := 0; i < 5; i++ {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	if m.cursor != 1 {
		t.Errorf("down past bottom should clamp at 1, got %d", m.cursor)
	}
}

// TestProjectMenu_EscCancels — Esc emits projectMenuCancelMsg.
func TestProjectMenu_EscCancels(t *testing.T) {
	m := newProjectMenu(styles.Default(), "foo", "/p/foo", nil, nil)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc should emit a command")
	}
	if _, ok := cmd().(projectMenuCancelMsg); !ok {
		t.Errorf("esc should emit projectMenuCancelMsg, got %T", cmd())
	}
}

// drainPick runs a tea.Cmd and asserts it produced a projectMenuPickMsg.
func drainPick(t *testing.T, cmd tea.Cmd) projectMenuPickMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a command, got nil")
	}
	pick, ok := cmd().(projectMenuPickMsg)
	if !ok {
		t.Fatalf("expected projectMenuPickMsg, got a different message")
	}
	return pick
}
