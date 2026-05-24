package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
)

// withTempCcmuxConfig points $HOME at a tempdir so config.Load /
// config.Save round-trip through an isolated ~/.config/ccmux/config.toml.
func withTempCcmuxConfig(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	// XDG_CONFIG_HOME wins over HOME for config.Path; clear it so the
	// HOME redirection actually takes effect.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home
}

// TestHostAdd_PersistsToConfig pins the daemon-host-management contract:
// `ccmux host add` writes a new entry to ~/.config/ccmux/config.toml so
// the dashboard's remote panel picks it up on next launch.
func TestHostAdd_PersistsToConfig(t *testing.T) {
	withTempCcmuxConfig(t)

	cmd := newHostCmd()
	cmd.SetArgs([]string{"add", "mac-mini", "100.64.0.5"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("host add: %v", err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if len(cfg.Hosts) != 1 {
		t.Fatalf("hosts after add: %d, want 1", len(cfg.Hosts))
	}
	got := cfg.Hosts[0]
	if got.Name != "mac-mini" || got.Address != "100.64.0.5" {
		t.Errorf("got %+v", got)
	}
	if got.Port != 7474 {
		t.Errorf("port = %d, want 7474 (the default)", got.Port)
	}
	if !got.Mosh {
		t.Error("default should opt into mosh — host add explicitly sets it")
	}
}

// TestHostRemove_DropsByName — removing a host filters by name. A
// missing name is a silent no-op (matches the current behavior).
func TestHostRemove_DropsByName(t *testing.T) {
	withTempCcmuxConfig(t)
	cfg, _ := config.Load()
	cfg.Hosts = []config.Host{
		{Name: "alpha", Address: "100.64.0.1"},
		{Name: "beta", Address: "100.64.0.2"},
	}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	cmd := newHostCmd()
	cmd.SetArgs([]string{"remove", "alpha"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("host remove: %v", err)
	}
	out, _ := config.Load()
	if len(out.Hosts) != 1 || out.Hosts[0].Name != "beta" {
		t.Errorf("after remove: %+v", out.Hosts)
	}
}

// TestHostList_TabularOutput — `ccmux host list` prints a table with
// the expected columns. A drift here is user-visible (scripts that
// parse the output).
func TestHostList_TabularOutput(t *testing.T) {
	withTempCcmuxConfig(t)
	cfg, _ := config.Load()
	cfg.Hosts = []config.Host{
		{Name: "mac-mini", Address: "100.64.0.5", User: "alice", Mosh: true},
	}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	// Capture stdout for the list output.
	orig := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	cmd := newHostCmd()
	cmd.SetArgs([]string{"list"})
	if err := cmd.Execute(); err != nil {
		os.Stdout = orig
		_ = w.Close()
		t.Fatalf("host list: %v", err)
	}
	_ = w.Close()
	os.Stdout = orig
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	got := buf.String()
	for _, want := range []string{"NAME", "ADDRESS", "USER", "MOSH", "mac-mini", "100.64.0.5", "alice", "true"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q in:\n%s", want, got)
		}
	}
}

// TestListCmd_JSONFlag — `ccmux list --json` emits a valid JSON
// array. Without a running daemon the sessions list comes from the
// local tmux server; with no tmux either, it's an empty array.
// Either way the output must be parseable JSON.
func TestListCmd_JSONFlag(t *testing.T) {
	// Capture stdout.
	orig := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	cmd := newListCmd()
	cmd.SetArgs([]string{"--json"})
	if err := cmd.Execute(); err != nil {
		os.Stdout = orig
		_ = w.Close()
		t.Fatalf("list --json: %v", err)
	}
	_ = w.Close()
	os.Stdout = orig
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	var sessions []daemon.SessionState
	if err := json.Unmarshal(buf.Bytes(), &sessions); err != nil {
		t.Errorf("--json output is not a valid JSON array of SessionState: %v\noutput:\n%s",
			err, buf.String())
	}
}

// TestKillCmd_RejectsExactArgs — Cobra validation: kill needs exactly
// one argument (the session/project name). Both zero and two args
// should be refused before the RunE function is called.
func TestKillCmd_RejectsExactArgs(t *testing.T) {
	cmd := newKillCmd()
	cmd.SetArgs([]string{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	if err := cmd.Execute(); err == nil {
		t.Error("kill with 0 args should error")
	}

	cmd = newKillCmd()
	cmd.SetArgs([]string{"a", "b"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	if err := cmd.Execute(); err == nil {
		t.Error("kill with 2 args should error")
	}
}

// TestDaemonCmd_HasExpectedSubcommands — drift-detector. The audit
// found `daemon install` writes the launchd plist; if a refactor drops
// a subcommand silently, the auto-start UX breaks on macOS with no
// warning to the user.
func TestDaemonCmd_HasExpectedSubcommands(t *testing.T) {
	cmd := newDaemonCmd()
	have := map[string]bool{}
	for _, sub := range cmd.Commands() {
		have[sub.Use] = true
	}
	for _, want := range []string{"start", "status", "stop", "restart", "install", "uninstall", "unit"} {
		if !have[want] {
			t.Errorf("daemon subcommand %q missing — was %v", want, keys(have))
		}
	}
}

// TestHostCmd_HasExpectedSubcommands — same drift-detector for `host`.
func TestHostCmd_HasExpectedSubcommands(t *testing.T) {
	cmd := newHostCmd()
	have := map[string]bool{}
	for _, sub := range cmd.Commands() {
		// Use field is "add <name> <address>" etc.; first token is the verb.
		verb := strings.Fields(sub.Use)[0]
		have[verb] = true
	}
	for _, want := range []string{"add", "remove", "list"} {
		if !have[want] {
			t.Errorf("host subcommand %q missing — was %v", want, keys(have))
		}
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
