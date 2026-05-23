package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// mkdirAll is a thin t.Fatal-on-error helper so the adopt tests stay
// readable.
func mkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

// updateKey routes a single key press through App.Update. Returns the
// new App + any tea.Cmd the Update produced — caller can run() it to
// drive the next message.
func updateKey(t *testing.T, a App, key string) (App, tea.Cmd) {
	t.Helper()
	var msg tea.KeyMsg
	switch key {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
	m, cmd := a.Update(msg)
	return m.(App), cmd
}

// sampleProjects returns a fixture used across the filter tests. The
// names are chosen to give substring overlaps (every "ccmux*" name
// matches "ccmux", only one matches "stress") so the assertions can
// distinguish "filter matches multiple" from "filter is exact".
func sampleProjects() []project.Project {
	return []project.Project{
		{Name: "ccmux", Host: "local", Path: "/home/u/Projects/ccmux"},
		{Name: "ccmux-website", Host: "local", Path: "/home/u/Projects/ccmux-website"},
		{Name: "ccmux-stress", Host: "local", Path: "/home/u/Projects/ccmux-stress"},
		{Name: "dotfiles", Host: "local", Path: "/home/u/Projects/dotfiles"},
		{Name: "notes-vault", Host: "remote-mac", Path: "/Users/u/Projects/notes-vault"},
	}
}

// TestMatchesProjectFilter pins down the predicate behind "/":
// case-insensitive substring match on Name. Empty query matches
// everything. Path is intentionally NOT matched (see comment on
// matchesProjectFilter for why).
func TestMatchesProjectFilter(t *testing.T) {
	p := project.Project{Name: "ccmux-Website", Path: "/home/u/Projects/ccmux-website"}
	cases := []struct {
		q    string
		want bool
		why  string
	}{
		{"", true, "empty query matches"},
		{"ccmux", true, "name substring"},
		{"WEB", true, "case-insensitive on name"},
		{"projects", false, "path is not matched"},
		{"xyz", false, "no overlap"},
	}
	for _, tc := range cases {
		got := matchesProjectFilter(p, strings.ToLower(tc.q))
		if got != tc.want {
			t.Errorf("matchesProjectFilter(%q)=%v want=%v — %s", tc.q, got, tc.want, tc.why)
		}
	}
}

// newFilterApp builds the minimal App needed to drive Projects-screen
// filter behaviour through tea.KeyMsg events. Mirrors newSessionsApp
// in sessions_test.go.
func newFilterApp(t *testing.T, ps []project.Project) App {
	t.Helper()
	st := styles.Default()
	km := DefaultKeymap()
	a := App{
		styles:    st,
		keys:      km,
		screen:    ScreenProjects,
		projectsM: newProjects(st, km),
		sessionsM: newSessions(st, km),
		matrix:    newMatrix(),
	}
	a.projects = ps
	a.projectsM.SetProjects(ps)
	return a
}

// typeKeys feeds a string through App.Update one rune at a time. Used to
// simulate the user typing into the filter textinput.
func typeKeys(t *testing.T, a App, s string) App {
	t.Helper()
	for _, r := range s {
		m, _ := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		a = m.(App)
	}
	return a
}

// pressKey sends a single tea.KeyType through App.Update.
func pressKey(t *testing.T, a App, kt tea.KeyType) App {
	t.Helper()
	m, _ := a.Update(tea.KeyMsg{Type: kt})
	return m.(App)
}

// TestProjects_FilterShrinksList — the headline behaviour: typing "/"
// then "stress" should leave exactly one visible row (ccmux-stress).
func TestProjects_FilterShrinksList(t *testing.T) {
	a := newFilterApp(t, sampleProjects())

	// "/" enters filter mode.
	a = typeKeys(t, a, "/")
	if !a.projectsM.FilterActive() {
		t.Fatal("FilterActive() = false after /, want true")
	}

	a = typeKeys(t, a, "stress")
	vis := a.projectsM.visibleProjects()
	if len(vis) != 1 {
		t.Fatalf("visibleProjects len = %d, want 1 (names=%v)", len(vis), names(vis))
	}
	if vis[0].Name != "ccmux-stress" {
		t.Errorf("filtered project = %q, want ccmux-stress", vis[0].Name)
	}
}

// TestProjects_FilterEnterAttachesFilteredMatch — pressing Enter while
// filtering should attach to the project visible under the cursor, not
// the project at the corresponding index in the unfiltered slice. This
// is the same class of bug as the wrong-session-join one.
func TestProjects_FilterEnterAttachesFilteredMatch(t *testing.T) {
	a := newFilterApp(t, sampleProjects())
	a = typeKeys(t, a, "/")
	a = typeKeys(t, a, "stress")

	// Selected() must report the filtered match, not sample[0].
	sel := a.projectsM.Selected()
	if sel == nil {
		t.Fatal("Selected() = nil after filter narrows to one")
	}
	if sel.Name != "ccmux-stress" {
		t.Errorf("Selected() = %q after filtering to 'stress', want ccmux-stress", sel.Name)
	}

	// Press Enter. App should commit the filter (FilterActive→false) and
	// the visible row should still be ccmux-stress. We don't run the
	// returned tea.Cmd because attachOrCreateLocal does real tmux work;
	// the contract here is "Selected() resolves to the filtered match".
	a = pressKey(t, a, tea.KeyEnter)
	if a.projectsM.FilterActive() {
		t.Error("FilterActive() still true after Enter, want false (commitFilter not called)")
	}
	if sel := a.projectsM.Selected(); sel == nil || sel.Name != "ccmux-stress" {
		t.Errorf("after Enter, Selected() = %v, want ccmux-stress", sel)
	}
}

// TestProjects_FilterEscClears — esc while filtering should drop focus,
// clear the text, and restore the full list.
func TestProjects_FilterEscClears(t *testing.T) {
	a := newFilterApp(t, sampleProjects())
	a = typeKeys(t, a, "/stress")
	if got := len(a.projectsM.visibleProjects()); got != 1 {
		t.Fatalf("pre-esc visible = %d, want 1", got)
	}

	a = pressKey(t, a, tea.KeyEsc)
	if a.projectsM.FilterActive() {
		t.Error("FilterActive() true after esc, want false")
	}
	if v := a.projectsM.filter.Value(); v != "" {
		t.Errorf("filter value = %q after esc, want empty", v)
	}
	if got := len(a.projectsM.visibleProjects()); got != len(sampleProjects()) {
		t.Errorf("visible after esc = %d, want %d", got, len(sampleProjects()))
	}
}

// TestProjects_FilterBackspaceShrinksQuery — backspace should remove
// the most-recently-typed character and re-widen the visible list.
func TestProjects_FilterBackspaceShrinksQuery(t *testing.T) {
	a := newFilterApp(t, sampleProjects())
	a = typeKeys(t, a, "/stre")
	if got := len(a.projectsM.visibleProjects()); got != 1 {
		t.Fatalf("after /stre visible = %d, want 1", got)
	}
	a = pressKey(t, a, tea.KeyBackspace)
	a = pressKey(t, a, tea.KeyBackspace)
	a = pressKey(t, a, tea.KeyBackspace)
	// Now the query is "s" — every project whose name contains "s"
	// (ccmux-website, ccmux-stress, dotfiles, notes-vault). 4 of 5.
	want := 4
	if got := len(a.projectsM.visibleProjects()); got != want {
		t.Errorf("after backspaces, visible = %d, want %d (names=%v)",
			got, want, names(a.projectsM.visibleProjects()))
	}
}

// TestProjects_FilterCursorClampsOnNarrowing — if the cursor was at
// index N in the full list and the filter removes all rows ≥N, the
// cursor must clamp to the last visible row rather than going OOB.
func TestProjects_FilterCursorClampsOnNarrowing(t *testing.T) {
	a := newFilterApp(t, sampleProjects())
	// Move cursor to index 4 (notes-vault).
	for i := 0; i < 4; i++ {
		a = pressKey(t, a, tea.KeyDown)
	}
	if a.projectsM.cursor != 4 {
		t.Fatalf("pre-filter cursor = %d, want 4", a.projectsM.cursor)
	}

	// Filter to "ccmux" — 3 visible rows (indices 0-2). Cursor 4 must
	// not be allowed to remain.
	a = typeKeys(t, a, "/ccmux")
	if a.projectsM.cursor >= len(a.projectsM.visibleProjects()) {
		t.Errorf("cursor %d out of bounds after filter shrank list to %d",
			a.projectsM.cursor, len(a.projectsM.visibleProjects()))
	}
}

// TestProjects_FilterSuppressesScreenSwitch — while typing into the
// filter, "2" should be part of the query, not switch the user to the
// Sessions tab. Same root cause as the form-screen-keys regression in
// sessions_test.go.
func TestProjects_FilterSuppressesScreenSwitch(t *testing.T) {
	ps := append(sampleProjects(), project.Project{Name: "proj-2", Path: "/p/2"})
	a := newFilterApp(t, ps)
	a = typeKeys(t, a, "/2")

	if a.screen != ScreenProjects {
		t.Errorf("screen = %v after typing 2 in filter, want ScreenProjects", a.screen)
	}
	if v := a.projectsM.filter.Value(); v != "2" {
		t.Errorf("filter value = %q after typing 2, want \"2\"", v)
	}
}

// TestProjects_FilterVimNavKeysFeedQuery — j/k are vim navigation
// outside filter mode but must become regular characters inside it.
// Otherwise the user can't filter to "javascript" or "kubectl" — j
// would jump the cursor instead of typing.
func TestProjects_FilterVimNavKeysFeedQuery(t *testing.T) {
	a := newFilterApp(t, sampleProjects())
	a = typeKeys(t, a, "/jk")
	if v := a.projectsM.filter.Value(); v != "jk" {
		t.Errorf("filter value = %q after typing j+k in filter mode, want \"jk\"", v)
	}
	// Cursor should not have moved despite "j"/"k" being navigation
	// bindings outside filter mode.
	if a.projectsM.cursor != 0 {
		t.Errorf("cursor = %d after j/k in filter, want 0 (no movement)", a.projectsM.cursor)
	}
}

// TestProjects_FilterUpDownNavigatesFiltered — arrows in filter mode
// move the cursor over filtered rows, never landing on a hidden one.
func TestProjects_FilterUpDownNavigatesFiltered(t *testing.T) {
	a := newFilterApp(t, sampleProjects())
	a = typeKeys(t, a, "/ccmux")
	// 3 matches: ccmux, ccmux-website, ccmux-stress.
	a = pressKey(t, a, tea.KeyDown)
	if a.projectsM.cursor != 1 {
		t.Fatalf("after Down, cursor = %d, want 1", a.projectsM.cursor)
	}
	if sel := a.projectsM.Selected(); sel == nil || sel.Name != "ccmux-website" {
		t.Errorf("Selected after Down = %v, want ccmux-website", sel)
	}
	a = pressKey(t, a, tea.KeyDown)
	if sel := a.projectsM.Selected(); sel == nil || sel.Name != "ccmux-stress" {
		t.Errorf("Selected after 2x Down = %v, want ccmux-stress", sel)
	}
	// Down past end stays clamped.
	a = pressKey(t, a, tea.KeyDown)
	if a.projectsM.cursor != 2 {
		t.Errorf("Down past end moved cursor to %d, want 2", a.projectsM.cursor)
	}
}

// TestProjects_FilterNoMatchesRendersEmptyState — when the filter
// excludes everything, Selected() is nil and the render path doesn't
// panic on an empty visible slice.
func TestProjects_FilterNoMatchesRendersEmptyState(t *testing.T) {
	a := newFilterApp(t, sampleProjects())
	a = typeKeys(t, a, "/zzz-nope")
	if got := len(a.projectsM.visibleProjects()); got != 0 {
		t.Fatalf("visible = %d, want 0", got)
	}
	if a.projectsM.Selected() != nil {
		t.Error("Selected() should be nil when filter matches nothing")
	}
	// View must not panic.
	_ = a.projectsM.View(120, 30)
}

// names is a small helper for table-driven assertions: turn a slice
// of projects into a comma-separated name list for error messages.
func names(ps []project.Project) string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Name
	}
	return strings.Join(out, ",")
}

// TestProjects_NarrowLayout — at phone width the project list keeps
// the project names (T0) but drops the inline key-hint (T2), with no
// line overflowing the terminal.
func TestProjects_NarrowLayout(t *testing.T) {
	m := newProjects(styles.Default(), DefaultKeymap())
	m.SetProjects(sampleProjects())
	out := m.View(50, 40)
	assertNoOverflow(t, out, 50)
	assertPresent(t, out, "ccmux-website", "dotfiles")
	assertAbsent(t, out, "/: filter", "upgrade cwd")
}

// TestProjects_CursorVisibleWhenScrolledPastWindow — regression for the
// "scroll down far enough and the cursor disappears" bug. With many
// projects and a small pane height, the cursor at the bottom of the
// list must still appear in the rendered View (it used to be clipped
// because all rows were appended unwindowed and lipgloss let the box
// overflow the terminal).
func TestProjects_CursorVisibleWhenScrolledPastWindow(t *testing.T) {
	m := newProjects(styles.Default(), DefaultKeymap())
	many := make([]project.Project, 30)
	for i := range many {
		many[i] = project.Project{
			Name: fmt.Sprintf("project-%02d", i),
			Host: "local",
			Path: fmt.Sprintf("/home/u/Projects/p%02d", i),
		}
	}
	m.SetProjects(many)
	m.cursor = 28 // near the bottom

	// Small pane — list budget is tighter than the full project count.
	out := m.View(120, 15)
	if !strings.Contains(out, "project-28") {
		t.Errorf("cursor row project-28 missing from rendered view (clipped at bottom?):\n%s", out)
	}
	// And the top of the unfiltered list should have been dropped by the
	// windowing — otherwise the window isn't actually doing its job.
	if strings.Contains(out, "project-00") {
		t.Errorf("project-00 still visible when cursor is at row 28 — windowing didn't shift")
	}
}

// TestAdoptModal_OpenedByCapitalA — pressing `A` on the Projects
// screen scans the projects root for orphans and installs the modal.
// We assert via projectsM.adopt (the model field App routes keys to).
func TestAdoptModal_OpenedByCapitalA(t *testing.T) {
	root := t.TempDir()
	mkdirAll(t, root+"/scratch")
	mkdirAll(t, root+"/notes")

	a := newFilterApp(t, nil)
	a.cfg.Projects.Root = root
	a.projectsM.SetProjectsRoot(root)

	// Press A.
	a, cmd := updateKey(t, a, "A")
	if cmd == nil {
		t.Fatal("A on Projects produced no cmd (expected scanOrphansCmd)")
	}
	// Run the cmd, feed its message back through App.Update.
	msg := cmd()
	if _, ok := msg.(adoptProjectOpenedMsg); !ok {
		t.Fatalf("scan cmd returned %T, want adoptProjectOpenedMsg", msg)
	}
	m, _ := a.Update(msg)
	a = m.(App)

	if a.projectsM.adopt == nil {
		t.Fatal("adopt modal not installed after adoptProjectOpenedMsg")
	}
	if len(a.projectsM.adopt.orphans) != 2 {
		t.Errorf("modal orphan count = %d, want 2 (orphans=%v)",
			len(a.projectsM.adopt.orphans), a.projectsM.adopt.orphans)
	}
}

// TestAdoptModal_EnterEmitsPickThenAdopts — drives the full keyboard
// path: A → Enter → adoption applies, modal closes, marker exists.
func TestAdoptModal_EnterEmitsPickThenAdopts(t *testing.T) {
	root := t.TempDir()
	mkdirAll(t, root+"/scratch")

	a := newFilterApp(t, nil)
	a.cfg.Projects.Root = root
	a.projectsM.SetProjectsRoot(root)

	// Open the modal (A, then deliver the scan result).
	a, cmd := updateKey(t, a, "A")
	m, _ := a.Update(cmd())
	a = m.(App)

	// Enter inside the modal: modal emits adoptProjectPickMsg, which
	// projectsModel.Update forwards up so App sees it and runs Adopt.
	a, cmd = updateKey(t, a, "enter")
	if cmd == nil {
		t.Fatal("Enter in adopt modal produced no cmd")
	}
	pick := cmd()
	if _, ok := pick.(adoptProjectPickMsg); !ok {
		t.Fatalf("Enter cmd returned %T, want adoptProjectPickMsg", pick)
	}
	m, cmd = a.Update(pick)
	a = m.(App)
	// App should have cleared the modal.
	if a.projectsM.adopt != nil {
		t.Error("adopt modal still open after pick — App should have nil'd it")
	}
	// And kicked off adoptProjectCmd.
	if cmd == nil {
		t.Fatal("App produced no cmd in response to adoptProjectPickMsg")
	}
	adopted := cmd()
	apm, ok := adopted.(projectAdoptedMsg)
	if !ok {
		t.Fatalf("adopt cmd returned %T, want projectAdoptedMsg", adopted)
	}
	if apm.Err != nil {
		t.Fatalf("adoption failed: %v", apm.Err)
	}
	if _, err := os.Stat(filepath.Join(root, "scratch", ".ccmux")); err != nil {
		t.Errorf(".ccmux marker not written: %v", err)
	}
}

// TestAdoptModal_EscCloses — Esc cancels the modal without adopting.
func TestAdoptModal_EscCloses(t *testing.T) {
	root := t.TempDir()
	mkdirAll(t, root+"/scratch")

	a := newFilterApp(t, nil)
	a.cfg.Projects.Root = root
	a.projectsM.SetProjectsRoot(root)
	a, cmd := updateKey(t, a, "A")
	m, _ := a.Update(cmd())
	a = m.(App)

	a, cmd = updateKey(t, a, "esc")
	if cmd == nil {
		t.Fatal("Esc in modal produced no cmd")
	}
	if _, ok := cmd().(adoptProjectCancelMsg); !ok {
		t.Fatalf("Esc cmd returned %T, want adoptProjectCancelMsg", cmd())
	}
	// Deliver the cancel; App should clear the modal.
	m, _ = a.Update(adoptProjectCancelMsg{})
	a = m.(App)
	if a.projectsM.adopt != nil {
		t.Error("adopt modal still open after Esc cancel")
	}
	if _, err := os.Stat(filepath.Join(root, "scratch", ".ccmux")); err == nil {
		t.Error(".ccmux marker created by Esc — should be cancel-only")
	}
}
