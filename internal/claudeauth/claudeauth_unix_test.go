//go:build !windows

package claudeauth

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestFetch_ExpandsTildeInConfiguredCommand — `[agents.claude] command
// = "~/.local/bin/claude"` was exec'd with the literal tilde (no shell
// expands it), so `claude auth status` always failed and tier
// auto-detection silently fell back.
func TestFetch_ExpandsTildeInConfiguredCommand(t *testing.T) {
	resetCache(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho '{\"loggedIn\":true,\"subscriptionType\":\"max\"}'\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cfgDir := filepath.Join(home, ".config", "ccmux")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "[agents.claude]\ncommand = \"~/.local/bin/claude\"\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch with a ~/ claude command: %v", err)
	}
	if !s.LoggedIn || s.Tier() != "max5x" {
		t.Errorf("fetch = %+v, want the stub's logged-in max status", s)
	}
}
