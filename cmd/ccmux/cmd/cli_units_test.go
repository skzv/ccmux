package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/config"
)

// isolateHome points every home-derived path at a temp dir.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	return home
}

// TestHostRemove_UnknownNameErrorsWithoutWriting — `host remove typo`
// exited 0 (so a typo looked like success) and rewrote config.toml.
func TestHostRemove_UnknownNameErrorsWithoutWriting(t *testing.T) {
	isolateHome(t)
	cfg := config.Defaults()
	cfg.Hosts = []config.Host{{Name: "alpha", Address: "100.64.0.1"}}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	path, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}

	c := newHostCmd()
	c.SetArgs([]string{"remove", "ghost"})
	c.SilenceUsage, c.SilenceErrors = true, true
	err = c.Execute()
	if err == nil {
		t.Fatal("host remove of an unknown name should fail")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error should name the host: %v", err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Errorf("config.toml changed:\n%s", after)
	}
	if fi, err := os.Stat(path); err != nil || !fi.ModTime().Equal(old) {
		t.Errorf("config.toml was rewritten (stat %v, err %v; want mtime %v)", fi, err, old)
	}
}

// TestListConversations_SinceAcceptsDays — the help's own example
// (`--since 7d`) was rejected: time.ParseDuration has no `d` unit.
func TestListConversations_SinceAcceptsDays(t *testing.T) {
	isolateHome(t)
	c := newListConversationsCmd()
	c.SetArgs([]string{"--since", "7d", "--json"})
	c.SetOut(&bytes.Buffer{})
	c.SetErr(&bytes.Buffer{})
	c.SilenceUsage, c.SilenceErrors = true, true
	if err := c.Execute(); err != nil {
		t.Fatalf("list-conversations --since 7d: %v", err)
	}
}

// TestBuildUninstallPlan_RemovesEveryInstalledBinary — `make install`
// puts ccmux, ccmuxd AND ccmux-mcp in ~/.local/bin; uninstall left
// ccmux-mcp behind.
func TestBuildUninstallPlan_RemovesEveryInstalledBinary(t *testing.T) {
	home := isolateHome(t)
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, b := range []string{"ccmux", "ccmuxd", "ccmux-mcp"} {
		if err := os.WriteFile(filepath.Join(binDir, b), []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := buildUninstallPlan(false, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range []string{"ccmux", "ccmuxd", "ccmux-mcp"} {
		want := filepath.Join(binDir, b)
		found := false
		for _, p := range plan.paths {
			if p == want {
				found = true
			}
		}
		if !found {
			t.Errorf("uninstall plan doesn't remove %s; paths: %v", want, plan.paths)
		}
	}
}

// TestShellAttachCmd_InsideTmuxSwitchesClient — `ccmux shell` from a
// tmux pane created the session, then failed attach-session ("sessions
// should be nested with care").
func TestShellAttachCmd_InsideTmuxSwitchesClient(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-1000/default,4242,0")
	if got := strings.Join(shellAttachCmd("c-foo").Args, " "); got != "tmux switch-client -t "+exactTarget("c-foo") {
		t.Errorf("inside tmux, shell attach = %q, want a switch-client", got)
	}
}
