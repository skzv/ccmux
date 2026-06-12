package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/config"
)

// TestAgentsSetDefaultModel_WritesConfig — `ccmux agents set-default-model X`
// is the muscle-memory + scripted path for pinning a model. Exercise
// the cobra command end-to-end (parse → RunE → on-disk effect) so a
// regression in the Cobra wiring or in config.Save is caught here.
func TestAgentsSetDefaultModel_WritesConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))

	// Set.
	c := newAgentsCmd()
	c.SetArgs([]string{"set-default-model", "claude-opus-4-8"})
	if err := c.Execute(); err != nil {
		t.Fatalf("set: %v", err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load after set: %v", err)
	}
	if cfg.Claude.DefaultModel != "claude-opus-4-8" {
		t.Errorf("DefaultModel = %q, want %q", cfg.Claude.DefaultModel, "claude-opus-4-8")
	}

	// Clear via no-arg form.
	c2 := newAgentsCmd()
	c2.SetArgs([]string{"set-default-model"})
	if err := c2.Execute(); err != nil {
		t.Fatalf("clear: %v", err)
	}
	cfg, _ = config.Load()
	if cfg.Claude.DefaultModel != "" {
		t.Errorf("DefaultModel should be cleared, got %q", cfg.Claude.DefaultModel)
	}

	// Whitespace gets trimmed so a stray space can't leak into the
	// shell command that becomes ANTHROPIC_MODEL.
	c3 := newAgentsCmd()
	c3.SetArgs([]string{"set-default-model", "  sonnet  "})
	if err := c3.Execute(); err != nil {
		t.Fatalf("set with whitespace: %v", err)
	}
	cfg, _ = config.Load()
	if cfg.Claude.DefaultModel != "sonnet" {
		t.Errorf("whitespace not trimmed: got %q", cfg.Claude.DefaultModel)
	}
}

// TestFormatTokens_HumanReadable — the table column rendering.
// Keeps the rules close to the helper: 0 hides as empty,
// 1M/200K shorthand for the common ranges.
func TestFormatTokens_HumanReadable(t *testing.T) {
	for _, tc := range []struct {
		in   int
		want string
	}{
		{0, ""},
		{200_000, "200K"},
		{1_000_000, "1M"},
		{2_000_000, "2M"},
		{500, "500"},
	} {
		if got := formatTokens(tc.in); got != tc.want {
			t.Errorf("formatTokens(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestAgentsCmd_RegisteredOnRoot — make sure the new subcommand is
// reachable from `ccmux agents` (i.e. wired into rootCmd). A
// regression here would mean someone deleted the AddCommand line and
// the feature silently disappeared from `ccmux --help`.
func TestAgentsCmd_RegisteredOnRoot(t *testing.T) {
	found := false
	var uses []string
	for _, c := range rootCmd.Commands() {
		uses = append(uses, c.Use)
		if c.Use == "agents" {
			found = true
		}
	}
	if !found {
		t.Errorf("rootCmd is missing the `agents` subcommand. Got: %s", strings.Join(uses, ", "))
	}
}
