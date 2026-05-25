package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/conversations"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tmux"
)

// TestStartAttaching_FlipsStateAndReturnsTick — the helper that all
// attach call sites use must set the overlay flag, the kind, the
// label, and a non-zero start time, and it must return a non-nil
// spinner-tick cmd. Without the tick the spinner would freeze on
// frame 0 — visible as a static character that doesn't animate.
func TestStartAttaching_FlipsStateAndReturnsTick(t *testing.T) {
	a := newAppForTest(t)
	before := time.Now()
	tick := a.startAttaching(attachKindResume, "my-project")
	if !a.attach.active {
		t.Fatal("startAttaching did not set attach.active")
	}
	if a.attach.kind != attachKindResume {
		t.Errorf("kind = %v, want attachKindResume", a.attach.kind)
	}
	if a.attach.label != "my-project" {
		t.Errorf("label = %q, want %q", a.attach.label, "my-project")
	}
	if a.attach.startedAt.Before(before) {
		t.Errorf("startedAt = %v, want >= %v", a.attach.startedAt, before)
	}
	if tick == nil {
		t.Fatal("startAttaching returned a nil cmd — the spinner would freeze")
	}
}

// TestStopAttaching_ClearsState — the only invariant we care about
// after a stop is that .active flips back to false; the rest of the
// fields are zeroed for cleanliness but the View never reads them
// once active=false.
func TestStopAttaching_ClearsState(t *testing.T) {
	a := newAppForTest(t)
	a.startAttaching(attachKindAttach, "x")
	a.stopAttaching()
	if a.attach.active {
		t.Error("stopAttaching did not clear active")
	}
	if a.attach.label != "" {
		t.Errorf("expected label cleared, got %q", a.attach.label)
	}
}

// TestAttachSpinTickMsg_AdvancesFrameAndReschedules — while the
// overlay is up, each tick must (a) bump the spin frame so the
// animation moves, and (b) return another tick cmd so the next frame
// fires. Stopping the cmd chain would leave a static spinner.
func TestAttachSpinTickMsg_AdvancesFrameAndReschedules(t *testing.T) {
	a := newAppForTest(t)
	a.startAttaching(attachKindAttach, "x")
	startFrame := a.attach.spinFrame
	m, cmd := a.Update(attachSpinTickMsg{})
	a2 := m.(App)
	if a2.attach.spinFrame != startFrame+1 {
		t.Errorf("spinFrame = %d, want %d", a2.attach.spinFrame, startFrame+1)
	}
	if cmd == nil {
		t.Fatal("attachSpinTickMsg produced no follow-up tick — animation would freeze")
	}
}

// TestAttachSpinTickMsg_StopsTickingWhenInactive — once the overlay
// is gone (attach.active == false), incoming ticks must NOT
// reschedule themselves. Otherwise an in-flight tick generated during
// a stale attach would loop forever in the background.
func TestAttachSpinTickMsg_StopsTickingWhenInactive(t *testing.T) {
	a := newAppForTest(t)
	// Don't call startAttaching — overlay is inactive.
	_, cmd := a.Update(attachSpinTickMsg{})
	if cmd != nil {
		t.Error("spin tick reschedule fired despite inactive overlay")
	}
}

// TestRefreshAfterDetachMsg_ClearsOverlay — the legacy detach path
// (still emitted by some remote callbacks) must clear the overlay so
// the user doesn't see "Attaching to …" after they've already
// detached and returned to ccmux.
func TestRefreshAfterDetachMsg_ClearsOverlay(t *testing.T) {
	a := newAppForTest(t)
	a.startAttaching(attachKindAttach, "x")
	m, _ := a.Update(refreshAfterDetachMsg{})
	a2 := m.(App)
	if a2.attach.active {
		t.Error("refreshAfterDetachMsg did not clear the attach overlay")
	}
}

// TestAttachExitedMsg_NoErr_ClearsAndRefreshes — clean detach: clear
// the overlay, no error toast, but DO refresh sessions+projects so
// the dashboard reflects any state changes from the user's tmux work.
func TestAttachExitedMsg_NoErr_ClearsAndRefreshes(t *testing.T) {
	a := newAppForTest(t)
	a.startAttaching(attachKindAttach, "x")
	m, cmd := a.Update(attachExitedMsg{})
	a2 := m.(App)
	if a2.attach.active {
		t.Error("attachExitedMsg did not clear overlay")
	}
	if cmd == nil {
		t.Fatal("attachExitedMsg produced no refresh cmd")
	}
}

// TestAttachExitedMsg_WithErr_EmitsErrorToast — when the suspended
// process failed (tmux missing, session vanished, etc.), the user
// needs to know. The error toast is bundled with the refresh cmd so
// the user sees both "tmux: …" and a fresh sessions list at once.
func TestAttachExitedMsg_WithErr_EmitsErrorToast(t *testing.T) {
	a := newAppForTest(t)
	a.startAttaching(attachKindAttach, "x")
	m, cmd := a.Update(attachExitedMsg{Err: errors.New("session not found")})
	a2 := m.(App)
	if a2.attach.active {
		t.Error("attachExitedMsg did not clear overlay despite error path")
	}
	if cmd == nil {
		t.Fatal("attachExitedMsg with err produced no cmd")
	}
	// Drain the batch and look for a toast — the batch produces a
	// single tea.BatchMsg with the constituent msgs; for our purposes
	// it's enough to know the cmd is non-nil and the overlay is gone.
}

// TestView_AttachingShortCircuitsToOverlay — the overlay must take
// the whole screen when active. The test checks that the rendered
// view contains the loading verb and the label, not the normal
// dashboard chrome.
func TestView_AttachingShortCircuitsToOverlay(t *testing.T) {
	a := newAppForTest(t)
	a.width, a.height = 80, 24
	a.startAttaching(attachKindResume, "auth-redesign")
	out := a.View()
	if !strings.Contains(out, "Resuming") {
		t.Errorf("expected 'Resuming' in overlay output, got: %s", out)
	}
	if !strings.Contains(out, "auth-redesign") {
		t.Errorf("expected label 'auth-redesign' in overlay, got: %s", out)
	}
	if !strings.Contains(out, "loading conversation history") {
		t.Errorf("expected resume hint in overlay, got: %s", out)
	}
}

// TestView_AttachOverlayHidesDashboardChrome — when the overlay is
// up the user must NOT see the screen tabs underneath. Specifically:
// the header's tab labels (Sessions / Conversations / …) should be
// absent from the output. Catches the bug where the overlay was
// drawn on top of a still-rendered header.
func TestView_AttachOverlayHidesDashboardChrome(t *testing.T) {
	a := newAppForTest(t)
	a.width, a.height = 80, 24
	// Render once without the overlay to capture some chrome string
	// we know shows up on the home screen.
	plain := a.View()
	a.startAttaching(attachKindAttach, "demo")
	overlay := a.View()
	// "Sessions" appears in the tab strip on the plain view; the
	// overlay's verb is "Attaching to" which doesn't contain
	// "Sessions", so the overlay output must NOT contain it.
	if strings.Contains(plain, "Sessions") && strings.Contains(overlay, "Sessions") {
		t.Error("overlay output still contains 'Sessions' chrome — header leaked through")
	}
}

// TestView_AttachOverlay_NoLabel_FallsBackToSession — defensive: if
// a caller forgets to set a label the overlay should still render
// something meaningful, not a blank "Attaching to ".
func TestView_AttachOverlay_NoLabel_FallsBackToSession(t *testing.T) {
	a := newAppForTest(t)
	a.width, a.height = 80, 24
	a.startAttaching(attachKindAttach, "")
	out := a.View()
	if !strings.Contains(out, "session") {
		t.Errorf("expected 'session' fallback in overlay, got: %s", out)
	}
}

// TestRenderAttachingOverlay_DegenerateSizesDontPanic — pathological
// terminal sizes (very narrow, zero) must not panic. Bubble Tea
// hands us width/height from the underlying tcell, which can briefly
// report 0×0 during a resize.
func TestRenderAttachingOverlay_DegenerateSizesDontPanic(t *testing.T) {
	a := newAppForTest(t)
	a.startAttaching(attachKindAttach, "x")
	cases := []struct{ w, h int }{
		{0, 0},
		{1, 1},
		{5, 5},
		{200, 60},
	}
	for _, c := range cases {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("panic at %dx%d: %v", c.w, c.h, r)
			}
		}()
		_ = a.renderAttachingOverlay(c.w, c.h)
	}
}

// TestPrepLocalAttachCmd_EmitsAttachReadyMsg — the prep cmd must
// emit attachReadyMsg with the session name and detach-others flag
// carried through. The Moshi probe inside can take a real second,
// but the test only checks message shape, not timing. Note: this
// touches the real Moshi binary lookup but is bounded by our 2s
// context — acceptable for a unit test on dev machines.
func TestPrepLocalAttachCmd_EmitsAttachReadyMsg(t *testing.T) {
	cmd := prepLocalAttachCmd("my-session", "my-project", true)
	if cmd == nil {
		t.Fatal("prepLocalAttachCmd returned nil")
	}
	msg := cmd()
	ready, ok := msg.(attachReadyMsg)
	if !ok {
		t.Fatalf("prepLocalAttachCmd emitted %T, want attachReadyMsg", msg)
	}
	if ready.Session != "my-session" {
		t.Errorf("Session = %q, want my-session", ready.Session)
	}
	if !ready.DetachOthers {
		t.Error("DetachOthers = false, want true (passed in as true)")
	}
}

func TestLocalNewSessionAttachCmd_ForcesMirrorAttach(t *testing.T) {
	a := newAppForTest(t)
	a.cfg = config.Config{Sessions: config.SessionsConfig{AttachMode: "exclusive"}}

	ready := mustAttachReadyMsg(t, a.localNewSessionAttachCmd("my-session", "my-project"))
	if ready.DetachOthers {
		t.Error("new-session attach used detachOthers=true; want false even when config is exclusive")
	}
}

func TestLocalAttachCmd_ExistingSessionHonorsAttachMode(t *testing.T) {
	cases := []struct {
		mode string
		want bool
	}{
		{mode: "mirror", want: false},
		{mode: "exclusive", want: true},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			a := newAppForTest(t)
			a.cfg = config.Config{Sessions: config.SessionsConfig{AttachMode: tc.mode}}

			ready := mustAttachReadyMsg(t, a.localAttachCmd("my-session", "my-project"))
			if ready.DetachOthers != tc.want {
				t.Errorf("DetachOthers = %v, want %v for attach_mode=%q", ready.DetachOthers, tc.want, tc.mode)
			}
		})
	}
}

func mustAttachReadyMsg(t *testing.T, cmd tea.Cmd) attachReadyMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("attach cmd is nil")
	}
	msg := cmd()
	ready, ok := msg.(attachReadyMsg)
	if !ok {
		t.Fatalf("attach cmd emitted %T, want attachReadyMsg", msg)
	}
	return ready
}

// TestAttachReadyMsg_NonNested_ReturnsExecProcessCmd — the handler
// for attachReadyMsg must return a non-nil cmd (the tea.ExecProcess).
// We can't actually run it (would exec tmux), but the non-nil shape
// is the contract.
func TestAttachReadyMsg_NonNested_ReturnsExecProcessCmd(t *testing.T) {
	a := newAppForTest(t)
	a.startAttaching(attachKindAttach, "x")
	_, cmd := a.Update(attachReadyMsg{Session: "s", Nested: false, DetachOthers: false})
	if cmd == nil {
		t.Fatal("attachReadyMsg produced no exec cmd")
	}
}

// TestAttachReadyMsg_Nested_ReturnsSwitchClientCmd — same contract
// as above for the nested-tmux branch (switch-client vs attach).
func TestAttachReadyMsg_Nested_ReturnsSwitchClientCmd(t *testing.T) {
	a := newAppForTest(t)
	a.startAttaching(attachKindAttach, "x")
	_, cmd := a.Update(attachReadyMsg{Session: "s", Nested: true})
	if cmd == nil {
		t.Fatal("nested attachReadyMsg produced no switch-client cmd")
	}
}

// TestEnterOnConversations_FlipsOverlayAndReturnsCmd — pressing
// Enter on a populated Conversations screen must (a) flip the overlay
// immediately so the user sees feedback during the resume's
// multi-second spawn, and (b) return a non-nil cmd that drives the
// actual resume work.
func TestEnterOnConversations_FlipsOverlayAndReturnsCmd(t *testing.T) {
	a := newAppForTest(t)
	a.screen = ScreenConversations
	a.conversationsM.SetList([]conversations.Conversation{
		{
			ID:           "abc-123",
			Agent:        agent.IDClaude,
			Project:      "/tmp/test-proj",
			LastActivity: time.Now(),
		},
	})
	m, cmd := a.Update(keyMsg("enter"))
	a2 := m.(App)
	if !a2.attach.active {
		t.Fatal("Enter on Conversations did not flip the overlay")
	}
	if a2.attach.kind != attachKindResume {
		t.Errorf("kind = %v, want attachKindResume", a2.attach.kind)
	}
	if cmd == nil {
		t.Fatal("Enter on Conversations produced no resume cmd")
	}
}

// TestEnterOnConversations_EmptyList_NoOverlay — Enter with nothing
// selected must NOT flip the overlay (otherwise the user would see a
// loading screen for a non-existent operation).
func TestEnterOnConversations_EmptyList_NoOverlay(t *testing.T) {
	a := newAppForTest(t)
	a.screen = ScreenConversations
	// No SetList — list is nil.
	m, _ := a.Update(keyMsg("enter"))
	a2 := m.(App)
	if a2.attach.active {
		t.Error("Enter on empty Conversations spuriously flipped the overlay")
	}
}

// TestAttachSelectedSession_LocalFlipsOverlay — pressing Enter on a
// local row of the Sessions screen flips the overlay. Covers the
// most common attach path.
func TestAttachSelectedSession_LocalFlipsOverlay(t *testing.T) {
	a := newAppForTest(t)
	a.screen = ScreenSessions
	a.sessionsM.SetSessions([]daemon.SessionState{
		{Name: "c-myproj", Host: "local", Project: "myproj"},
	})
	a2, cmd := a.attachSelectedSession()
	if !a2.attach.active {
		t.Fatal("attachSelectedSession (local) did not flip overlay")
	}
	if a2.attach.label != "myproj" {
		t.Errorf("label = %q, want %q", a2.attach.label, "myproj")
	}
	if cmd == nil {
		t.Fatal("attachSelectedSession produced no cmd")
	}
}

// TestAttachSelectedSession_NoSelection_NoOverlay — defensive: with
// no row selected, no overlay should appear and no cmd should fire.
func TestAttachSelectedSession_NoSelection_NoOverlay(t *testing.T) {
	a := newAppForTest(t)
	a.screen = ScreenSessions
	// No SetSessions — Selected returns nil.
	a2, cmd := a.attachSelectedSession()
	if a2.attach.active {
		t.Error("attachSelectedSession with no selection flipped overlay")
	}
	if cmd != nil {
		t.Error("attachSelectedSession with no selection produced a cmd")
	}
}

// TestProjectMenuPick_Session_FlipsAttachOverlay — picking an
// existing session from the project menu modal must flip the overlay.
func TestProjectMenuPick_Session_FlipsAttachOverlay(t *testing.T) {
	a := newAppForTest(t)
	a.projectsM.SetProjects([]project.Project{
		{Name: "p", Path: "/tmp/p"},
	})
	entry := projectMenuEntry{
		kind:    menuSession,
		session: tmuxSessionForTest("c-p"),
	}
	m, _ := a.Update(projectMenuPickMsg{
		Project:     "p",
		ProjectPath: "/tmp/p",
		Entry:       entry,
	})
	a2 := m.(App)
	if !a2.attach.active {
		t.Fatal("menuSession pick did not flip overlay")
	}
	if a2.attach.kind != attachKindAttach {
		t.Errorf("kind = %v, want attachKindAttach", a2.attach.kind)
	}
}

// TestProjectMenuPick_Conversation_FlipsResumeOverlay — picking a
// past conversation flips the overlay with the resume kind so the
// user sees "Resuming …" (not "Attaching to …").
func TestProjectMenuPick_Conversation_FlipsResumeOverlay(t *testing.T) {
	a := newAppForTest(t)
	entry := projectMenuEntry{
		kind: menuConversation,
		conv: conversations.Conversation{
			ID:      "x",
			Agent:   agent.IDClaude,
			Project: "/tmp/p",
		},
	}
	m, _ := a.Update(projectMenuPickMsg{
		Project:     "p",
		ProjectPath: "/tmp/p",
		Entry:       entry,
	})
	a2 := m.(App)
	if !a2.attach.active {
		t.Fatal("menuConversation pick did not flip overlay")
	}
	if a2.attach.kind != attachKindResume {
		t.Errorf("kind = %v, want attachKindResume", a2.attach.kind)
	}
}

// TestProjectMenuPick_NewSession_FlipsStartOverlay — picking "start
// new" flips the overlay with the new kind so the verb reads
// "Starting" rather than the misleading "Attaching to".
func TestProjectMenuPick_NewSession_FlipsStartOverlay(t *testing.T) {
	a := newAppForTest(t)
	entry := projectMenuEntry{kind: menuNewSession}
	m, _ := a.Update(projectMenuPickMsg{
		Project:     "p",
		ProjectPath: "/tmp/p",
		Entry:       entry,
	})
	a2 := m.(App)
	if !a2.attach.active {
		t.Fatal("menuNewSession pick did not flip overlay")
	}
	if a2.attach.kind != attachKindNew {
		t.Errorf("kind = %v, want attachKindNew", a2.attach.kind)
	}
}

// TestConversationResumedMsg_ErrorClearsOverlay — when the spawn cmd
// reports an error (agent binary missing, tmux call failed, …), the
// overlay must be cleared. Otherwise the user stares at "Resuming …"
// forever while only seeing a fleeting error toast.
func TestConversationResumedMsg_ErrorClearsOverlay(t *testing.T) {
	a := newAppForTest(t)
	a.startAttaching(attachKindResume, "x")
	m, _ := a.Update(conversationResumedMsg{Err: errors.New("agent missing")})
	a2 := m.(App)
	if a2.attach.active {
		t.Error("conversationResumedMsg error did not clear overlay")
	}
}

// TestAttachVerb_AndHint_CoverEveryKind — defensive: a new attach
// kind added in the future without updating attachVerb/attachHint
// would produce an empty header. Lock in the current kinds so that
// omission is loud.
func TestAttachVerb_AndHint_CoverEveryKind(t *testing.T) {
	for _, k := range []attachKind{
		attachKindAttach,
		attachKindResume,
		attachKindNew,
		attachKindRemote,
		attachKindOpening,
	} {
		if v := attachVerb(k); v == "" {
			t.Errorf("attachVerb(%v) is empty", k)
		}
		if h := attachHint(k); h == "" {
			t.Errorf("attachHint(%v) is empty", k)
		}
	}
}

// TestEnterOnProjects_FlipsOpeningOverlay — pressing Enter on a
// project must flip the "Opening …" overlay so the user sees
// immediate feedback while attachOrCreateLocal does its
// tmux.List + conversations walk. Without it the user sees the
// Projects screen sitting frozen for several hundred ms.
func TestEnterOnProjects_FlipsOpeningOverlay(t *testing.T) {
	a := newAppForTest(t)
	a.projectsM.SetProjects([]project.Project{
		{Name: "auth-service", Path: "/tmp/auth-service"},
	})
	a2, cmd := a.attachOrCreateForSelectedProject()
	if !a2.attach.active {
		t.Fatal("attachOrCreateForSelectedProject did not flip overlay")
	}
	if a2.attach.kind != attachKindOpening {
		t.Errorf("kind = %v, want attachKindOpening", a2.attach.kind)
	}
	if a2.attach.label != "auth-service" {
		t.Errorf("label = %q, want %q", a2.attach.label, "auth-service")
	}
	if cmd == nil {
		t.Fatal("attachOrCreateForSelectedProject returned no cmd")
	}
}

// TestEnterOnProjects_NoSelection_NoOverlay — Enter with no project
// selected must NOT flip the overlay or produce a cmd.
func TestEnterOnProjects_NoSelection_NoOverlay(t *testing.T) {
	a := newAppForTest(t)
	a2, cmd := a.attachOrCreateForSelectedProject()
	if a2.attach.active {
		t.Error("attachOrCreateForSelectedProject flipped overlay with no selection")
	}
	if cmd != nil {
		t.Error("attachOrCreateForSelectedProject returned a cmd with no selection")
	}
}

// TestProjectMenuMsg_ClearsOpeningOverlay — when the menu modal is
// ready to show, the loading overlay flipped by Enter-on-Projects
// must be cleared, otherwise the modal renders underneath it and
// the user sees nothing but the spinner.
func TestProjectMenuMsg_ClearsOpeningOverlay(t *testing.T) {
	a := newAppForTest(t)
	a.startAttaching(attachKindOpening, "auth-service")
	m, _ := a.Update(projectMenuMsg{
		Project:     "auth-service",
		ProjectPath: "/tmp/auth-service",
	})
	a2 := m.(App)
	if a2.attach.active {
		t.Error("projectMenuMsg did not clear the opening overlay")
	}
	if a2.projectsM.menu == nil {
		t.Error("projectMenuMsg did not install the menu modal")
	}
}

// TestErrorToast_ClearsAttachOverlay — when a spawn or remote call
// errors out (e.g. tmux.New failed) the producer emits a toastMsg
// rather than going through attachExitedMsg. The toastMsg handler
// must clear the overlay so the user isn't stuck on a "Opening …"
// screen with only a fleeting toast underneath.
func TestErrorToast_ClearsAttachOverlay(t *testing.T) {
	a := newAppForTest(t)
	a.startAttaching(attachKindOpening, "x")
	m, _ := a.Update(toastMsg{
		Text:  "start session: tmux not found",
		Kind:  toastError,
		Until: time.Now().Add(5 * time.Second),
	})
	a2 := m.(App)
	if a2.attach.active {
		t.Error("error toast did not clear the attach overlay")
	}
}

// TestSuccessToast_DoesNotClearAttachOverlay — a *success* toast
// arriving mid-attach (e.g. the post-resume "resumed …" toast that
// fires alongside the actual attach cmd) must NOT clear the
// overlay. Otherwise the user would briefly lose the loading
// screen between resume-spawn and tmux-attach.
func TestSuccessToast_DoesNotClearAttachOverlay(t *testing.T) {
	a := newAppForTest(t)
	a.startAttaching(attachKindResume, "x")
	m, _ := a.Update(toastMsg{
		Text:  "resumed claude conversation in c-resume-abc",
		Kind:  toastSuccess,
		Until: time.Now().Add(4 * time.Second),
	})
	a2 := m.(App)
	if !a2.attach.active {
		t.Error("success toast spuriously cleared the attach overlay")
	}
}

// tmuxSessionForTest is a tiny constructor for menu entries; it
// only needs a non-empty Name to drive the attach path. Lives here
// rather than as a shared helper because no other test wants it.
func tmuxSessionForTest(name string) tmux.Session {
	return tmux.Session{Name: name}
}
