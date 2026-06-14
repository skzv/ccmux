package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// --- e2e helpers: drive the REAL App, not the sub-model in isolation ---
//
// The preview pane shipped broken because the original tests exercised
// sessionsModel.Update / sessionsModel.View directly, while the real
// wide Home screen routes keys through App.Update and renders through
// App.homeView — neither of which the unit tests touched. These helpers
// build a real App so every preview test goes through the actual
// keypress→route→render path a user hits.

// buildPreviewApp returns an App parked on the Sessions screen with two
// local sessions and a stubbed pane-capture. width/height are set so
// homeView takes the wide (monitor) layout — the exact path that
// bypassed the preview before the fix.
func buildPreviewApp(t *testing.T, captureContent string, captureErr error) App {
	t.Helper()
	st := styles.Default()
	km := DefaultKeymap()
	cfg := config.Defaults()

	a := App{
		cfg:          cfg,
		styles:       st,
		keys:         km,
		version:      "v0.0.0-test",
		screen:       ScreenSessions,
		daemonOnline: true,
	}
	a.width, a.height = 160, 40 // wide → monitor layout in homeView
	a.dashboard = newDashboard(st, km)
	a.dashboard.SetConfig(cfg)
	a.sessionsM = newSessions(st, km)

	sessions := []daemon.SessionState{
		{Name: "c-foo", Host: "local", State: "active", Project: "foo"},
		{Name: "c-bar", Host: "local", State: "idle", Project: "bar"},
	}
	a.sessions = sessions
	a.sessionsM.SetSessions(sessions)
	a.dashboard.SetSessions(sessions)

	t.Cleanup(stubCapture(captureContent, captureErr))
	return a
}

// stubCapture overrides the package-level capturePreviewCmd for one
// test. Returns the cleanup the test defers (via t.Cleanup).
func stubCapture(content string, err error) func() {
	orig := capturePreviewCmd
	capturePreviewCmd = func(s daemon.SessionState) tea.Cmd {
		return func() tea.Msg {
			return previewLoadedMsg{Session: s.Name, Content: content, Err: err}
		}
	}
	return func() { capturePreviewCmd = orig }
}

// updateApp runs one message through the REAL App.Update and returns the
// concrete App back (Update returns tea.Model). Mirrors what Bubble Tea
// does at runtime.
func updateApp(t *testing.T, a App, msg tea.Msg) (App, tea.Cmd) {
	t.Helper()
	m, cmd := a.Update(msg)
	got, ok := m.(App)
	if !ok {
		t.Fatalf("App.Update returned %T, want App", m)
	}
	return got, cmd
}

// selName returns the name of the App's currently-selected session.
// Tests target this rather than hardcoding a name, because the Sessions
// list re-sorts by attention priority (a done-unseen row outranks a
// working one) so "the first session I added" is NOT necessarily the
// selected one.
func selectedSession(t *testing.T, a App) string {
	t.Helper()
	sel := a.sessionsM.Selected()
	if sel == nil {
		t.Fatal("no session selected")
	}
	return sel.Name
}

// feedCapture delivers a previewLoadedMsg for the currently-selected
// session through the REAL App.Update, so it isn't dropped as stale.
func feedCapture(t *testing.T, a App, content string, err error) App {
	t.Helper()
	a, _ = updateApp(t, a, previewLoadedMsg{Session: selectedSession(t, a), Content: content, Err: err})
	return a
}

// drainCmd executes a tea.Cmd (recursing into tea.BatchMsg) and returns
// every concrete tea.Msg it produced. Lets a test assert what a keypress
// actually scheduled (the capture, the tick) end to end.
func drainCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	var out []tea.Msg
	msg := cmd()
	switch m := msg.(type) {
	case tea.BatchMsg:
		for _, c := range m {
			out = append(out, drainCmd(c)...)
		}
	case nil:
		// no-op
	default:
		out = append(out, msg)
	}
	return out
}

// ===================================================================
// END-TO-END: the tests that would have caught the shipped bug.
// ===================================================================

// TestE2E_PreviewToggleRendersThroughHomeView is THE regression test.
// It drives the real App: press `p` on the Sessions screen, then render
// via App.homeView (the wide-monitor path that bypassed the preview),
// and assert the captured content actually appears. Before the fix this
// failed because homeView never consulted showPreview.
func TestE2E_PreviewToggleRendersThroughHomeView(t *testing.T) {
	a := buildPreviewApp(t, "AGENT IS THINKING…\nstep 3 of 7", nil)

	// Baseline: preview off → homeView shows the stat tiles (Devices
	// panel), NOT a preview.
	base := a.homeView(a.width, a.height)
	if strings.Contains(base, "AGENT IS THINKING") {
		t.Fatal("preview content present before `p` was pressed")
	}

	// Press `p` through the REAL routing.
	a, cmd := updateApp(t, a, keyMsg("p"))
	if !a.sessionsM.showPreview {
		t.Fatal("`p` through App.Update did not toggle showPreview — key never reached sessionsM")
	}
	// The keypress must schedule the capture + tick.
	msgs := drainCmd(cmd)
	var sawLoaded, sawTick bool
	for _, m := range msgs {
		switch m.(type) {
		case previewLoadedMsg:
			sawLoaded = true
		case previewTickMsg:
			sawTick = true
		}
	}
	if !sawLoaded {
		t.Error("pressing `p` did not schedule a pane capture (previewLoadedMsg)")
	}
	if !sawTick {
		t.Error("pressing `p` did not schedule the refresh tick (previewTickMsg)")
	}

	// Deliver the capture result through the REAL App.Update routing —
	// this is the path (top-level case) that drops the message if it
	// isn't wired. If it's dropped, the content never stores.
	sel := selectedSession(t, a)
	a = feedCapture(t, a, "AGENT IS THINKING…\nstep 3 of 7", nil)
	if a.sessionsM.preview == "" {
		t.Fatal("previewLoadedMsg did not reach sessionsM through App.Update — message routing broken")
	}

	// Now the wide Home screen MUST show the captured content.
	out := a.homeView(a.width, a.height)
	if !strings.Contains(out, "AGENT IS THINKING") {
		t.Errorf("homeView did not render preview content after `p` + capture.\n--- got ---\n%s", out)
	}
	if !strings.Contains(out, "step 3 of 7") {
		t.Error("homeView dropped part of the captured body")
	}
	// The selected session's name heads the preview pane.
	if !strings.Contains(out, sel) {
		t.Errorf("preview pane header (session name %q) missing", sel)
	}
}

// TestE2E_PreviewToggleOffRestoresTiles — pressing `p` twice returns the
// Home screen to the detail + stat-tile layout. Confirms the toggle is
// truly two-way through the real render path.
func TestE2E_PreviewToggleOffRestoresTiles(t *testing.T) {
	a := buildPreviewApp(t, "live pane content", nil)

	a, _ = updateApp(t, a, keyMsg("p"))
	a = feedCapture(t, a, "live pane content", nil)
	withPreview := a.homeView(a.width, a.height)
	if !strings.Contains(withPreview, "live pane content") {
		t.Fatal("preview not showing after first `p`")
	}

	a, _ = updateApp(t, a, keyMsg("p"))
	if a.sessionsM.showPreview {
		t.Fatal("second `p` did not turn preview off")
	}
	off := a.homeView(a.width, a.height)
	if strings.Contains(off, "live pane content") {
		t.Error("preview content still rendered after toggling off")
	}
}

// TestE2E_PreviewFollowsCursor — moving the cursor with the arrow keys
// while preview is on must re-capture for the newly-selected session and
// render ITS content. Drives Down through App.Update and feeds the new
// capture back.
func TestE2E_PreviewFollowsCursor(t *testing.T) {
	a := buildPreviewApp(t, "", nil)

	a, _ = updateApp(t, a, keyMsg("p"))
	first := selectedSession(t, a)
	a = feedCapture(t, a, "FIRST pane", nil)
	if !strings.Contains(a.homeView(a.width, a.height), "FIRST pane") {
		t.Fatal("first preview not shown")
	}

	// Arrow down → selects the other session → must fire a fresh capture.
	a, cmd := updateApp(t, a, keyMsg("down"))
	second := selectedSession(t, a)
	if second == first {
		t.Fatalf("cursor did not move (still %q)", first)
	}
	msgs := drainCmd(cmd)
	sawCapture := false
	for _, m := range msgs {
		if lm, ok := m.(previewLoadedMsg); ok && lm.Session == second {
			sawCapture = true
		}
	}
	if !sawCapture {
		t.Errorf("moving the cursor did not re-capture for the newly-selected session %q", second)
	}

	// Feed the new selection's content and confirm the pane switched.
	a = feedCapture(t, a, "SECOND pane", nil)
	out := a.homeView(a.width, a.height)
	if !strings.Contains(out, "SECOND pane") {
		t.Error("preview did not update to the newly-selected session's content")
	}
	if strings.Contains(out, "FIRST pane") {
		t.Error("stale previous-session content still showing after cursor move")
	}
}

// TestE2E_PreviewTickSurvivesScreenSwitch — the once-a-second refresh
// must keep ticking even after the user navigates away from Sessions
// (so re-entering shows live content). This is the case the old
// pre-route handled incorrectly (the screen switch overwrote the tick
// cmd). Now the top-level case returns the tick cmd directly.
func TestE2E_PreviewTickSurvivesScreenSwitch(t *testing.T) {
	a := buildPreviewApp(t, "x", nil)
	a, _ = updateApp(t, a, keyMsg("p")) // preview on
	// Navigate to Projects.
	a.screen = ScreenProjects

	// A tick arrives while on Projects. It must still reschedule.
	_, cmd := updateApp(t, a, previewTickMsg{})
	msgs := drainCmd(cmd)
	sawNextTick := false
	for _, m := range msgs {
		if _, ok := m.(previewTickMsg); ok {
			sawNextTick = true
		}
	}
	if !sawNextTick {
		t.Error("preview tick chain died after navigating away from Sessions — re-entry would show a frozen frame")
	}
}

// TestE2E_PreviewTickStopsWhenToggledOff — once preview is off, an
// in-flight tick must NOT schedule another. Otherwise the chain runs
// forever.
func TestE2E_PreviewTickStopsWhenToggledOff(t *testing.T) {
	a := buildPreviewApp(t, "x", nil)
	a, _ = updateApp(t, a, keyMsg("p")) // on
	a, _ = updateApp(t, a, keyMsg("p")) // off
	_, cmd := updateApp(t, a, previewTickMsg{})
	msgs := drainCmd(cmd)
	for _, m := range msgs {
		if _, ok := m.(previewTickMsg); ok {
			t.Error("tick rescheduled itself after preview was toggled off — infinite chain")
		}
	}
}

// TestE2E_PreviewNarrowNeverSplits — on a phone-width terminal the Home
// screen must never show the side-by-side preview (it's unreadable).
// Even with showPreview on, the narrow homeView path stays stacked.
func TestE2E_PreviewNarrowNeverSplits(t *testing.T) {
	a := buildPreviewApp(t, "secret preview body", nil)
	a.width = 60 // phone
	a, _ = updateApp(t, a, keyMsg("p"))
	a = feedCapture(t, a, "secret preview body", nil)
	out := a.homeView(a.width, a.height)
	if strings.Contains(out, "secret preview body") {
		t.Error("narrow Home screen showed the side preview — should stay stacked on a phone")
	}
}

// TestE2E_PreviewErrorSurfaced — a capture error must show a readable
// message in the pane, not an empty box.
func TestE2E_PreviewErrorSurfaced(t *testing.T) {
	a := buildPreviewApp(t, "", nil)
	a, _ = updateApp(t, a, keyMsg("p"))
	a = feedCapture(t, a, "", errFake{msg: "tmux: no server running"})
	out := a.homeView(a.width, a.height)
	if !strings.Contains(out, "preview unavailable") {
		t.Errorf("error state did not surface; got:\n%s", out)
	}
	if !strings.Contains(out, "no server running") {
		t.Error("underlying error text not shown to the user")
	}
}

// ===================================================================
// SUB-MODEL UNIT TESTS — the logic of sessionsModel in isolation.
// Kept because they pin the model's internal contract cheaply, but they
// are NOT a substitute for the e2e tests above (that was the original
// mistake). Every one of these has an e2e sibling that proves the wired
// path.
// ===================================================================

func newPreviewSubModel(t *testing.T, content string, captureErr error) *sessionsModel {
	t.Helper()
	m := newSessions(styles.Default(), DefaultKeymap())
	m.sessions = []daemon.SessionState{
		{Name: "c-foo", Host: "local", State: "active"},
		{Name: "c-bar", Host: "local", State: "idle"},
	}
	t.Cleanup(stubCapture(content, captureErr))
	return &m
}

func TestPreviewSub_ToggleClearsStateOnOff(t *testing.T) {
	m := newPreviewSubModel(t, "old", nil)
	m.showPreview = true
	m.preview = "old"
	m.previewSession = "c-foo"
	m.previewErr = "stale"
	m.previewLoading = true

	updated, _ := m.Update(keyMsg("p"))
	if updated.showPreview || updated.preview != "" || updated.previewErr != "" ||
		updated.previewSession != "" || updated.previewLoading {
		t.Errorf("toggle-off must clear all preview state, got %+v", updated)
	}
}

func TestPreviewSub_StaleLoadDropped(t *testing.T) {
	m := newPreviewSubModel(t, "", nil)
	m.showPreview = true
	m.cursor = 1 // c-bar selected
	m.preview = "current bar"
	m.previewSession = "c-bar"
	updated, _ := m.Update(previewLoadedMsg{Session: "c-foo", Content: "stale foo"})
	if updated.preview != "current bar" {
		t.Errorf("stale capture clobbered current content: %q", updated.preview)
	}
}

func TestPreviewSub_TailLinesClampsAndTruncates(t *testing.T) {
	// 5 lines, ask for last 2, width 6 → last two lines, each clamped.
	got := tailLines("aaaa\nbbbb\ncccc\ndddddddd\neeeeeeee", 2, 6)
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("tailLines returned %d lines, want 2: %q", len(lines), got)
	}
	if lines[0] != "ddddd…" {
		t.Errorf("line not truncated to width: %q", lines[0])
	}
}

func TestPreviewSub_RemoteSessionUnsupported(t *testing.T) {
	// A remote session's capture returns the sentinel error (the cross-
	// host capture isn't wired). The real cmd must produce that error,
	// not attempt a local tmux capture.
	cmd := realCapturePreviewCmd(daemon.SessionState{Name: "c-remote", Host: "mini"})
	msg := cmd()
	lm, ok := msg.(previewLoadedMsg)
	if !ok {
		t.Fatalf("expected previewLoadedMsg, got %T", msg)
	}
	if lm.Err == nil {
		t.Error("remote session preview should return the not-wired sentinel error")
	}
}

// errFake is a tiny error type so tests don't pull in fmt at each site.
type errFake struct{ msg string }

func (e errFake) Error() string { return e.msg }
