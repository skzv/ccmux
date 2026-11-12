package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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

// With no sessions the empty state has no prompt line, so "/" must not
// focus the filter: every later key would go into an input nobody sees.
func TestSessions_FilterIgnoredWhenNoSessions(t *testing.T) {
	a := typeKeys(t, newSessionsApp(t), "/")
	if a.sessionsM.FilterActive() {
		t.Fatal("FilterActive() = true after / on an empty list, want false")
	}
	if a.modalCapturingText() {
		t.Error("modalCapturingText() = true on an empty list, want global keys live")
	}
	a = typeKeys(t, a, "2")
	if a.screen != ScreenProjects {
		t.Errorf("screen = %v after /2 on an empty list, want ScreenProjects", a.screen)
	}
}

// The last session ending while the filter has focus drops the filter,
// for the same reason.
func TestSessions_FilterDroppedWhenListEmpties(t *testing.T) {
	a := typeKeys(t, newSessionsFilterApp(t), "/cc")
	a = sendSessions(t, a, nil)
	if a.sessionsM.FilterActive() || a.sessionsM.filter.Value() != "" {
		t.Errorf("after the list emptied: active=%v value=%q, want cleared",
			a.sessionsM.FilterActive(), a.sessionsM.filter.Value())
	}
	// A committed filter goes too: it would come back as a hidden
	// narrowing the moment a session reappears.
	a = typeKeys(t, newSessionsFilterApp(t), "/cc")
	a = pressKey(t, a, tea.KeyEnter)
	a.attach.active = false
	a = sendSessions(t, a, nil)
	if v := a.sessionsM.filter.Value(); v != "" {
		t.Errorf("committed filter value = %q after the list emptied, want empty", v)
	}
}

// Typing a query selects its top match, wherever the cursor was — the
// help line promises "enter to attach to top match".
func TestSessions_FilterTypingSelectsTopMatch(t *testing.T) {
	a := navigate(t, newSessionsFilterApp(t), 3) // scratch-2
	a = typeKeys(t, a, "/ccmux")
	if got := selName(a.sessionsM.Selected()); got != "c-ccmux" {
		t.Errorf("Selected() after /ccmux = %q, want top match c-ccmux", got)
	}
	// Arrows still pick another match; editing the query goes back to
	// the top one.
	a = pressKey(t, a, tea.KeyDown)
	if got := selName(a.sessionsM.Selected()); got != "c-ccmux-website" {
		t.Fatalf("Selected() after Down = %q, want c-ccmux-website", got)
	}
	a = pressKey(t, a, tea.KeyBackspace)
	if got := selName(a.sessionsM.Selected()); got != "c-ccmux" {
		t.Errorf("Selected() after editing the query = %q, want c-ccmux", got)
	}
	// Moving the text cursor leaves the query, and so the selection, alone.
	a = pressKey(t, a, tea.KeyDown)
	a = pressKey(t, a, tea.KeyLeft)
	if got := selName(a.sessionsM.Selected()); got != "c-ccmux-website" {
		t.Errorf("Selected() after Left = %q, want c-ccmux-website kept", got)
	}
}

// Host and agent match by prefix. "cal" is inside "local", the host of
// every local session, and must not match them all.
func TestSessions_FilterHostMatchesByPrefix(t *testing.T) {
	a := typeKeys(t, newSessionsFilterApp(t), "/cal")
	if got := sessionNames(a.sessionsM.visibleSessions()); got != "" {
		t.Errorf("/cal visible = [%s], want none", got)
	}
	a = typeKeys(t, newSessionsFilterApp(t), "/loc")
	if got := sessionNames(a.sessionsM.visibleSessions()); got != "c-ccmux,c-ccmux-website,scratch-2" {
		t.Errorf("/loc visible = [%s], want the three local sessions", got)
	}
}

func renderedLines(s string) []string { return strings.Split(s, "\n") }

// The no-match state is six lines inside the border. listMinHeight has
// to say so, or the narrow layout keeps the detail pane and overflows.
func TestSessions_FilterNoMatchFitsHeight(t *testing.T) {
	withLang(t, "en")
	a := typeKeys(t, newSessionsFilterApp(t), "/zzz")
	min := a.sessionsM.listMinHeight()
	if got := len(renderedLines(a.sessionsM.renderList(40, min))); got != min {
		t.Errorf("no-match renderList(40, %d) is %d lines tall, want %d", min, got, min)
	}
	for _, h := range []int{10, 12, 16} {
		if got := len(renderedLines(a.sessionsM.View(40, h, true))); got > h {
			t.Errorf("no-match View(40, %d) is %d lines tall, want at most %d", h, got, h)
		}
	}
	// A query too long for the pane is cut, not wrapped.
	a = typeKeys(t, a, strings.Repeat("z", 40))
	if got := len(renderedLines(a.sessionsM.renderList(40, min))); got != min {
		t.Errorf("long no-match renderList(40, %d) is %d lines tall, want %d", min, got, min)
	}
}

// A query longer than the pane scrolls inside the prompt; the match
// count stays on the prompt's line.
func TestSessions_FilterLongQueryStaysOnOneLine(t *testing.T) {
	withLang(t, "en")
	long := strings.Repeat("c", 32)
	a := sendSessions(t, newSessionsApp(t), []daemon.SessionState{
		{Name: long + "-session", Host: "local", State: "idle", Seen: true},
	})
	a = typeKeys(t, a, "/"+long)
	out := a.sessionsM.renderList(40, 10)
	lines := renderedLines(out)
	if len(lines) != 10 {
		t.Errorf("renderList(40, 10) is %d lines tall, want 10:\n%s", len(lines), out)
	}
	found := false
	for _, l := range lines {
		if strings.Contains(l, "/ ") && strings.Contains(l, "(1/1)") {
			found = true
		}
		if w := lipgloss.Width(l); w > 40 {
			t.Errorf("line is %d columns wide, want at most 40: %q", w, l)
		}
	}
	if !found {
		t.Errorf("prompt and match count are not on one line:\n%s", out)
	}
	// The tail of the query (where the cursor is) is what stays visible.
	if !strings.Contains(out, strings.Repeat("c", 20)) {
		t.Errorf("scrolled prompt lost the query text:\n%s", out)
	}
	// The stored query is untouched by the render-time fit.
	if got := a.sessionsM.filter.Value(); got != long {
		t.Errorf("filter value = %q after render, want %q", got, long)
	}
}

// Renaming the selected session so it stops matching a committed filter
// drops the filter and keeps the cursor on the renamed row.
func TestSessions_RenameOutOfCommittedFilterKeepsRow(t *testing.T) {
	a := typeKeys(t, newSessionsFilterApp(t), "/scratch")
	a = pressKey(t, a, tea.KeyEnter)
	a.attach.active = false
	a.sessionsM.RenameSession("local", "scratch-2", "api-debug")
	if v := a.sessionsM.filter.Value(); v != "" {
		t.Errorf("filter value = %q after renaming out of it, want cleared", v)
	}
	if got := selName(a.sessionsM.Selected()); got != "api-debug" {
		t.Errorf("Selected() after rename = %q, want api-debug", got)
	}

	// A rename that still matches leaves the filter alone.
	a = typeKeys(t, newSessionsFilterApp(t), "/scratch")
	a = pressKey(t, a, tea.KeyEnter)
	a.attach.active = false
	a.sessionsM.RenameSession("local", "scratch-2", "scratch-3")
	if v := a.sessionsM.filter.Value(); v != "scratch" {
		t.Errorf("filter value = %q after a rename that still matches, want scratch", v)
	}
	if got := selName(a.sessionsM.Selected()); got != "scratch-3" {
		t.Errorf("Selected() after rename = %q, want scratch-3", got)
	}
}
