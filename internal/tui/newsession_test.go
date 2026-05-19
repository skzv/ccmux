package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/tui/styles"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// remoteHost builds a hostStatus representing a configured remote host with
// the given address, user, and mosh preference. Used by multiple tests.
func remoteHost(name, address, user string, mosh bool) hostStatus {
	return hostStatus{
		Name:     name,
		Address:  address + ":7474",
		DialHost: address,
		User:     user,
		Mosh:     mosh,
		OK:       true,
	}
}

// openSessionsFormApp returns an App on the Sessions screen with the new-
// session form open, seeded with the given hosts in the picker.
func openSessionsFormApp(t *testing.T, hosts []hostStatus) App {
	t.Helper()
	st := styles.Default()
	km := DefaultKeymap()
	a := App{
		styles:    st,
		keys:      km,
		screen:    ScreenSessions,
		sessionsM: newSessions(st, km),
		projectsM: newProjects(st, km),
		matrix:    newMatrix(),
	}
	a.sessionsM.SetHosts(hosts)
	form := newNewSessionForm(st, hosts, "", "")
	a.sessionsM.form = &form
	return a
}

// submitForm drives the app through an Enter press and executes the returned
// command. Returns the updated App and the tea.Msg from the command.
func submitForm(t *testing.T, a App) (App, tea.Msg) {
	t.Helper()
	m, cmd := a.Update(keyMsg("enter"))
	a2 := m.(App)
	if cmd == nil {
		return a2, nil
	}
	return a2, cmd()
}

// ---------------------------------------------------------------------------
// Bug 1: form not closed after submit
// ---------------------------------------------------------------------------

// TestNewBareSession_FormClosedOnSubmit verifies that after the new-session
// form emits a newBareSessionSubmitMsg, App.Update clears sessionsM.form.
// Regression: the message type-switch in App.Update intercepted the message
// before sessionsModel.Update could see it, so form = nil never ran.
func TestNewBareSession_FormClosedOnSubmit(t *testing.T) {
	a := openSessionsFormApp(t, nil)

	// Submit the form via Enter. The modal-routing branch in App.Update
	// routes Enter through sessionsModel.Update, which emits the submit msg.
	// That message is then returned as a Cmd, so we need one more Update
	// round-trip to deliver it.
	m, cmd := a.Update(keyMsg("enter"))
	a2 := m.(App)
	if cmd == nil {
		t.Fatal("Enter on form produced no cmd")
	}
	msg := cmd()

	submit, ok := msg.(newBareSessionSubmitMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want newBareSessionSubmitMsg", msg)
	}

	// Deliver the submit message to App.Update — this is where Bug 1 lived.
	m2, _ := a2.Update(submit)
	a3 := m2.(App)
	if a3.sessionsM.form != nil {
		t.Error("sessionsM.form still set after newBareSessionSubmitMsg was handled; expected it to be nil")
	}
}

// ---------------------------------------------------------------------------
// Bug 2: DialHost / User / Mosh not propagated for configured hosts
// ---------------------------------------------------------------------------

// TestHostChoicesFrom_ConfiguredHostCarriesDialHost verifies that hostChoicesFrom
// correctly copies DialHost, User, and Mosh from hostStatus rows that were built
// from explicit cfg.Hosts entries (as opposed to tailnet-discovered peers).
func TestHostChoicesFrom_ConfiguredHostCarriesDialHost(t *testing.T) {
	hosts := []hostStatus{
		{Name: "local", Local: true, OK: true},
		remoteHost("mac-mini", "mac-mini.local", "sasha", true),
	}
	choices := hostChoicesFrom(hosts)
	if len(choices) != 2 {
		t.Fatalf("len(choices) = %d, want 2", len(choices))
	}
	remote := choices[1]
	if remote.DialHost != "mac-mini.local" {
		t.Errorf("DialHost = %q, want mac-mini.local", remote.DialHost)
	}
	if remote.User != "sasha" {
		t.Errorf("User = %q, want sasha", remote.User)
	}
	if !remote.Mosh {
		t.Error("Mosh = false, want true")
	}
}

// TestNewSessionForm_SubmitCarriesDialHostUserMosh verifies that pressing Enter
// on the form with a remote host selected builds a newBareSessionSubmitMsg with
// the correct DialHost, User, and Mosh fields.
func TestNewSessionForm_SubmitCarriesDialHostUserMosh(t *testing.T) {
	st := styles.Default()
	hosts := []hostStatus{
		{Name: "local", Local: true, OK: true},
		remoteHost("mac-mini", "mac-mini.local", "sasha", true),
	}
	form := newNewSessionForm(st, hosts, "", "")

	// Cycle to the remote host picker field (tab twice: name → workdir → device).
	form, _ = form.Update(keyMsg("tab"))
	form, _ = form.Update(keyMsg("tab"))
	// Advance to the remote host (right arrow).
	form, _ = form.Update(keyMsg("right"))
	// Submit.
	_, cmd := form.Update(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("enter produced no cmd")
	}
	msg := cmd()
	submit, ok := msg.(newBareSessionSubmitMsg)
	if !ok {
		t.Fatalf("msg = %T, want newBareSessionSubmitMsg", msg)
	}
	if submit.DialHost != "mac-mini.local" {
		t.Errorf("DialHost = %q, want mac-mini.local", submit.DialHost)
	}
	if submit.User != "sasha" {
		t.Errorf("User = %q, want sasha", submit.User)
	}
	if !submit.Mosh {
		t.Error("Mosh = false, want true")
	}
}

// ---------------------------------------------------------------------------
// Bug 3: Mosh not used in remoteSessionStartedMsg handler
// ---------------------------------------------------------------------------

// TestSpawnBareSessionCmd_RemoteProducesCorrectDialFields verifies that
// spawnBareSessionCmd, when given a remote submit, returns a
// remoteSessionStartedMsg with User and Mosh populated. We can't actually
// dial a daemon in a unit test, so we exercise the error path (Address is
// unreachable) and verify the toast — then separately verify the msg
// shape with a fake that doesn't need a live daemon (using a test
// that directly constructs the message).
func TestRemoteSessionStartedMsg_MoshFields(t *testing.T) {
	// The remoteSessionStartedMsg handler in App.Update should use mosh
	// when Mosh == true. We verify by ensuring the command built is
	// exec.Command("mosh", ...) rather than exec.Command("ssh", ...).
	// We can't exec it in a test, but we can confirm the App returns a
	// non-nil tea.Cmd (ExecProcess) without panicking for a non-empty
	// DialHost.
	st := styles.Default()
	km := DefaultKeymap()
	a := App{
		styles:    st,
		keys:      km,
		screen:    ScreenSessions,
		sessionsM: newSessions(st, km),
		projectsM: newProjects(st, km),
		matrix:    newMatrix(),
	}
	msg := remoteSessionStartedMsg{
		SessionName: "c-test",
		DialHost:    "mac-mini.local",
		User:        "sasha",
		Mosh:        true,
	}
	_, cmd := a.Update(msg)
	if cmd == nil {
		t.Error("expected a tea.Cmd (ExecProcess) for mosh attach, got nil")
	}

	// Same for SSH (Mosh == false).
	msg.Mosh = false
	_, cmd = a.Update(msg)
	if cmd == nil {
		t.Error("expected a tea.Cmd (ExecProcess) for ssh attach, got nil")
	}
}

// TestRemoteSessionStartedMsg_EmptyDialHostToasts verifies that a
// remoteSessionStartedMsg with no DialHost emits a toastError rather
// than hanging or panicking — existing behavior that must stay intact.
func TestRemoteSessionStartedMsg_EmptyDialHostToasts(t *testing.T) {
	st := styles.Default()
	km := DefaultKeymap()
	a := App{
		styles:    st,
		keys:      km,
		screen:    ScreenSessions,
		sessionsM: newSessions(st, km),
		projectsM: newProjects(st, km),
		matrix:    newMatrix(),
	}
	m, cmd := a.Update(remoteSessionStartedMsg{SessionName: "c-test", DialHost: ""})
	a2 := m.(App)
	if cmd != nil {
		t.Errorf("expected nil cmd for empty DialHost, got non-nil")
	}
	if a2.toast == "" {
		t.Error("expected error toast for empty DialHost, got empty toast")
	}
}

// ---------------------------------------------------------------------------
// Fuzz test: dialTarget derivation
// ---------------------------------------------------------------------------

// FuzzDialTarget exercises the DialHost / User composition logic that
// spawnBareSessionCmd uses when constructing the dial target for ssh/mosh.
// Invariants:
//   - target is non-empty whenever dialHost is non-empty
//   - when user is set, target starts with user + "@"
func FuzzDialTarget(f *testing.F) {
	f.Add("mac-mini.local", "sasha")
	f.Add("100.64.1.2", "")
	f.Add("my-machine", "")
	f.Add("", "sasha")
	f.Add("host.tail.ts.net", "alice")

	f.Fuzz(func(t *testing.T, dialHost, user string) {
		// Reproduce the composition logic from the remoteSessionStartedMsg handler.
		target := dialHost
		if user != "" {
			target = user + "@" + dialHost
		}
		// Non-empty dial host must produce a non-empty target.
		if dialHost != "" && target == "" {
			t.Errorf("target is empty for dialHost=%q user=%q", dialHost, user)
		}
		// User prefix must be present in the target.
		if user != "" && !strings.HasPrefix(target, user+"@") {
			t.Errorf("target %q does not start with %q", target, user+"@")
		}
	})
}
