package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// newPreviewTestModel returns a sessionsModel with one selected
// session and a stubbed capturePreviewCmd that returns the canned
// content. Keeps these tests independent of a live tmux server — the
// no-live-tmux discipline the rest of the suite enforces.
func newPreviewTestModel(t *testing.T, content string, captureErr error) *sessionsModel {
	t.Helper()
	st := styles.Default()
	km := DefaultKeymap()
	m := newSessions(st, km)
	m.sessions = []daemon.SessionState{
		{Name: "c-foo", Host: "local", State: "active"},
		{Name: "c-bar", Host: "local", State: "idle"},
	}
	t.Cleanup(stubCapture(content, captureErr))
	return &m
}

// stubCapture overrides the package-level capturePreviewCmd for the
// duration of one test. Returns the cleanup func the test should defer.
func stubCapture(content string, err error) func() {
	orig := capturePreviewCmd
	capturePreviewCmd = func(s daemon.SessionState) tea.Cmd {
		return func() tea.Msg {
			return previewLoadedMsg{Session: s.Name, Content: content, Err: err}
		}
	}
	return func() { capturePreviewCmd = orig }
}

// TestPreviewToggle_FlipsAndFiresCapture — pressing `p` must flip
// showPreview AND emit a capture command for the selected session.
// We don't need the tick here (a second key call would reveal a tick
// scheduler bug, but that's covered separately).
func TestPreviewToggle_FlipsAndFiresCapture(t *testing.T) {
	m := newPreviewTestModel(t, "captured content\n", nil)
	updated, cmd := m.Update(keyMsg("p"))
	if !updated.showPreview {
		t.Fatal("p should turn showPreview on")
	}
	if cmd == nil {
		t.Fatal("p should emit a Cmd (capture + tick), got nil")
	}
}

// TestPreviewToggle_OffClearsState — pressing `p` again must turn the
// pane off AND clear the cached content so a future re-toggle doesn't
// flash the previous session's stale text for one frame.
func TestPreviewToggle_OffClearsState(t *testing.T) {
	m := newPreviewTestModel(t, "old text", nil)
	m.showPreview = true
	m.preview = "old text"
	m.previewSession = "c-foo"
	m.previewErr = "stale err"
	m.previewLoading = true

	updated, _ := m.Update(keyMsg("p"))
	if updated.showPreview {
		t.Error("second `p` press should turn showPreview off")
	}
	if updated.preview != "" {
		t.Errorf("preview should be cleared, got %q", updated.preview)
	}
	if updated.previewSession != "" {
		t.Errorf("previewSession should be cleared, got %q", updated.previewSession)
	}
	if updated.previewErr != "" {
		t.Errorf("previewErr should be cleared, got %q", updated.previewErr)
	}
	if updated.previewLoading {
		t.Error("previewLoading should be cleared on toggle off")
	}
}

// TestPreviewLoaded_PopulatesCache — when previewLoadedMsg arrives for
// the current selection, the model stores it and clears the loading
// flag. Sanity-check for the success path.
func TestPreviewLoaded_PopulatesCache(t *testing.T) {
	m := newPreviewTestModel(t, "", nil)
	m.showPreview = true
	m.previewLoading = true
	updated, _ := m.Update(previewLoadedMsg{Session: "c-foo", Content: "the pane content"})
	if updated.preview != "the pane content" {
		t.Errorf("preview = %q, want %q", updated.preview, "the pane content")
	}
	if updated.previewSession != "c-foo" {
		t.Errorf("previewSession = %q, want c-foo", updated.previewSession)
	}
	if updated.previewLoading {
		t.Error("previewLoading should clear when the capture lands")
	}
	if updated.previewErr != "" {
		t.Errorf("previewErr leaked: %q", updated.previewErr)
	}
}

// TestPreviewLoaded_StaleResultDropped — if the user moves the cursor
// while a capture is in flight, the late-arriving result must NOT
// overwrite the now-correct content. The model identifies "stale" by
// comparing the message's session name to the currently-selected name.
func TestPreviewLoaded_StaleResultDropped(t *testing.T) {
	m := newPreviewTestModel(t, "", nil)
	m.showPreview = true
	m.previewLoading = true
	m.cursor = 1 // c-bar is selected now
	m.preview = "current bar content"
	m.previewSession = "c-bar"

	// Late capture from when c-foo was selected:
	updated, _ := m.Update(previewLoadedMsg{Session: "c-foo", Content: "stale foo"})
	if updated.preview == "stale foo" {
		t.Error("stale capture from a previous selection clobbered current content")
	}
	if updated.preview != "current bar content" {
		t.Errorf("current content corrupted: %q", updated.preview)
	}
	if updated.previewLoading {
		t.Error("previewLoading should clear even when result is stale")
	}
}

// TestPreviewLoaded_ErrorRecorded — capture errors land in previewErr
// for the View to surface. Pins the contract so the renderer doesn't
// have to defend against an empty err + empty content.
func TestPreviewLoaded_ErrorRecorded(t *testing.T) {
	m := newPreviewTestModel(t, "", nil)
	m.showPreview = true
	m.preview = "previous content"
	updated, _ := m.Update(previewLoadedMsg{Session: "c-foo", Err: errFake{msg: "tmux exploded"}})
	if updated.previewErr != "tmux exploded" {
		t.Errorf("previewErr = %q, want tmux exploded", updated.previewErr)
	}
	if updated.preview != "" {
		t.Error("previous content must be cleared on error so the View shows the err, not stale text")
	}
}

// TestPreviewTick_ScheduledOnlyWhilePreviewOn — once the user toggles
// preview off, an in-flight tick that arrives later must NOT schedule
// the next tick. Otherwise the chain never dies.
func TestPreviewTick_ScheduledOnlyWhilePreviewOn(t *testing.T) {
	m := newPreviewTestModel(t, "", nil)
	m.showPreview = false
	_, cmd := m.Update(previewTickMsg{})
	if cmd != nil {
		t.Errorf("tick with showPreview=off must NOT schedule another; got cmd=%v", cmd)
	}
}

// TestPreviewTick_ReschedulesWhenOn — the live path: tick arrives,
// preview is still on, we get a non-nil Cmd back (capture + next tick
// batched).
func TestPreviewTick_ReschedulesWhenOn(t *testing.T) {
	m := newPreviewTestModel(t, "captured", nil)
	m.showPreview = true
	_, cmd := m.Update(previewTickMsg{})
	if cmd == nil {
		t.Fatal("tick with showPreview=on must return a Cmd (capture + next tick)")
	}
}

// TestCursorMove_RefreshesPreview — moving down with preview on must
// fire an immediate capture so the right pane updates without waiting
// a full second for the tick. Without preview, no capture should fire.
func TestCursorMove_RefreshesPreview(t *testing.T) {
	m := newPreviewTestModel(t, "captured", nil)
	m.showPreview = true

	_, cmd := m.Update(keyMsg("down"))
	if cmd == nil {
		t.Error("Down with preview on should fire an immediate capture")
	}

	m2 := newPreviewTestModel(t, "captured", nil)
	m2.showPreview = false
	_, cmd2 := m2.Update(keyMsg("down"))
	if cmd2 != nil {
		t.Errorf("Down with preview off should NOT fire a capture, got %v", cmd2)
	}
}

// TestView_PreviewOff_KeepsStackedLayout — sanity check: when preview
// is off, the View still includes the bottom detail pane (its presence
// is detectable by one of the labels that only appears there).
func TestView_PreviewOff_KeepsStackedLayout(t *testing.T) {
	m := newPreviewTestModel(t, "", nil)
	m.showPreview = false
	out := m.View(120, 24, false)
	if !strings.Contains(out, "state") {
		t.Error("detail pane (which says 'state') missing from default layout")
	}
}

// TestView_PreviewOn_RendersCapturedBody — when preview is on, the
// captured body shows up in the rendered output. Confirms the renderer
// reads `m.preview` and isn't accidentally reading the stale detail
// pane.
func TestView_PreviewOn_RendersCapturedBody(t *testing.T) {
	m := newPreviewTestModel(t, "", nil)
	m.showPreview = true
	m.preview = "MARKER_LINE_IN_PREVIEW"
	out := m.View(120, 24, false)
	if !strings.Contains(out, "MARKER_LINE_IN_PREVIEW") {
		t.Errorf("preview body missing from rendered output; got:\n%s", out)
	}
}

// TestView_PreviewOn_NarrowFallsBack — narrow terminals (phone) must
// fall back to the default stacked layout even with showPreview=true.
// A side-by-side split below 80 cols is unreadable.
func TestView_PreviewOn_NarrowFallsBack(t *testing.T) {
	m := newPreviewTestModel(t, "", nil)
	m.showPreview = true
	m.preview = "wide content body"
	out := m.View(60, 30, true) // narrow=true
	// The detail pane (which we drop in the wide preview layout) must
	// reappear in narrow mode.
	if !strings.Contains(out, "state") {
		t.Error("narrow layout with showPreview=true should still show the detail pane")
	}
}

// TestView_PreviewOn_RemotePreviewMessage — when the daemon returns
// the "remote not wired" sentinel, the View surfaces a readable
// message instead of an empty pane.
func TestView_PreviewOn_RemotePreviewMessage(t *testing.T) {
	m := newPreviewTestModel(t, "", nil)
	m.showPreview = true
	m.previewErr = errRemotePreviewNotWired.Error()
	out := m.View(120, 24, false)
	if !strings.Contains(out, "preview unavailable") {
		t.Errorf("expected 'preview unavailable' header in error state; got:\n%s", out)
	}
	if !strings.Contains(out, "remote") {
		t.Errorf("error body should mention remote limitation; got:\n%s", out)
	}
}

// errFake is a tiny error type so we don't pull in fmt.Errorf at every
// test site. The msg field is the body the View will surface.
type errFake struct{ msg string }

func (e errFake) Error() string { return e.msg }
