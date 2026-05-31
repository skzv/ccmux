package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/notes"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// flattenCmd materializes a tea.Cmd into the flat list of tea.Msg
// values it produces. tea.BatchMsg holds a slice of further cmds, so
// we recurse to keep flattening. Nil cmds and nil messages are
// dropped. Lives alongside the existing single-message runCmd in
// sshsetup_wizard_test.go, which can't represent batched results.
func flattenCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if msg == nil {
		return nil
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, flattenCmd(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// findMsg picks the first message in `msgs` whose concrete type is T,
// returning the zero value when none match.
func findMsg[T tea.Msg](msgs []tea.Msg) (T, bool) {
	var zero T
	for _, m := range msgs {
		if t, ok := m.(T); ok {
			return t, true
		}
	}
	return zero, false
}

// TestNotes_NarrowLayout — at phone width the Notes screen keeps its
// header (T0) but drops the inline key-hint line (T2), with no line
// overflowing the terminal.
func TestNotes_NarrowLayout(t *testing.T) {
	m := newNotes(styles.Default(), DefaultKeymap())
	m.SetProject(&project.Project{
		Name: "auth-redesign",
		Path: "/tmp/ccmux-notes-narrow-test-nonexistent",
	})
	out := m.View(50, 40)
	assertNoOverflow(t, out, 50)
	assertPresent(t, out, "auth-redesign")
	assertAbsent(t, out, "p: switch project", "tab: focus preview")
}

// notesWith builds a Notes model holding a fixed entry list, ready to
// render — bypasses the filesystem so the list-rendering tests are
// deterministic.
func notesWith(entries []notes.Entry, cursor int) notesModel {
	m := newNotes(styles.Default(), DefaultKeymap())
	m.project = &project.Project{Name: "ccmux", Path: "/tmp/ccmux"}
	m.entries = entries
	m.cursor = cursor
	return m
}

// TestNotes_ListsFilesOutsideDocs — the list surfaces markdown anywhere
// in the project, as a collapsible folder tree. Root-level files are
// always visible; foldered notes appear only when their (synthesized)
// top-level folder is expanded. Each row sits one design-system step
// inside its folder header per the sub-section indent rule.
func TestNotes_ListsFilesOutsideDocs(t *testing.T) {
	m := notesWith([]notes.Entry{
		{Rel: "README.md", Dir: "", Display: "README"},
		{Rel: "CLAUDE.md", Dir: "", Display: "CLAUDE"},
		{Rel: "docs/01_Specs/00_Vision.md", Dir: "docs/01_Specs", Display: "Vision"},
		{Rel: "openspec/specs/spec.md", Dir: "openspec/specs", Display: "spec"},
	}, 0)

	// Collapsed by default: root files show, top-level folder headers
	// show, but nested files/headers stay hidden until expanded.
	collapsed := m.renderList(70, 40, false)
	assertPresent(t, collapsed, "README", "CLAUDE", "docs/", "openspec/")
	// "Vision" and "01_Specs" are unique to the hidden nested rows.
	// (We avoid asserting "spec" absent — it's a substring of the
	// always-visible "openspec/" header.)
	assertAbsent(t, collapsed, "Vision", "01_Specs")

	// Expand every folder so the deep entries surface.
	m.expanded = map[string]bool{
		"docs": true, "docs/01_Specs": true,
		"openspec": true, "openspec/specs": true,
	}
	out := m.renderList(70, 40, false)

	// Files from the project root and from nested folders all show.
	assertPresent(t, out, "README", "CLAUDE", "Vision", "spec")
	// Nested folder headers show only the last segment — the indent
	// conveys the hierarchy, so parent prefixes aren't repeated.
	assertPresent(t, out, "docs/", "01_Specs/", "openspec/", "specs/")

	// Tree-depth indent: top-level folders sit at indent 0, root files
	// at indent 0, and each level adds one design-system step
	// (s.Spacing.SM). So a file in docs/01_Specs (tree depth 2) indents
	// 2 steps. The plain-ANSI search pins column positions independent
	// of SGR escape sequences.
	plain := stripGoldenANSI(out)
	step := styles.Default().Spacing.SM
	// Each row carries the unselected list-row prefix (2 spaces);
	// selected rows replace those with "▌ ". We pick non-selected
	// labels for these assertions.
	wantFile := func(level int, label string) string {
		return strings.Repeat(" ", level*step) + "  " + label
	}
	if !strings.Contains(plain, wantFile(0, "CLAUDE")) {
		t.Errorf("expected CLAUDE at indent for a root file (level 0):\n%s", plain)
	}
	if !strings.Contains(plain, wantFile(2, "Vision")) {
		t.Errorf("expected Vision at indent for docs/01_Specs (level 2):\n%s", plain)
	}
	if !strings.Contains(plain, wantFile(2, "spec")) {
		t.Errorf("expected spec at indent for openspec/specs (level 2):\n%s", plain)
	}

	// Headers show only the last path segment, not the full path.
	if strings.Contains(plain, "docs/01_Specs/") {
		t.Errorf("header should not include parent path `docs/`:\n%s", plain)
	}
	if strings.Contains(plain, "openspec/specs/") {
		t.Errorf("header should not include parent path `openspec/`:\n%s", plain)
	}
}

// TestNotes_FoldNavigation exercises the collapse/expand keymap on the
// file tree: folders start collapsed, → expands and drills in, ←
// collapses and jumps out, and the cursor never lands on a hidden row.
func TestNotes_FoldNavigation(t *testing.T) {
	entries := []notes.Entry{
		{Rel: "README.md", Dir: "", Display: "README"},
		{Rel: "docs/01_Specs/00_Vision.md", Dir: "docs/01_Specs", Display: "Vision"},
		{Rel: "docs/01_Specs/01_Catalog.md", Dir: "docs/01_Specs", Display: "Catalog"},
	}

	// Collapsed default: visible rows are [README, docs(folder)].
	m := notesWith(entries, 1) // cursor on the "docs" folder header
	rows := m.visibleRows()
	if len(rows) != 2 {
		t.Fatalf("collapsed visible rows = %d, want 2 (README + docs/)", len(rows))
	}
	if rows[1].kind != rowFolder || rows[1].dir != "docs" {
		t.Fatalf("row[1] = %+v, want docs folder header", rows[1])
	}

	// Down from a collapsed folder must NOT descend into hidden children.
	mDown, _ := m.Update(keyMsg("down"))
	if got := mDown.visibleRows()[mDown.cursor]; got.dir == "docs/01_Specs" {
		t.Errorf("down entered a collapsed folder's child: %+v", got)
	}

	// → on the collapsed "docs" expands it (cursor stays on docs).
	m2, _ := m.Update(keyMsg("right"))
	if !m2.expanded["docs"] {
		t.Fatal("right did not expand docs")
	}
	if r, _ := m2.selectedRow(); r.dir != "docs" || r.kind != rowFolder {
		t.Errorf("cursor moved off docs on expand: %+v", r)
	}
	// Now visible: README, docs/, docs/01_Specs/ (still collapsed).
	if n := len(m2.visibleRows()); n != 3 {
		t.Fatalf("after expanding docs, rows = %d, want 3", n)
	}

	// → again drills into the first child (the 01_Specs sub-folder).
	m3, _ := m2.Update(keyMsg("right"))
	if r, _ := m3.selectedRow(); r.dir != "docs/01_Specs" {
		t.Errorf("right did not drill into first child: %+v", r)
	}

	// → expands 01_Specs; → once more lands on a file (Vision).
	m4, _ := m3.Update(keyMsg("right"))
	m5, _ := m4.Update(keyMsg("right"))
	if e := m5.selectedEntry(); e == nil || e.Display != "Vision" {
		t.Errorf("expected to drill onto Vision file, got %v", e)
	}

	// ← from the Vision file jumps to its parent folder header.
	m6, _ := m5.Update(keyMsg("left"))
	if r, _ := m6.selectedRow(); r.kind != rowFolder || r.dir != "docs/01_Specs" {
		t.Errorf("left from file did not jump to parent header: %+v", r)
	}

	// ← collapses the now-selected 01_Specs folder.
	m7, _ := m6.Update(keyMsg("left"))
	if m7.expanded["docs/01_Specs"] {
		t.Error("left did not collapse docs/01_Specs")
	}
}

// TestNotes_CollapseKeepsCursorVisible guards the cursor-safety rule:
// collapsing a folder whose descendant is selected moves the cursor up
// to the folder header so it never points at a hidden row.
func TestNotes_CollapseKeepsCursorVisible(t *testing.T) {
	entries := []notes.Entry{
		{Rel: "docs/a.md", Dir: "docs", Display: "a"},
		{Rel: "docs/b.md", Dir: "docs", Display: "b"},
	}
	m := notesWith(entries, 0)
	m.expanded = map[string]bool{"docs": true}
	// Visible: [docs/, a, b]. Put the cursor on file "b".
	m.cursor = 2
	if e := m.selectedEntry(); e == nil || e.Display != "b" {
		t.Fatalf("setup: cursor not on file b, got %v", e)
	}

	// Collapse docs directly (simulating a collapse while a child is
	// selected). The cursor must retreat to the docs header.
	m.collapseFolder("docs")
	if m.expanded["docs"] {
		t.Fatal("docs should be collapsed")
	}
	r, ok := m.selectedRow()
	if !ok || r.kind != rowFolder || r.dir != "docs" {
		t.Errorf("cursor did not retreat to docs header: %+v (ok=%v)", r, ok)
	}
}

// TestNotes_ExpandFoldersDefault — with SetExpandFolders(true) a freshly
// loaded project opens with every folder expanded.
func TestNotes_ExpandFoldersDefault(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "docs", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs", "sub", "deep.md"), []byte("# d"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := newNotes(styles.Default(), DefaultKeymap())
	m.SetExpandFolders(true)
	cmd := m.SetProject(&project.Project{Name: "p", Path: dir})
	loaded, ok := findMsg[notesEntriesLoadedMsg](flattenCmd(cmd))
	if !ok {
		t.Fatal("no notesEntriesLoadedMsg from SetProject")
	}
	m2, _ := m.Update(loaded)
	if !m2.expanded["docs"] || !m2.expanded["docs/sub"] {
		t.Errorf("expand-all default did not seed folds: %+v", m2.expanded)
	}
	// The deep file is visible because every ancestor folder is
	// pre-expanded. Assert on the row tree rather than the rendered
	// Display label (which is derived from the H1, not the filename).
	foundDeep := false
	for _, r := range m2.visibleRows() {
		if r.kind == rowFile && m2.entries[r.entryIdx].Dir == "docs/sub" {
			foundDeep = true
		}
	}
	if !foundDeep {
		t.Errorf("deep file not visible despite expand-all default; visible rows=%d", len(m2.visibleRows()))
	}
}

// TestFolderHeader_LastSegmentOnly pins the behaviour the row
// rendering depends on: nested headers show only the last segment.
func TestFolderHeader_LastSegmentOnly(t *testing.T) {
	cases := []struct{ dir, want string }{
		{"", "(project root)"},
		{"docs", "docs/"},
		{"docs/01_Specs", "01_Specs/"},
		{"docs/02_Architecture", "02_Architecture/"},
		{"openspec/specs", "specs/"},
		{"internal/tui/styles", "styles/"},
	}
	for _, tc := range cases {
		if got := folderHeader(tc.dir); got != tc.want {
			t.Errorf("folderHeader(%q) = %q, want %q", tc.dir, got, tc.want)
		}
	}
}

// stripGoldenANSI removes SGR escape sequences so a row's column
// position can be asserted with raw spaces. Mirrors the helper in
// styles/glamour_test.go (kept local to avoid an unexported export).
var goldenANSIRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripGoldenANSI(s string) string { return goldenANSIRE.ReplaceAllString(s, "") }

// TestNotes_LongListWindowsAroundCursor — a list longer than the pane
// is windowed: the cursor row stays visible, off-screen rows are
// dropped, and a "more" hint reports how many.
func TestNotes_LongListWindowsAroundCursor(t *testing.T) {
	var entries []notes.Entry
	for i := 0; i < 60; i++ {
		entries = append(entries, notes.Entry{
			Rel:     fmt.Sprintf("docs/file%02d.md", i),
			Dir:     "docs",
			Display: fmt.Sprintf("file%02d", i),
		})
	}
	m := notesWith(entries, 0)
	m.expanded = map[string]bool{"docs": true}
	m.cursor = 56 // row 0 is the docs/ header; file55 is row 56
	out := m.renderList(70, 20, false)

	assertNoOverflow(t, out, 70)
	// The cursor row is on screen; rows far above it are not.
	assertPresent(t, out, "file55")
	assertAbsent(t, out, "file00", "file05")
	// The "more" affordance reports the hidden rows above.
	if !strings.Contains(out, "↑") || !strings.Contains(out, "more") {
		t.Errorf("expected a scroll hint for the hidden rows:\n%s", out)
	}
}

func TestWindowLines(t *testing.T) {
	rows := make([]string, 100)
	for i := range rows {
		rows[i] = fmt.Sprintf("row%02d", i)
	}

	// Everything fits → full slice, nothing hidden.
	vis, above, below := windowLines(rows[:10], 3, 20)
	if len(vis) != 10 || above != 0 || below != 0 {
		t.Errorf("fits: len=%d above=%d below=%d, want 10/0/0", len(vis), above, below)
	}

	// Cursor near the end → window clamps to the bottom, cursor visible,
	// the hidden counts still partition the whole list.
	vis, above, below = windowLines(rows, 95, 20)
	if len(vis) != 20 {
		t.Fatalf("window size = %d, want 20", len(vis))
	}
	if above+len(vis)+below != 100 {
		t.Errorf("above+visible+below = %d, want 100", above+len(vis)+below)
	}
	if below != 0 {
		t.Errorf("cursor at end should leave nothing below, got %d", below)
	}
	if !strings.Contains(strings.Join(vis, "|"), "row95") {
		t.Errorf("cursor row95 not in window: %v", vis)
	}

	// Cursor at the top → nothing hidden above.
	vis, above, _ = windowLines(rows, 0, 20)
	if above != 0 || !strings.Contains(strings.Join(vis, "|"), "row00") {
		t.Errorf("cursor at top: above=%d window=%v", above, vis)
	}
}

func TestFolderHeader(t *testing.T) {
	if got := folderHeader(""); got != "(project root)" {
		t.Errorf("folderHeader(\"\") = %q, want (project root)", got)
	}
	// Nested folders show the last segment only — the depth-based
	// indent in noteRows carries the hierarchy.
	if got := folderHeader("docs/01_Specs"); got != "01_Specs/" {
		t.Errorf("folderHeader(docs/01_Specs) = %q, want 01_Specs/", got)
	}
}

func TestScrollHintText(t *testing.T) {
	if got := scrollHintText(0, 5); !strings.Contains(got, "↓ 5") || strings.Contains(got, "↑") {
		t.Errorf("below-only hint = %q", got)
	}
	if got := scrollHintText(3, 0); !strings.Contains(got, "↑ 3") || strings.Contains(got, "↓") {
		t.Errorf("above-only hint = %q", got)
	}
	if got := scrollHintText(3, 5); !strings.Contains(got, "↑ 3") || !strings.Contains(got, "↓ 5") {
		t.Errorf("both-sides hint = %q", got)
	}
}

// TestNotes_AsyncLoad_CachesAndDiscardsStale verifies the Vault.List
// walk runs off the UI goroutine, that the per-project cache short-
// circuits repeat visits, and that a late-arriving notesEntriesLoadedMsg
// for a project the user has already navigated away from is dropped.
func TestNotes_AsyncLoad_CachesAndDiscardsStale(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# r"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs", "api.md"), []byte("# api"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := newNotes(styles.Default(), DefaultKeymap())

	cmd := m.SetProject(&project.Project{Name: "p1", Path: dir})
	if cmd == nil {
		t.Fatal("first SetProject returned no Cmd — should be async")
	}
	if !m.loading {
		t.Error("expected loading=true while the walk is in flight")
	}
	if len(m.entries) != 0 {
		t.Errorf("entries=%d before the walk completes, want 0", len(m.entries))
	}

	// SetProject now returns tea.Batch(loadCmd, spinnerTick); flatten
	// the batch and pick the notesEntriesLoadedMsg out so the test
	// doesn't care which slot it sits in.
	loaded, ok := findMsg[notesEntriesLoadedMsg](flattenCmd(cmd))
	if !ok {
		t.Fatalf("batched Cmd produced no notesEntriesLoadedMsg")
	}
	if loaded.Path != dir {
		t.Errorf("loaded.Path = %q, want %q", loaded.Path, dir)
	}
	if len(loaded.Entries) != 2 {
		t.Errorf("walk found %d entries, want 2 (README + docs/api)", len(loaded.Entries))
	}

	m2, _ := m.Update(loaded)
	if m2.loading {
		t.Error("loading should be false after notesEntriesLoadedMsg")
	}
	if len(m2.entries) != 2 {
		t.Errorf("post-load entries=%d, want 2", len(m2.entries))
	}

	// Cache hit: revisiting the same project must not spawn another walk.
	if cmd := m2.SetProject(&project.Project{Name: "p1", Path: dir}); cmd != nil {
		t.Error("cache hit should return nil Cmd")
	}

	// Stale-result safety: switch projects, then receive a late msg for
	// the old project — it must be discarded.
	other := t.TempDir()
	m2.SetProject(&project.Project{Name: "p2", Path: other})
	stale := notesEntriesLoadedMsg{Path: dir, Entries: loaded.Entries}
	m3, _ := m2.Update(stale)
	if len(m3.entries) > 0 {
		t.Errorf("stale msg for %q leaked into m.entries (now project %q): %d items",
			dir, other, len(m3.entries))
	}
}

// TestNotes_NewNote_NoProject — pressing `n` without a project
// selected must not open the form; it surfaces a toast instead.
func TestNotes_NewNote_NoProject(t *testing.T) {
	m := newNotes(styles.Default(), DefaultKeymap())

	m2, cmd := m.Update(keyMsg("n"))
	if m2.newNoteForm != nil {
		t.Error("`n` opened the form without a project — should be gated")
	}
	if cmd == nil {
		t.Fatal("expected toast cmd when `n` pressed without project")
	}
	toast, ok := cmd().(toastMsg)
	if !ok {
		t.Fatalf("expected toastMsg, got %T", cmd())
	}
	if !strings.Contains(toast.Text, "project") {
		t.Errorf("toast didn't mention project: %q", toast.Text)
	}
}

// TestNotes_NewNote_CreatesAndOpens — the full happy path: open the
// form, submit a filename + title, and confirm the file is written
// to disk with the `# {title}\n\n` body. The editor handoff is
// exercised by createAndOpenNote returning a non-nil cmd; we don't
// actually run an editor in the test (that would spawn a real
// subprocess).
func TestNotes_NewNote_CreatesAndOpens(t *testing.T) {
	dir := t.TempDir()
	m := newNotes(styles.Default(), DefaultKeymap())
	m.project = &project.Project{Name: "p", Path: dir}

	// Submit through the form's Update so we exercise the full
	// validator chain (filename trim, .md suffix add).
	m2, _ := m.Update(newNoteSubmitMsg{
		Filename: "docs/test-note.md",
		Title:    "My Test Note",
	})
	if m2.newNoteForm != nil {
		t.Error("form should be closed after submit")
	}

	wantPath := filepath.Join(dir, "docs", "test-note.md")
	body, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("note not written to disk: %v", err)
	}
	want := "# My Test Note\n\n"
	if string(body) != want {
		t.Errorf("note body = %q, want %q", body, want)
	}
}

// TestNotes_NewNote_NoTitleProducesEmptyBody — submitting with an
// empty title creates the file with no leading H1, so the user can
// start typing whatever they want as the first line.
func TestNotes_NewNote_NoTitleProducesEmptyBody(t *testing.T) {
	dir := t.TempDir()
	m := newNotes(styles.Default(), DefaultKeymap())
	m.project = &project.Project{Name: "p", Path: dir}

	m.Update(newNoteSubmitMsg{Filename: "blank.md", Title: ""})

	body, err := os.ReadFile(filepath.Join(dir, "blank.md"))
	if err != nil {
		t.Fatalf("note not written: %v", err)
	}
	if len(body) != 0 {
		t.Errorf("expected empty body when title is empty, got %q", body)
	}
}

// TestNotes_NewNote_CollisionToasts — submitting a filename that
// already exists surfaces a toast and leaves the existing file
// untouched.
func TestNotes_NewNote_CollisionToasts(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing.md")
	if err := os.WriteFile(existing, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newNotes(styles.Default(), DefaultKeymap())
	m.project = &project.Project{Name: "p", Path: dir}

	_, cmd := m.Update(newNoteSubmitMsg{Filename: "existing.md", Title: "hi"})
	if cmd == nil {
		t.Fatal("expected toast cmd for collision")
	}
	toast, ok := cmd().(toastMsg)
	if !ok {
		t.Fatalf("expected toastMsg, got %T", cmd())
	}
	if !strings.Contains(toast.Text, "exists") {
		t.Errorf("toast didn't mention file exists: %q", toast.Text)
	}
	body, _ := os.ReadFile(existing)
	if string(body) != "original" {
		t.Errorf("existing file was overwritten: %q", body)
	}
}

// TestNotes_InfoOverlay_Opens — pressing `i` opens the overlay
// populated with the selected note's metadata.
func TestNotes_InfoOverlay_Opens(t *testing.T) {
	dir := t.TempDir()
	notePath := filepath.Join(dir, "demo.md")
	body := "---\ntitle: x\n---\n\n# Demo Title\n\none two three four\n"
	if err := os.WriteFile(notePath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newNotes(styles.Default(), DefaultKeymap())
	m.project = &project.Project{Name: "p", Path: dir}
	m.entries = []notes.Entry{{Path: notePath, Rel: "demo.md", Dir: "", Display: "demo"}}

	m2, _ := m.Update(noteInfoOpenMsg{})
	if !m2.noteInfo.open {
		t.Fatal("overlay did not open")
	}
	if m2.noteInfo.h1 != "Demo Title" {
		t.Errorf("h1 = %q, want %q", m2.noteInfo.h1, "Demo Title")
	}
	if m2.noteInfo.wordCount == 0 {
		t.Error("word count should be > 0")
	}
	if !strings.Contains(m2.noteInfo.frontmatter, "title: x") {
		t.Errorf("frontmatter = %q, want it to contain `title: x`", m2.noteInfo.frontmatter)
	}

	// Esc closes it.
	m3, _ := m2.Update(keyMsg("esc"))
	if m3.noteInfo.open {
		t.Error("esc should close the overlay")
	}
}

// TestApp_NotesSearch_FindsTerm drives the full search flow through the
// App router: open search with "/", type a query whose first character
// collides with the global "r" (refresh) binding, run it, and confirm
// the query reached the backend intact and produced hits. Regression
// guard for the bug where "r" was swallowed by keys.Refresh before the
// notes search textinput ever saw it.
func TestApp_NotesSearch_FindsTerm(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	md := "# api\n\nThe old refresh token is immediately invalidated.\n"
	if err := os.WriteFile(filepath.Join(dir, "docs", "api.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}

	a := newAppForTest(t)
	a.screen = ScreenNotes
	a.notes.SetProject(&project.Project{Name: "auth-service", Path: dir})

	// Open the search box.
	m, _ := a.Update(keyMsg("/"))
	a = m.(App)
	if !a.notes.searching {
		t.Fatal("'/' did not open the notes search input")
	}

	// Type the query one rune at a time — "r" first, which is the
	// global Refresh keybinding and used to be swallowed here.
	for _, r := range "refresh token" {
		m, _ = a.Update(keyMsg(string(r)))
		a = m.(App)
	}
	if got := a.notes.searchInput.Value(); got != "refresh token" {
		t.Fatalf("search input = %q, want %q (first char swallowed?)", got, "refresh token")
	}

	// Enter runs the search; execute the returned cmd to harvest the msg.
	m, cmd := a.Update(keyMsg("enter"))
	a = m.(App)
	if cmd == nil {
		t.Fatal("enter did not produce a search command")
	}
	msg := cmd()
	res, ok := msg.(notesSearchResultMsg)
	if !ok {
		t.Fatalf("search cmd produced %T, want notesSearchResultMsg", msg)
	}
	if res.Query != "refresh token" {
		t.Fatalf("search ran for %q, want %q", res.Query, "refresh token")
	}
	if len(res.Hits) == 0 {
		t.Fatal("search for 'refresh token' found 0 hits — feature broken")
	}
}
