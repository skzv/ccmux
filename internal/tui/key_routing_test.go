package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/conversations"
	"github.com/skzv/ccmux/internal/sshsetup"
)

// TestCtrlC_QuitsFromEveryOverlay — the help promises "q / Ctrl-c quit"
// anywhere, but overlays that swallow every other key (the first-run
// tour, help, usage, previews, info panels, the matrix, the SSH wizard)
// swallowed ctrl+c too, so the only way out was to find the overlay's
// own close key first. ctrl+c must quit from every one of them.
func TestCtrlC_QuitsFromEveryOverlay(t *testing.T) {
	cases := []struct {
		name string
		mut  func(a *App)
	}{
		{"no overlay", func(a *App) {}},
		{"first-run tour", func(a *App) { a.tour.Open() }},
		{"help overlay", func(a *App) { a.helpOpen = true }},
		{"usage overlay", func(a *App) { a.usageOpen = true }},
		{"conversation preview", func(a *App) {
			a.screen = ScreenConversations
			a.convPreview.Open(conversations.Conversation{ID: "x"})
		}},
		{"project info", func(a *App) { a.screen = ScreenProjects; a.projectInfoOpen = true }},
		{"settings info", func(a *App) { a.screen = ScreenSettings; a.settingsInfoOpen = true }},
		{"network detail", func(a *App) { a.screen = ScreenNetwork; a.network.detailOpen = true }},
		{"matrix", func(a *App) { a.matrix.Open() }},
		{"ssh wizard", func(a *App) {
			a.sshWizard.Open(sshsetup.Target{User: "alice", Host: "sputnik"}, nil)
		}},
		{"notes info panel", func(a *App) { a.screen = ScreenNotes; a.notes.noteInfo.open = true }},
		{"project menu", func(a *App) { a.screen = ScreenProjects; a.projectsM.menu = &projectMenuModel{} }},
		{"projects filter", func(a *App) { a.screen = ScreenProjects; a.projectsM.enterFilter() }},
		{"sessions rename form", func(a *App) { f := newRenameForm(a.styles, "c-x"); a.sessionsM.renameForm = &f }},
		{"settings editor", func(a *App) { a.screen = ScreenSettings; a.settings.editing = true }},
		{"agents picker", func(a *App) {
			a.screen = ScreenAgents
			a.agentsM.active = agent.IDClaude
			a.agentsM.claude.picker = pickerModel
		}},
		{"quit confirmation", func(a *App) { a.confirm = newQuitConfirmation() }},
		{"attach overlay", func(a *App) { a.attach.active = true }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newAppForTest(t)
			a.width, a.height = 100, 30
			a.sshWizard = newSSHWizard(a.styles)
			tc.mut(&a)
			_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
			if !commandContainsQuit(cmd) {
				t.Errorf("ctrl+c with %s did not quit", tc.name)
			}
		})
	}
}

// TestNoteInfo_DoesNotLeakModalStateAcrossScreens — an open Notes info
// panel disabled global keys on EVERY screen, because App OR-ed every
// screen's capturesInput. Only the focused screen's modal may capture.
func TestNoteInfo_DoesNotLeakModalStateAcrossScreens(t *testing.T) {
	a := newAppForTest(t)
	a.notes.noteInfo.open = true
	a.screen = ScreenSessions

	a, _ = updateApp(t, a, keyRunes("?"))
	if !a.helpOpen {
		t.Fatal("? on Sessions did not open help while a Notes info panel was open")
	}
	a, _ = updateApp(t, a, tea.KeyMsg{Type: tea.KeyEsc})
	a, _ = updateApp(t, a, keyRunes("T"))
	if !a.tour.Active() {
		t.Error("T on Sessions did not open the tour while a Notes info panel was open")
	}
}

// TestNoteInfo_OwnsKeysWhileOpen — like the other info overlays, the
// Notes info panel owns the keyboard: a digit must not switch screens
// underneath it (that is how the panel got stranded open on Notes),
// and `i` closes it.
func TestNoteInfo_OwnsKeysWhileOpen(t *testing.T) {
	a := newAppForTest(t)
	a.screen = ScreenNotes
	a.notes.noteInfo.open = true

	a, _ = updateApp(t, a, keyRunes("1"))
	if a.screen != ScreenNotes {
		t.Fatalf("digit switched to %v while the note info panel was open", a.screen)
	}
	if !a.notes.noteInfo.open {
		t.Fatal("digit closed the note info panel")
	}
	a, _ = updateApp(t, a, keyRunes("?"))
	if a.helpOpen {
		t.Error("? opened help over the note info panel")
	}
	a, _ = updateApp(t, a, keyRunes("i"))
	if a.notes.noteInfo.open {
		t.Fatal("i did not close the note info panel")
	}
	a, _ = updateApp(t, a, keyRunes("1"))
	if a.screen != ScreenSessions {
		t.Errorf("digit after closing the panel should switch screens, got %v", a.screen)
	}
}

// TestConversations_RefreshKeyReloadsList — `r` on Conversations only
// refreshed sessions, although the help says "refresh conversation
// list". It must start a new (numbered) transcript walk with the
// loading state on, and that walk's result must be applied.
func TestConversations_RefreshKeyReloadsList(t *testing.T) {
	a := newAppForTest(t)
	a.screen = ScreenConversations
	before := a.convLoadGen

	// The returned cmd is NOT executed: it would walk the real
	// transcript tree and poll the real daemon.
	a, cmd := updateApp(t, a, keyRunes("r"))
	if cmd == nil {
		t.Fatal("r on Conversations returned no command")
	}
	if a.convLoadGen != before+1 {
		t.Fatalf("convLoadGen = %d, want %d — r did not start a conversations reload", a.convLoadGen, before+1)
	}
	if !a.conversationsM.loading {
		t.Error("r on Conversations should show the loading state")
	}
	a, _ = updateApp(t, a, conversationsLoadedMsg{
		Gen:  a.convLoadGen,
		List: []conversations.Conversation{{ID: "fresh", Agent: agent.IDClaude}},
	})
	if sel := a.conversationsM.Selected(); sel == nil || sel.ID != "fresh" {
		t.Errorf("reloaded list not applied, selected = %+v", sel)
	}
}
