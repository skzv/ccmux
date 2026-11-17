package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/daemonservice"
)

// TestDaemonUnit_CarriesAgentHomeOverrides — `ccmux daemon unit` prints
// the unit a Linux user installs by hand, so it must carry the agent
// home overrides set in this shell (escaped for systemd) and never a
// credential that happens to be exported alongside them.
func TestDaemonUnit_CarriesAgentHomeOverrides(t *testing.T) {
	for _, name := range daemonservice.PassthroughEnvNames() {
		t.Setenv(name, "")
	}
	t.Setenv("CODEX_HOME", "/srv/my codex/100%")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-should-never-leak")
	t.Setenv("OPENROUTER_API_KEY", "sk-or-should-never-leak")

	var out bytes.Buffer
	cmd := newDaemonCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"unit"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("daemon unit: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "\nEnvironment=\"CODEX_HOME=/srv/my codex/100%%\"\n") {
		t.Errorf("unit missing the escaped CODEX_HOME line:\n%s", got)
	}
	for _, leak := range []string{"ANTHROPIC_API_KEY", "OPENROUTER_API_KEY", "should-never-leak", "CLAUDE_CONFIG_DIR", "CURSOR_HOME", "ANTIGRAVITY_HOME"} {
		if strings.Contains(got, leak) {
			t.Errorf("unit contains %q:\n%s", leak, got)
		}
	}
}

func TestDaemonInstallEnvLine(t *testing.T) {
	if got := daemonInstallEnvLine(nil); got != "" {
		t.Errorf("no overrides: got %q, want no line", got)
	}
	got := daemonInstallEnvLine([]daemonservice.EnvVar{
		{Name: "CLAUDE_CONFIG_DIR", Value: "/srv/claude"},
		{Name: "CODEX_HOME", Value: "/srv/codex"},
	})
	for _, want := range []string{"CLAUDE_CONFIG_DIR=/srv/claude", "CODEX_HOME=/srv/codex", "ccmux daemon install"} {
		if !strings.Contains(got, want) {
			t.Errorf("install line %q missing %q", got, want)
		}
	}
}

// TestDaemonInstallHelp_NamesEveryOverride keeps the help text in step
// with the one list in daemonservice.
func TestDaemonInstallHelp_NamesEveryOverride(t *testing.T) {
	for _, sub := range newDaemonCmd().Commands() {
		if sub.Use != "install" {
			continue
		}
		for _, name := range daemonservice.PassthroughEnvNames() {
			if !strings.Contains(sub.Long, name) {
				t.Errorf("`daemon install` help does not mention %s:\n%s", name, sub.Long)
			}
		}
		return
	}
	t.Fatal("daemon install subcommand not found")
}
