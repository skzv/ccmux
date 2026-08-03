package tui

import (
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// newFanoutTestApp builds an App with every screen model constructed,
// since the spinner fan-out in App.Update touches all of them.
func newFanoutTestApp(t *testing.T) App {
	t.Helper()
	// Isolate ~/.claude so newAgents → claudeModel.reload() reads a
	// sandbox instead of the developer's real config.
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	st := styles.Default()
	km := DefaultKeymap()
	cfg := config.Defaults()
	return App{
		cfg:            cfg,
		styles:         st,
		keys:           km,
		screen:         ScreenConversations,
		width:          120,
		height:         40,
		dashboard:      newDashboard(st, km),
		sessionsM:      newSessions(st, km),
		projectsM:      newProjects(st, km),
		conversationsM: newConversations(st, km),
		notes:          newNotes(st, km),
		agentsM:        newAgents(st, km),
		settings:       newSettings(st, km, cfg, "test"),
		network:        newNetwork(st, km),
		matrix:         newMatrix(),
	}
}

// TestApp_SpinnerTickFanoutReachesConversations is the regression test
// for the frozen loading spinner: the top-level spinner.TickMsg case in
// App.Update returns early, so any model left out of the fan-out never
// sees its own ticks and its spinner sticks on frame 0. Drive one tick
// from the Conversations spinner through the REAL App.Update and assert
// a follow-up command comes back — that's the tick chain staying alive.
func TestApp_SpinnerTickFanoutReachesConversations(t *testing.T) {
	a := newFanoutTestApp(t)
	a.conversationsM.SetLoading(true)

	// Harvest the model's own first TickMsg (carries its spinner ID).
	tick := a.conversationsM.SpinnerTickCmd()()
	if _, ok := tick.(spinner.TickMsg); !ok {
		t.Fatalf("SpinnerTickCmd produced %T, want spinner.TickMsg", tick)
	}

	_, cmd := updateApp(t, a, tick)
	if cmd == nil {
		t.Fatal("spinner tick chain died: App.Update returned no follow-up cmd for a Conversations spinner tick")
	}
	// The follow-up must itself contain the next tick, not just any
	// command. tea.Batch collapses to the single non-nil cmd here.
	if !containsSpinnerTick(cmd()) {
		t.Errorf("follow-up cmd did not carry the next spinner.TickMsg")
	}
}

// containsSpinnerTick walks a message (possibly a tea.BatchMsg) looking
// for a spinner.TickMsg.
func containsSpinnerTick(msg tea.Msg) bool {
	switch m := msg.(type) {
	case spinner.TickMsg:
		return true
	case tea.BatchMsg:
		for _, c := range m {
			if c == nil {
				continue
			}
			if containsSpinnerTick(c()) {
				return true
			}
		}
	}
	return false
}
