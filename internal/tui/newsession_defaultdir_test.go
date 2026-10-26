package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// Issue #234: Sessions tab → n → Enter with the working dir left blank
// started the session in $HOME. The placeholder promised
// sessions.default_dir, and `ccmux shell` (resolved by ccmuxd) used it.

// stubBareTmuxNew swaps bareTmuxNew for a recorder of the start dirs
// local bare sessions are created in.
func stubBareTmuxNew(t *testing.T) *[]string {
	t.Helper()
	var dirs []string
	orig := bareTmuxNew
	bareTmuxNew = func(_ context.Context, _, dir, _, _ string) error {
		dirs = append(dirs, dir)
		return nil
	}
	t.Cleanup(func() { bareTmuxNew = orig })
	return &dirs
}

// writeSessionsDefaultDir saves a config.toml under the test's $HOME
// whose sessions.default_dir is dir ("" leaves it unset).
func writeSessionsDefaultDir(t *testing.T, dir string) {
	t.Helper()
	cfg := config.Defaults()
	cfg.Sessions.DefaultDir = dir
	if err := config.Save(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}
}

func mkdirUnder(t *testing.T, parent, name string) string {
	t.Helper()
	p := filepath.Join(parent, name)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestSpawnBareSession_ResolvesWorkdirLikeTheDaemon — the local spawn
// resolves the working dir by ccmuxd's rules (explicit →
// sessions.default_dir → $HOME, leading ~ expanded), from config.toml.
func TestSpawnBareSession_ResolvesWorkdirLikeTheDaemon(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	code := mkdirUnder(t, home, "code")
	other := mkdirUnder(t, home, "other")
	elsewhere := t.TempDir()

	cases := []struct {
		name       string
		defaultDir string // sessions.default_dir in config.toml
		path       string // the form's working-dir field
		want       string
	}{
		{"blank uses default_dir (#234)", code, "", code},
		{"whitespace uses default_dir", code, "   ", code},
		{"tilde default_dir expands", "~/code", "", code},
		{"bare tilde default_dir is $HOME", "~", "", home},
		{"default_dir outside $HOME", elsewhere, "", elsewhere},
		{"explicit path beats default_dir", code, other, other},
		{"explicit tilde path beats default_dir", code, "~/other", other},
		{"explicit path is trimmed", code, "  " + other + "  ", other},
		{"blank with no default_dir is $HOME", "", "", home},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dirs := stubBareTmuxNew(t)
			writeSessionsDefaultDir(t, tc.defaultDir)
			msg := spawnBareSessionCmd(newBareSessionSubmitMsg{Name: "s", Path: tc.path, Host: "local"})()
			if _, ok := msg.(bareSessionReadyMsg); !ok {
				t.Fatalf("spawn returned %T (%v), want bareSessionReadyMsg", msg, msg)
			}
			if len(*dirs) != 1 || (*dirs)[0] != tc.want {
				t.Errorf("tmux start dirs = %q, want [%q]", *dirs, tc.want)
			}
		})
	}
}

// TestSpawnBareSession_MissingDefaultDirIsAnError — a default_dir that
// doesn't exist is reported by name; it must not quietly fall back to
// $HOME, which is how #234 hid.
func TestSpawnBareSession_MissingDefaultDirIsAnError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dirs := stubBareTmuxNew(t)
	missing := filepath.Join(home, "no-such-dir")
	writeSessionsDefaultDir(t, missing)

	msg := spawnBareSessionCmd(newBareSessionSubmitMsg{Name: "s", Host: "local"})()
	tm, ok := msg.(toastMsg)
	if !ok || tm.Kind != toastError || !strings.Contains(tm.Text, missing) {
		t.Errorf("spawn returned %T (%+v), want an error toast naming %s", msg, msg, missing)
	}
	if len(*dirs) != 0 {
		t.Errorf("tmux new-session ran for a missing default_dir: %q", *dirs)
	}
}

// TestNewSessionForm_BlankWorkdirSubmitsEmptyPath — the form leaves a
// blank field blank for every device; the spawner (local) or the
// remote daemon fills in its own default_dir.
func TestNewSessionForm_BlankWorkdirSubmitsEmptyPath(t *testing.T) {
	st := styles.Default()
	hosts := []hostStatus{
		{Name: "local", Local: true, OK: true},
		remoteHost("mac-mini", "mac-mini.local", "sasha", false),
	}
	for _, remote := range []bool{false, true} {
		form := newNewSessionForm(st, hosts, "/work/code", "shell")
		if remote {
			form, _ = form.Update(keyMsg("tab"))
			form, _ = form.Update(keyMsg("tab"))
			form, _ = form.Update(keyMsg("right"))
		}
		_, cmd := form.Update(keyMsg("enter"))
		if cmd == nil {
			t.Fatalf("remote=%v: enter produced no cmd", remote)
		}
		submit, ok := cmd().(newBareSessionSubmitMsg)
		if !ok {
			t.Fatalf("remote=%v: enter did not submit", remote)
		}
		if submit.Path != "" {
			t.Errorf("remote=%v: submit.Path = %q, want blank", remote, submit.Path)
		}
		if wantHost := map[bool]string{false: "local", true: "mac-mini"}[remote]; submit.Host != wantHost {
			t.Errorf("remote=%v: submit.Host = %q, want %q", remote, submit.Host, wantHost)
		}
	}
}

// TestNewSessionForm_PlaceholderFollowsDevice — this machine's
// default_dir is only the fallback on this machine. With a remote
// device picked, the placeholder says that device's own default_dir
// (else its $HOME) applies, and switching back restores the local one.
func TestNewSessionForm_PlaceholderFollowsDevice(t *testing.T) {
	withLang(t, "en")
	st := styles.Default()
	hosts := []hostStatus{
		{Name: "local", Local: true, OK: true},
		remoteHost("mac-mini", "mac-mini.local", "sasha", false),
	}
	const def = "/work/code"
	localText := defaultDirPlaceholder(def, true)
	remoteText := defaultDirPlaceholder(def, false)
	if !strings.Contains(localText, def) {
		t.Fatalf("local placeholder %q doesn't name default_dir %q", localText, def)
	}
	if strings.Contains(remoteText, def) {
		t.Fatalf("remote placeholder %q names this machine's default_dir", remoteText)
	}

	form := newNewSessionForm(st, hosts, def, "shell")
	if got := form.workdir.Placeholder; got != localText {
		t.Errorf("on open: placeholder = %q, want %q", got, localText)
	}
	form, _ = form.Update(keyMsg("tab"))
	form, _ = form.Update(keyMsg("tab")) // device row
	form, _ = form.Update(keyMsg("right"))
	if got := form.workdir.Placeholder; got != remoteText {
		t.Errorf("remote picked: placeholder = %q, want %q", got, remoteText)
	}
	if view := ansi.Strip(form.View(120)); !strings.Contains(view, remoteText) {
		t.Errorf("remote placeholder not rendered:\n%s", view)
	}
	form, _ = form.Update(keyMsg("left"))
	if got := form.workdir.Placeholder; got != localText {
		t.Errorf("back to local: placeholder = %q, want %q", got, localText)
	}
	// Wrapping past the end lands on local again, and so does the
	// local placeholder.
	form, _ = form.Update(keyMsg("right"))
	form, _ = form.Update(keyMsg("right"))
	if got := form.workdir.Placeholder; got != localText {
		t.Errorf("wrapped to local: placeholder = %q, want %q", got, localText)
	}

	// Left/right on other rows never touch the placeholder.
	form = newNewSessionForm(st, hosts, def, "shell")
	form, _ = form.Update(keyMsg("right")) // name row: cursor move
	if got := form.workdir.Placeholder; got != localText {
		t.Errorf("right on the name row changed the placeholder to %q", got)
	}
}

// TestSessionsTab_BlankWorkdirOpensDefaultDir drives the reported
// flow end to end inside the TUI: Sessions tab, n, Enter with the
// working dir blank. The placeholder shows default_dir and the session
// is created there, not in $HOME.
func TestSessionsTab_BlankWorkdirOpensDefaultDir(t *testing.T) {
	withLang(t, "en")
	home := t.TempDir()
	t.Setenv("HOME", home)
	code := mkdirUnder(t, home, "code")
	writeSessionsDefaultDir(t, code)
	dirs := stubBareTmuxNew(t)

	a := openSessionsFormApp(t, nil)
	a.sessionsM.form = nil
	a.sessionsM.SetDefaultDir(code) // what App does on startup from config
	a.sessionsM.SetDefaultAgent("shell")

	m, _ := a.Update(keyMsg("n"))
	a = m.(App)
	if a.sessionsM.form == nil {
		t.Fatal("n on the Sessions tab didn't open the new-session form")
	}
	if ph := a.sessionsM.form.workdir.Placeholder; !strings.Contains(ph, code) {
		t.Errorf("placeholder %q doesn't show default_dir %q", ph, code)
	}

	m, cmd := a.Update(keyMsg("enter"))
	a = m.(App)
	if cmd == nil {
		t.Fatal("enter on the form produced no cmd")
	}
	submit, ok := cmd().(newBareSessionSubmitMsg)
	if !ok {
		t.Fatal("enter on the form did not submit")
	}
	_, cmd = a.Update(submit)
	if cmd == nil {
		t.Fatal("submit produced no spawn cmd")
	}
	if msg := cmd(); msg == nil {
		t.Fatal("spawn cmd returned nil")
	} else if _, ok := msg.(bareSessionReadyMsg); !ok {
		t.Fatalf("spawn returned %T (%v), want bareSessionReadyMsg", msg, msg)
	}
	if len(*dirs) != 1 || (*dirs)[0] != code {
		t.Errorf("session created in %q, want sessions.default_dir %q (not $HOME %q)", *dirs, code, home)
	}
}
