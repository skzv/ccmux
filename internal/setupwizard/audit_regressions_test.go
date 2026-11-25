package setupwizard

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/config"
)

func TestRegisterMCP_ReturnsErrorWhenClaudeMissing(t *testing.T) {
	withFakeClaudeHome(t)
	noClaudeCLI(t)

	var buf bytes.Buffer
	err := RegisterMCP(context.Background(), &buf, RegisterOptions{})
	if err == nil {
		t.Fatal("RegisterMCP returned nil error when Claude Code is not installed")
	}
	if !strings.Contains(err.Error(), "Claude Code not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDefaultAgentChoices_IncludesAllRegisteredAgents(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable script test")
	}
	bin := t.TempDir()
	fakeGrok := filepath.Join(bin, "grok")
	if err := os.WriteFile(fakeGrok, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("SHELL", "/bin/sh")

	choices := defaultAgentChoices(context.Background(), config.Config{})
	found := false
	for _, id := range choices {
		if id == agent.IDGrok {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("defaultAgentChoices = %v, want %q included", choices, agent.IDGrok)
	}
	if got := defaultAgentLabel(agent.IDGrok); got != agent.ByID(agent.IDGrok).DisplayName() {
		t.Errorf("defaultAgentLabel(grok) = %q, want %q", got, agent.ByID(agent.IDGrok).DisplayName())
	}
}

func TestDetectShellRC_FishQuotesSpaces(t *testing.T) {
	rc, line := detectShellRC("/Users/My Name", "/usr/bin/fish", "")
	if rc != "/Users/My Name/.config/fish/config.fish" {
		t.Errorf("rc = %q", rc)
	}
	want := `set -gx PATH '/Users/My Name/.local/bin' $PATH`
	if line != want {
		t.Errorf("line = %q, want %q", line, want)
	}
}

func TestShellResolvedAgentCandidate_RespectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := shellResolvedAgentCandidate(ctx, "sh"); got != "" {
		t.Errorf("canceled context returned %q, want empty", got)
	}
}
