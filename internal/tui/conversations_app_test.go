package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/conversations"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// newAppForTest builds a minimally-initialized App suitable for
// routing tests. Avoids touching the filesystem (no config load, no
// claude auth probe) so tests stay hermetic.
func newAppForTest(t *testing.T) App {
	t.Helper()
	st := styles.Default()
	km := DefaultKeymap()
	return App{
		styles:         st,
		keys:           km,
		screen:         ScreenSessions,
		dashboard:      newDashboard(st, km),
		sessionsM:      newSessions(st, km),
		conversationsM: newConversations(st, km),
		projectsM:      newProjects(st, km),
		notes:          newNotes(st, km),
		agentsM:        newAgents(st, km),
		matrix:         newMatrix(),
	}
}

// TestApp_KeyConversations_SwitchesScreen — pressing 2 (the
// Conversations keybind) from any other screen must land the user on
// ScreenConversations AND fire the refresh cmd so the list is fresh.
func TestApp_KeyConversations_SwitchesScreen(t *testing.T) {
	a := newAppForTest(t)
	a.screen = ScreenSessions

	m, cmd := a.Update(keyMsg(a.keys.Conversations.Keys()[0]))
	a2 := m.(App)
	if a2.screen != ScreenConversations {
		t.Errorf("screen = %v, want ScreenConversations", a2.screen)
	}
	// Loading flag should flip so the user sees a placeholder while
	// the refresh runs.
	if !a2.conversationsM.loading {
		t.Error("expected loading=true after entering Conversations")
	}
	if cmd == nil {
		t.Fatal("entering Conversations produced no refresh cmd")
	}
	// The cmd batches the walker refresh + the spinner tick. Resolve
	// the batch and confirm one of the produced msgs is the walker
	// result (shape only — the result depends on what's on disk).
	if !cmdEmitsMsgKind(cmd, func(m tea.Msg) bool {
		_, ok := m.(conversationsLoadedMsg)
		return ok
	}) {
		t.Errorf("refresh cmd batch did not emit a conversationsLoadedMsg")
	}
}

// cmdEmitsMsgKind drains a tea.Cmd (which may be a Batch / Sequence)
// and returns true if any of the emitted msgs satisfies `match`. Used
// by tests that have to introspect a batched cmd without caring about
// the order of its constituents.
func cmdEmitsMsgKind(cmd tea.Cmd, match func(tea.Msg) bool) bool {
	if cmd == nil {
		return false
	}
	var stack []tea.Msg
	stack = append(stack, cmd())
	for len(stack) > 0 {
		head := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if batch, ok := head.(tea.BatchMsg); ok {
			for _, c := range batch {
				if c == nil {
					continue
				}
				stack = append(stack, c())
			}
			continue
		}
		if match(head) {
			return true
		}
	}
	return false
}

// TestApp_OpenConversationsForProjectMsg_AppliesFilter — the
// Projects→Conversations drill-down: pressing `c` on a project row
// emits openConversationsForProjectMsg, which the App turns into
// (a) screen switch, (b) pre-applied filter, (c) refresh fire.
func TestApp_OpenConversationsForProjectMsg_AppliesFilter(t *testing.T) {
	a := newAppForTest(t)
	a.screen = ScreenProjects

	m, cmd := a.Update(openConversationsForProjectMsg{Project: "/Users/skz/Projects/auth-redesign"})
	a2 := m.(App)
	if a2.screen != ScreenConversations {
		t.Errorf("screen = %v, want ScreenConversations", a2.screen)
	}
	if a2.conversationsM.projectFilter != "/Users/skz/Projects/auth-redesign" {
		t.Errorf("projectFilter = %q, want /Users/skz/Projects/auth-redesign",
			a2.conversationsM.projectFilter)
	}
	if !a2.conversationsM.loading {
		t.Error("expected loading=true after drill-down")
	}
	if cmd == nil {
		t.Fatal("drill-down produced no refresh cmd")
	}
}

// TestApp_ProjectsC_EmitsDrillDownMsg — pressing `c` on a Projects
// row must fire openConversationsForProjectMsg with the selected
// project's Path. This is the producer side of the drill-down — the
// consumer side is covered by the test above.
func TestApp_ProjectsC_EmitsDrillDownMsg(t *testing.T) {
	a := newAppForTest(t)
	a.screen = ScreenProjects
	a.projectsM.SetProjects([]project.Project{
		{Name: "auth-redesign", Path: "/Users/skz/Projects/auth-redesign"},
	})

	_, cmd := a.Update(keyMsg("c"))
	if cmd == nil {
		t.Fatal("`c` on Projects produced no cmd")
	}
	msg := cmd()
	drill, ok := msg.(openConversationsForProjectMsg)
	if !ok {
		t.Fatalf("cmd produced %T, want openConversationsForProjectMsg", msg)
	}
	if drill.Project != "/Users/skz/Projects/auth-redesign" {
		t.Errorf("Project = %q, want the selected project's path", drill.Project)
	}
}

// TestApp_EnterConversations_TriggersResume — Enter on a populated
// Conversations screen must dispatch through resumeSelectedConversation,
// which fires a cmd. We don't run the cmd (it'd spawn real tmux) —
// we just assert the cmd is non-nil, proving Enter is wired through.
func TestApp_EnterConversations_TriggersResume(t *testing.T) {
	a := newAppForTest(t)
	a.screen = ScreenConversations
	a.conversationsM.SetList([]conversations.Conversation{
		{
			ID:           "abc-123",
			Agent:        agent.IDClaude,
			Project:      "/tmp/test",
			LastActivity: time.Now(),
			Preview:      "test",
		},
	})

	_, cmd := a.Update(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("Enter on Conversations produced no cmd")
	}
	// Sanity check: the cmd should be the resume path. We can't
	// safely run it (it shells out to tmux), but we can confirm the
	// model state is consistent.
	if a.conversationsM.Selected() == nil {
		t.Error("Selected returned nil despite a populated list")
	}
}

// TestApp_EnterConversations_EmptyList_NoOp — Enter when the list is
// empty must NOT crash. Resume returns nil when there's nothing
// selected; App's switch case just passes that along.
func TestApp_EnterConversations_EmptyList_NoOp(t *testing.T) {
	a := newAppForTest(t)
	a.screen = ScreenConversations
	// No SetList call — list is nil.

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Enter on empty Conversations panicked: %v", r)
		}
	}()
	_, _ = a.Update(keyMsg("enter"))
}

// TestApp_ConversationsLoadedMsg_PopulatesList — when the refresh
// command completes, the loaded msg lands in App.Update and the model
// gets the list.
func TestApp_ConversationsLoadedMsg_PopulatesList(t *testing.T) {
	a := newAppForTest(t)
	a.conversationsM.SetLoading(true)
	list := []conversations.Conversation{
		{ID: "x", Agent: agent.IDClaude, LastActivity: time.Now()},
	}
	m, _ := a.Update(conversationsLoadedMsg{List: list})
	a2 := m.(App)
	if a2.conversationsM.loading {
		t.Error("loading should clear when results arrive")
	}
	if len(a2.conversationsM.list) != 1 {
		t.Errorf("list len = %d, want 1", len(a2.conversationsM.list))
	}
}

// TestApp_ConversationsLoadedMsg_SurfacesError — error path: the
// load failed. Loading flag must clear, error string must land in the
// model for the View to surface.
func TestApp_ConversationsLoadedMsg_SurfacesError(t *testing.T) {
	a := newAppForTest(t)
	a.conversationsM.SetLoading(true)
	m, _ := a.Update(conversationsLoadedMsg{Err: errExample})
	a2 := m.(App)
	if a2.conversationsM.loadErr == "" {
		t.Error("expected loadErr to be non-empty after error msg")
	}
	if !strings.Contains(a2.conversationsM.loadErr, "boom") {
		t.Errorf("loadErr = %q, expected to contain 'boom'", a2.conversationsM.loadErr)
	}
	if a2.conversationsM.loading {
		t.Error("loading should clear even on error")
	}
}

// errExample is a sentinel error used by the load-failure test above.
// Inline so the test reads "expected error" instead of "errors.New(...)".
var errExample = exampleError{}

type exampleError struct{}

func (exampleError) Error() string { return "boom: simulated walker failure" }

// TestApp_ConversationDeletedMsg_RefreshesOnSuccess — a successful
// delete must trigger a Conversations refresh (so the row vanishes)
// and put the screen back into loading state while the walk runs.
func TestApp_ConversationDeletedMsg_RefreshesOnSuccess(t *testing.T) {
	a := newAppForTest(t)
	a.screen = ScreenConversations
	m, cmd := a.Update(conversationDeletedMsg{ID: "abc-123", Agent: "claude"})
	a2 := m.(App)
	if !a2.conversationsM.loading {
		t.Error("successful delete should put the screen into loading (refresh in flight)")
	}
	if cmd == nil {
		t.Fatal("successful delete produced no refresh cmd")
	}
}

// TestApp_KeyP_OpensPreviewOverlay — pressing `p` on the Conversations
// screen with a row selected must arm the convPreview overlay and fire
// the load command so the messages stream in. Other screens leave the
// overlay closed and consume `p` for nothing.
func TestApp_KeyP_OpensPreviewOverlay(t *testing.T) {
	a := newAppForTest(t)
	a.screen = ScreenConversations
	a.conversationsM.SetList([]conversations.Conversation{
		{
			ID:           "claude-1",
			Agent:        agent.IDClaude,
			Project:      "/p",
			LastActivity: time.Now(),
			Preview:      "hello",
		},
	})

	m, cmd := a.Update(keyMsg("p"))
	a2 := m.(App)
	if !a2.convPreview.IsOpen() {
		t.Fatal("p on Conversations should open the preview overlay")
	}
	if a2.convPreview.Conversation().ID != "claude-1" {
		t.Errorf("overlay targeted %q, want claude-1", a2.convPreview.Conversation().ID)
	}
	if cmd == nil {
		t.Fatal("p should fire a load command")
	}
	loaded, ok := cmd().(conversationPreviewLoadedMsg)
	if !ok {
		t.Fatalf("load cmd produced %T, want conversationPreviewLoadedMsg", cmd())
	}
	if loaded.ID != "claude-1" {
		t.Errorf("loaded.ID = %q, want claude-1", loaded.ID)
	}
}

// TestApp_KeyP_NoSelectionIsNoop — `p` with an empty Conversations
// list does not open the overlay (nothing to preview) and does not
// crash dereferencing a nil selection.
func TestApp_KeyP_NoSelectionIsNoop(t *testing.T) {
	a := newAppForTest(t)
	a.screen = ScreenConversations
	// Empty list — Selected() returns nil.
	m, _ := a.Update(keyMsg("p"))
	a2 := m.(App)
	if a2.convPreview.IsOpen() {
		t.Fatal("p with no selection should NOT open the overlay")
	}
}

// TestApp_KeyP_OnNonConversationsScreen_NoOp — `p` outside the
// Conversations screen must not open the overlay. The Sessions screen
// already uses other letters; reserving `p` only on Conversations
// avoids collisions.
func TestApp_KeyP_OnNonConversationsScreen_NoOp(t *testing.T) {
	a := newAppForTest(t)
	a.screen = ScreenSessions
	m, _ := a.Update(keyMsg("p"))
	a2 := m.(App)
	if a2.convPreview.IsOpen() {
		t.Fatal("p on a non-Conversations screen should NOT open the overlay")
	}
}

// TestApp_KeyP_TogglesClose — once the overlay is open, the same `p`
// keystroke closes it. Mirrors the `u` usage-overlay behaviour so the
// keystroke is a toggle, not a one-way trip.
func TestApp_KeyP_TogglesClose(t *testing.T) {
	a := newAppForTest(t)
	a.screen = ScreenConversations
	a.conversationsM.SetList([]conversations.Conversation{
		{ID: "claude-1", Agent: agent.IDClaude, Project: "/p", LastActivity: time.Now()},
	})
	m, _ := a.Update(keyMsg("p"))
	a = m.(App)
	if !a.convPreview.IsOpen() {
		t.Fatal("precondition: overlay should be open")
	}
	m, _ = a.Update(keyMsg("p"))
	a2 := m.(App)
	if a2.convPreview.IsOpen() {
		t.Fatal("second p should close the overlay")
	}
}

// TestApp_KeyEsc_ClosesPreviewOverlay — esc is the universal "back
// out" gesture; the preview overlay honours it just like the usage and
// help overlays do.
func TestApp_KeyEsc_ClosesPreviewOverlay(t *testing.T) {
	a := newAppForTest(t)
	a.screen = ScreenConversations
	a.conversationsM.SetList([]conversations.Conversation{
		{ID: "claude-1", Agent: agent.IDClaude, Project: "/p", LastActivity: time.Now()},
	})
	m, _ := a.Update(keyMsg("p"))
	a = m.(App)
	if !a.convPreview.IsOpen() {
		t.Fatal("precondition: overlay should be open")
	}
	m, _ = a.Update(keyMsg("esc"))
	a2 := m.(App)
	if a2.convPreview.IsOpen() {
		t.Fatal("esc should close the overlay")
	}
}

// TestApp_PreviewLoadedMsg_PopulatesOverlay — the load command's
// result lands in App.Update and the messages slide into the overlay
// state so the View can render them.
func TestApp_PreviewLoadedMsg_PopulatesOverlay(t *testing.T) {
	a := newAppForTest(t)
	a.convPreview.Open(conversations.Conversation{ID: "claude-1", Agent: agent.IDClaude})

	msgs := []conversations.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}
	m, _ := a.Update(conversationPreviewLoadedMsg{ID: "claude-1", Messages: msgs})
	a2 := m.(App)
	view := a2.convPreview.View(styles.Default(), 120, 40)
	if !strings.Contains(view, "hello") || !strings.Contains(view, "hi") {
		t.Fatalf("overlay view should render the loaded messages:\n%s", view)
	}
}

// TestApp_PreviewLoadedMsg_StaleIgnored — if the user closes the
// overlay (or opens it against a different conversation) between Open
// and the load returning, the late message must not bleed into the
// next preview.
func TestApp_PreviewLoadedMsg_StaleIgnored(t *testing.T) {
	a := newAppForTest(t)
	a.convPreview.Open(conversations.Conversation{ID: "claude-1", Agent: agent.IDClaude})
	a.convPreview.Close()

	m, _ := a.Update(conversationPreviewLoadedMsg{
		ID:       "claude-1",
		Messages: []conversations.Message{{Role: "user", Content: "hello"}},
	})
	a2 := m.(App)
	if a2.convPreview.IsOpen() {
		t.Fatal("late load must not re-open a closed overlay")
	}
}

// TestApp_Conversations_ToastRoutedToSidePane — on the Conversations
// screen in wide layout, an active toast must land in the detail pane
// (via conversationsM.banner) instead of the screen footer. The
// renderHelpLine path skips the toast in that case so we don't show it
// twice.
func TestApp_Conversations_ToastRoutedToSidePane(t *testing.T) {
	a := newAppForTest(t)
	a.width, a.height = 160, 40
	a.screen = ScreenConversations
	a.conversationsM.SetList([]conversations.Conversation{
		{ID: "claude-1", Agent: agent.IDClaude, Project: "/p", LastActivity: time.Now()},
	})
	a.toasts.Set(toastSuccess, "killed claude-1", 3*time.Second)

	// Render the body — App should push the banner into conversationsM.
	rendered := tea.Model(a).View()
	if !strings.Contains(rendered, "killed claude-1") {
		t.Fatalf("expected toast text to surface somewhere on Conversations:\n%s", rendered)
	}
	// And the footer should NOT also carry the toast (would be a dupe).
	helpLine := a.renderHelpLine()
	if strings.Contains(helpLine, "killed claude-1") {
		t.Fatalf("footer should suppress the toast when it lands in the side pane:\n%s", helpLine)
	}
}

// TestApp_Conversations_NarrowKeepsFooterToast — narrow layout has no
// detail pane, so the footer toast remains the only place a toast
// can land. Regression guard against accidentally hiding the toast
// when the side-pane route doesn't apply.
func TestApp_Conversations_NarrowKeepsFooterToast(t *testing.T) {
	a := newAppForTest(t)
	a.width, a.height = 80, 40 // narrow
	a.screen = ScreenConversations
	a.toasts.Set(toastSuccess, "killed claude-1", 3*time.Second)
	helpLine := a.renderHelpLine()
	if !strings.Contains(helpLine, "killed claude-1") {
		t.Fatalf("narrow layout should keep the footer toast:\n%s", helpLine)
	}
}

// TestApp_OtherScreens_ToastStaysInFooter — only the Conversations
// screen routes toasts to the side pane. Other screens (Sessions,
// Projects, etc.) keep the long-standing footer toast behaviour.
func TestApp_OtherScreens_ToastStaysInFooter(t *testing.T) {
	a := newAppForTest(t)
	a.width, a.height = 160, 40
	a.screen = ScreenSessions
	a.toasts.Set(toastSuccess, "config reloaded", 3*time.Second)
	helpLine := a.renderHelpLine()
	if !strings.Contains(helpLine, "config reloaded") {
		t.Fatalf("non-Conversations screens should keep the footer toast:\n%s", helpLine)
	}
}

// TestApp_ConversationDeletedMsg_ErrorToasts — a failed delete must
// surface a toast and must NOT trigger a refresh (nothing changed on
// disk, so the list is still valid).
func TestApp_ConversationDeletedMsg_ErrorToasts(t *testing.T) {
	a := newAppForTest(t)
	a.screen = ScreenConversations
	m, cmd := a.Update(conversationDeletedMsg{ID: "abc-123", Agent: "claude", Err: errExample})
	a2 := m.(App)
	if a2.conversationsM.loading {
		t.Error("failed delete should NOT put the screen into loading")
	}
	if cmd == nil {
		t.Fatal("failed delete produced no cmd (expected an error toast)")
	}
	msg := cmd()
	toast, ok := msg.(toastMsg)
	if !ok {
		t.Fatalf("failed delete produced %T, want toastMsg", msg)
	}
	if toast.Kind != toastError {
		t.Errorf("delete-failure toast kind = %v, want toastError", toast.Kind)
	}
}
