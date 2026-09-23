package setupwizard

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/config"
)

// fakeAgentBinaries puts executable stubs for `names` on an otherwise
// empty PATH and neutralizes the login-shell lookup.
func fakeAgentBinaries(t *testing.T, names ...string) {
	t.Helper()
	bin := t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(bin, n), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	t.Setenv("SHELL", "/bin/false")
}

// TestConfigureAgentCommands_SkipsAgentsWithoutCommandField —
// regression: for second-wave agents (opencode, kimi, droid, …)
// config.toml has no [agents.<id>] command field, yet the wizard
// "selected" a command for them, reported a change (printing "wrote
// agent command selection") and — since nothing was saved — did the
// same again on every run. Those agents must be skipped.
func TestConfigureAgentCommands_SkipsAgentsWithoutCommandField(t *testing.T) {
	fakeAgentBinaries(t, "opencode", "kimi", "droid")
	cfg := config.Config{}
	var out bytes.Buffer

	changed, err := configureAgentCommands(withAssumeYes(context.Background()), &out, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Errorf("reported a config change for agents with no command field; output:\n%s", out.String())
	}
	if strings.Contains(out.String(), "using ") {
		t.Errorf("claimed to pick a command for an agent it can't pin:\n%s", out.String())
	}

	for _, a := range []agent.Agent{agent.OpenCode{}, agent.Kimi{}, agent.Droid{}} {
		if changed, _ := configureAgentCommand(withAssumeYes(context.Background()), &out, &cfg, a); changed {
			t.Errorf("configureAgentCommand(%s) reported a change", a.ID())
		}
	}
}

// TestConfigureAgentCommands_PinsAgentsWithCommandField — control: an
// agent that does have a command field still gets its PATH binary
// pinned.
func TestConfigureAgentCommands_PinsAgentsWithCommandField(t *testing.T) {
	fakeAgentBinaries(t, "codex", "opencode")
	cfg := config.Config{}
	var out bytes.Buffer

	changed, err := configureAgentCommands(withAssumeYes(context.Background()), &out, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || !strings.HasSuffix(cfg.Agents.Codex.Command, "codex") {
		t.Errorf("codex command not pinned: changed=%v command=%q", changed, cfg.Agents.Codex.Command)
	}
}
