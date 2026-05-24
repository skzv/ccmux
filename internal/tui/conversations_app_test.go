package tui

import (
	"strings"
	"testing"
	"time"

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
	// The cmd should fire and produce a conversationsLoadedMsg
	// regardless of whether there are real transcripts on this
	// machine — we don't assert success, just shape.
	msg := cmd()
	if _, ok := msg.(conversationsLoadedMsg); !ok {
		t.Errorf("refresh cmd produced %T, want conversationsLoadedMsg", msg)
	}
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
