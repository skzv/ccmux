package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/daemon"
)

// sampleSessions is the fixture for the Sessions "/" filter tests. All
// are idle and seen so sortByAttention orders them by name: c-ccmux,
// c-ccmux-website, c-dotfiles, scratch-2.
func sampleSessions() []daemon.SessionState {
	return []daemon.SessionState{
		{Name: "c-ccmux", Host: "local", Project: "ccmux", State: "idle", Seen: true},
		{Name: "c-ccmux-website", Host: "local", Project: "ccmux-website", State: "idle", Seen: true},
		{Name: "c-dotfiles", Host: "mini", Project: "dotfiles", State: "idle", Seen: true, Agent: "codex"},
		{Name: "scratch-2", Host: "local", State: "idle", Seen: true},
	}
}

func newSessionsFilterApp(t *testing.T) App {
	t.Helper()
	return sendSessions(t, newSessionsApp(t), sampleSessions())
}

func sessionNames(ss []daemon.SessionState) string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.Name
	}
	return strings.Join(out, ",")
}

func TestSessions_FilterNarrowsList(t *testing.T) {
	a := newSessionsFilterApp(t)
	a = typeKeys(t, a, "/")
	if !a.sessionsM.FilterActive() {
		t.Fatal("FilterActive() = false after /, want true")
	}
	a = typeKeys(t, a, "website")
	vis := a.sessionsM.visibleSessions()
	if len(vis) != 1 || vis[0].Name != "c-ccmux-website" {
		t.Fatalf("visible = [%s], want [c-ccmux-website]", sessionNames(vis))
	}
	if got := selName(a.sessionsM.Selected()); got != "c-ccmux-website" {
		t.Errorf("Selected() = %q, want c-ccmux-website", got)
	}
}

// Host and agent are searchable, not just the name.
func TestSessions_FilterMatchesHostAndAgent(t *testing.T) {
	for _, q := range []string{"mini", "codex"} {
		a := typeKeys(t, newSessionsFilterApp(t), "/"+q)
		if got := sessionNames(a.sessionsM.visibleSessions()); got != "c-dotfiles" {
			t.Errorf("/%s visible = [%s], want [c-dotfiles]", q, got)
		}
	}
}

// Enter while filtering commits the filter and attaches to the
// highlighted match — not to the row at that index in the full list.
func TestSessions_FilterEnterAttachesFilteredMatch(t *testing.T) {
	a := typeKeys(t, newSessionsFilterApp(t), "/dotfiles")
	m, cmd := a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	a = m.(App)
	if a.sessionsM.FilterActive() {
		t.Error("FilterActive() still true after Enter, want committed")
	}
	if cmd == nil {
		t.Error("Enter on a filtered match returned no command, want an attach")
	}
	if got := selName(a.sessionsM.Selected()); got != "c-dotfiles" {
		t.Errorf("after Enter, Selected() = %q, want c-dotfiles", got)
	}
	if got := a.sessionsM.filter.Value(); got != "dotfiles" {
		t.Errorf("committed filter value = %q, want dotfiles kept", got)
	}
}

// While typing, global single-key bindings must type into the query:
// "2" must not switch screens, "q" must not open the quit dialog, "x"
// must not open the kill confirmation, j/k must not move the cursor.
func TestSessions_FilterCapturesGlobalKeys(t *testing.T) {
	a := typeKeys(t, newSessionsFilterApp(t), "/2qxjk")
	if a.screen != ScreenSessions {
		t.Errorf("screen = %v after typing in filter, want ScreenSessions", a.screen)
	}
	if a.confirm.open() {
		t.Error("a confirmation opened while typing in the filter")
	}
	if got := a.sessionsM.filter.Value(); got != "2qxjk" {
		t.Errorf("filter value = %q, want 2qxjk", got)
	}
	if !a.modalCapturingText() {
		t.Error("modalCapturingText() = false while filtering, want true")
	}
}

func TestSessions_FilterArrowsNavigateFiltered(t *testing.T) {
	a := typeKeys(t, newSessionsFilterApp(t), "/ccmux")
	// Matches: c-ccmux, c-ccmux-website.
	a = pressKey(t, a, tea.KeyDown)
	if got := selName(a.sessionsM.Selected()); got != "c-ccmux-website" {
		t.Errorf("after Down, Selected() = %q, want c-ccmux-website", got)
	}
	a = pressKey(t, a, tea.KeyDown) // past the end: stays
	if a.sessionsM.cursor != 1 {
		t.Errorf("Down past end moved cursor to %d, want 1", a.sessionsM.cursor)
	}
}

// Narrowing the list under the cursor clamps it into the visible rows.
func TestSessions_FilterCursorClampsOnNarrowing(t *testing.T) {
	a := navigate(t, newSessionsFilterApp(t), 3) // scratch-2
	a = typeKeys(t, a, "/ccmux")
	if n := len(a.sessionsM.visibleSessions()); a.sessionsM.cursor >= n {
		t.Errorf("cursor %d out of bounds for %d visible rows", a.sessionsM.cursor, n)
	}
}

// esc clears the filter and restores the full list, keeping the
// session the user had highlighted selected.
func TestSessions_FilterEscClearsAndKeepsSelection(t *testing.T) {
	a := typeKeys(t, newSessionsFilterApp(t), "/dotfiles")
	a = pressKey(t, a, tea.KeyEsc)
	if a.sessionsM.FilterActive() || a.sessionsM.filter.Value() != "" {
		t.Errorf("after esc: active=%v value=%q, want cleared", a.sessionsM.FilterActive(), a.sessionsM.filter.Value())
	}
	if n := len(a.sessionsM.visibleSessions()); n != len(sampleSessions()) {
		t.Errorf("visible after esc = %d, want %d", n, len(sampleSessions()))
	}
	if got := selName(a.sessionsM.Selected()); got != "c-dotfiles" {
		t.Errorf("Selected() after esc = %q, want c-dotfiles", got)
	}
}

// A committed filter (Enter) keeps narrowing; esc then clears it.
func TestSessions_CommittedFilterEscClears(t *testing.T) {
	a := typeKeys(t, newSessionsFilterApp(t), "/zzz")
	a = pressKey(t, a, tea.KeyEnter)
	if a.sessionsM.FilterActive() || len(a.sessionsM.visibleSessions()) != 0 {
		t.Fatal("precondition: committed no-match filter should hide every row")
	}
	a = pressKey(t, a, tea.KeyEsc)
	if v := a.sessionsM.filter.Value(); v != "" {
		t.Errorf("filter value = %q after esc on a committed filter, want empty", v)
	}
	if n := len(a.sessionsM.visibleSessions()); n != len(sampleSessions()) {
		t.Errorf("visible after esc = %d, want %d", n, len(sampleSessions()))
	}
}

// The 2-second poll must not jump the cursor onto a hidden row or drop
// the filter.
func TestSessions_FilterSurvivesRefresh(t *testing.T) {
	a := typeKeys(t, newSessionsFilterApp(t), "/ccmux")
	a = pressKey(t, a, tea.KeyDown) // c-ccmux-website
	ss := append(sampleSessions(), daemon.SessionState{Name: "c-aaa", Host: "local", State: "idle", Seen: true})
	a = sendSessions(t, a, ss)
	if !a.sessionsM.FilterActive() {
		t.Error("refresh dropped filter focus")
	}
	if got := sessionNames(a.sessionsM.visibleSessions()); got != "c-ccmux,c-ccmux-website" {
		t.Errorf("visible after refresh = [%s], want [c-ccmux,c-ccmux-website]", got)
	}
	if got := selName(a.sessionsM.Selected()); got != "c-ccmux-website" {
		t.Errorf("Selected() after refresh = %q, want c-ccmux-website", got)
	}
}

// A committed filter scopes the kill key to the visible match.
func TestSessions_CommittedFilterKillTargetsMatch(t *testing.T) {
	a := typeKeys(t, newSessionsFilterApp(t), "/scratch")
	a = pressKey(t, a, tea.KeyEnter)
	// Enter attached; the "Opening…" overlay swallows keys until the
	// user detaches back to ccmux. Simulate the detach.
	a.attach.active = false
	a = typeKeys(t, a, "x")
	if !a.confirm.open() {
		t.Fatal("x on a committed filter did not open the kill confirmation")
	}
	if a.confirm.target != "scratch-2" {
		t.Errorf("kill confirmation targets %q, want scratch-2", a.confirm.target)
	}
}

func TestSessions_FilterRendersPromptAndEmptyState(t *testing.T) {
	withLang(t, "en")
	a := typeKeys(t, newSessionsFilterApp(t), "/web")
	out := a.sessionsM.renderList(80, 20)
	if !strings.Contains(out, "(1/4)") {
		t.Errorf("filtered list missing match count (1/4):\n%s", out)
	}
	if strings.Contains(out, "c-dotfiles") {
		t.Errorf("filtered list still renders a hidden row:\n%s", out)
	}

	a = typeKeys(t, a, "zzz")
	if a.sessionsM.Selected() != nil {
		t.Error("Selected() should be nil when nothing matches")
	}
	out = a.sessionsM.renderList(80, 20)
	if !strings.Contains(out, "No sessions match webzzz.") || !strings.Contains(out, "Press esc to clear the filter.") {
		t.Errorf("no-match list missing empty-state hint:\n%s", out)
	}
	_ = a.sessionsM.View(80, 20, true) // narrow path must not panic
}
