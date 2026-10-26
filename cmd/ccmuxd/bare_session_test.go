package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
)

// The working-dir priority itself (explicit → default_dir → $HOME, with
// ~ expansion) is pinned by config.TestResolveBareSessionDir_*; the
// daemon and the TUI's local spawn both resolve through it.

// saveDiskConfig writes cfg as the config.toml under the test's $HOME,
// the file ccmuxd re-reads at session-create time.
func saveDiskConfig(t *testing.T, cfg config.Config) {
	t.Helper()
	if err := config.Save(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}
}

// newBareTestServer is a server over the logging fake tmux whose
// startup config — the one loaded when the daemon started — is startup.
func newBareTestServer(t *testing.T, startup config.Config) (*server, string) {
	t.Helper()
	logPath := fakeTmuxLog(t)
	s := &server{cfg: startup}
	// The fake tmux exits 0 for everything, so its has-session would
	// say every session exists.
	s.has = func(context.Context, string) (bool, error) { return false, nil }
	return s, logPath
}

// TestCreateBareSession_BlankPathUsesFreshDefaultDir — a blank path
// resolves against sessions.default_dir as config.toml says now, not as
// it said when the daemon started. `ccmux shell` kept opening the old
// default_dir after an edit until ccmuxd restarted (#234).
func TestCreateBareSession_BlankPathUsesFreshDefaultDir(t *testing.T) {
	stale := t.TempDir()
	fresh := t.TempDir()
	s, logPath := newBareTestServer(t, config.Config{Sessions: config.SessionsConfig{DefaultDir: stale}})
	disk := config.Defaults()
	disk.Sessions.DefaultDir = fresh
	saveDiskConfig(t, disk)

	rec := post(t, s.createBareSession, "/v1/sessions/bare", daemon.NewBareSessionRequest{Name: "fresh-dir", Agent: "shell"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var got daemon.NewBareSessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Path != fresh {
		t.Errorf("response path = %q, want the fresh default_dir %q (startup value was %q)", got.Path, fresh, stale)
	}
	creates := tmuxCallsWith(t, logPath, "new-session|")
	if len(creates) != 1 || !strings.Contains(creates[0], "|-c|"+fresh+"|") {
		t.Errorf("new-session calls = %q, want one starting in %s", creates, fresh)
	}

	// A default_dir that doesn't exist is an error naming it — never a
	// silent fallback to $HOME or to the startup value.
	missing := filepath.Join(fresh, "gone")
	disk.Sessions.DefaultDir = missing
	saveDiskConfig(t, disk)
	rec = post(t, s.createBareSession, "/v1/sessions/bare", daemon.NewBareSessionRequest{Name: "gone-dir", Agent: "shell"})
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), missing) {
		t.Errorf("missing default_dir: status %d body %q, want 404 naming %s", rec.Code, rec.Body, missing)
	}
	if creates := tmuxCallsWith(t, logPath, "new-session|"); len(creates) != 1 {
		t.Errorf("tmux new-session ran for a missing default_dir: %q", creates[1:])
	}
}

// TestCreateBareSession_ExplicitPathBeatsDefaultDir — an explicit path,
// including a ~ one, wins over the fresh default_dir.
func TestCreateBareSession_ExplicitPathBeatsDefaultDir(t *testing.T) {
	s, logPath := newBareTestServer(t, config.Config{})
	def := t.TempDir()
	disk := config.Defaults()
	disk.Sessions.DefaultDir = def
	saveDiskConfig(t, disk)
	home, _ := os.UserHomeDir() // fakeTmuxLog's temp $HOME
	explicit := filepath.Join(home, "explicit")
	if err := os.Mkdir(explicit, 0o755); err != nil {
		t.Fatal(err)
	}

	rec := post(t, s.createBareSession, "/v1/sessions/bare", daemon.NewBareSessionRequest{Name: "explicit", Path: "~/explicit", Agent: "shell"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	creates := tmuxCallsWith(t, logPath, "new-session|")
	if len(creates) != 1 || !strings.Contains(creates[0], "|-c|"+explicit+"|") {
		t.Errorf("new-session calls = %q, want one starting in %s", creates, explicit)
	}
}

// TestCreateBareSession_UnreadableConfigFallsBackToStartup — a corrupt
// config.toml mid-edit must not break new-session: the startup config
// still supplies default_dir.
func TestCreateBareSession_UnreadableConfigFallsBackToStartup(t *testing.T) {
	startup := t.TempDir()
	s, logPath := newBareTestServer(t, config.Config{Sessions: config.SessionsConfig{DefaultDir: startup}})
	p, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("[sessions\ndefault_dir = "), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := post(t, s.createBareSession, "/v1/sessions/bare", daemon.NewBareSessionRequest{Name: "corrupt", Agent: "shell"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	creates := tmuxCallsWith(t, logPath, "new-session|")
	if len(creates) != 1 || !strings.Contains(creates[0], "|-c|"+startup+"|") {
		t.Errorf("new-session calls = %q, want one starting in the startup default_dir %s", creates, startup)
	}
}

// TestCreateBareSession_UsesFreshDefaultAgent — with no agent in the
// request, agents.default comes from config.toml as it is now, the same
// read that supplies default_dir.
func TestCreateBareSession_UsesFreshDefaultAgent(t *testing.T) {
	s, logPath := newBareTestServer(t, config.Config{Agents: config.AgentsConfig{Default: "shell"}})
	disk := config.Defaults()
	disk.Agents.Default = "codex"
	saveDiskConfig(t, disk)

	rec := post(t, s.createBareSession, "/v1/sessions/bare", daemon.NewBareSessionRequest{Name: "fresh-agent", Path: t.TempDir()})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	assertTaggedAtCreation(t, logPath, "fresh-agent", "codex")
}

// TestExpandTilde_OnlyLeading — the helper deliberately does NOT
// handle mid-path tildes or other shell expansions (env vars, glob).
// Pinning that limitation keeps the next person from adding "smart"
// expansions to a daemon process, which is a quiet source of footguns.
func TestExpandTilde_OnlyLeading(t *testing.T) {
	t.Setenv("HOME", "/h")
	cases := []struct{ in, want string }{
		{"~", "/h"},
		{"~/foo", "/h/foo"},
		{"~foo", "~foo"},     // tilde-username NOT expanded
		{"/a/~/b", "/a/~/b"}, // mid-path tilde NOT expanded
		{"", ""},
		{"/abs", "/abs"},
	}
	for _, tc := range cases {
		if got := expandTilde(tc.in); got != tc.want {
			t.Errorf("expandTilde(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestExpandTilde_HOMEUnsetIsSafe — if $HOME isn't set (rare, but
// possible in stripped sandboxes) we should leave the tilde alone
// rather than expand to "". The bare-session creator would then
// fail os.Stat with a clearer error than "open '/foo': no such file".
func TestExpandTilde_HOMEUnsetIsSafe(t *testing.T) {
	t.Setenv("HOME", "")
	// On macOS, os.UserHomeDir() falls back to the SHELL or login
	// info even when HOME is empty, so we can't always force the
	// failure path. The assertion is "result is not empty" — either
	// the function expanded against the real home (fine) or it left
	// the tilde alone (also fine, both better than empty).
	for _, in := range []string{"~", "~/foo"} {
		got := expandTilde(in)
		if got == "" {
			t.Errorf("expandTilde(%q) returned empty even though we want a non-empty result on $HOME-unset", in)
		}
	}
	_ = os.Setenv
}
