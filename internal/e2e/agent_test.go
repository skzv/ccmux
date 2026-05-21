//go:build integration

package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/config"
)

// TestDefaultAgent_SwitchPersistsAndLaunches covers the config/agent
// CUJ: changing the default agent persists to the on-disk config, and a
// subsequently created bare session launches that agent's command
// rather than the built-in claude default.
//
// codex is chosen deliberately as the non-default agent — if the test
// used claude it could not tell "the config default was honored" apart
// from "the hardcoded claude default fired".
func TestDefaultAgent_SwitchPersistsAndLaunches(t *testing.T) {
	e := newEnv(t)

	// Switch the default agent to codex and persist it exactly the way
	// the TUI Settings screen does — config.Save to the sandbox's
	// config.toml (newEnv's writeConfig wraps config.Save).
	cfg := e.defaultConfig()
	cfg.Agents.Default = "codex"
	e.writeConfig(cfg)

	// Persistence: the change must survive a fresh load from disk.
	loaded, err := config.Load()
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if loaded.Agents.Default != "codex" {
		t.Fatalf("default agent did not persist: got %q, want %q", loaded.Agents.Default, "codex")
	}

	// A bare session created with no explicit --agent must launch the
	// configured default (codex). The daemon reads cfg.Agents.Default at
	// startup, so it is started after the config is written.
	e.startDaemon()
	const name = "c-agentdefault"
	if _, stderr, _ := e.ccmux("shell", "--name", name, "--path", e.Root); !e.hasSession(name) {
		t.Fatalf("`ccmux shell` did not create session %q\nstderr: %s", name, stderr)
	}

	// The stub agent echoes `ccmux-stub-agent=<name>` on launch; poll
	// the pane until the marker appears rather than racing the echo.
	var pane string
	if !waitFor(5*time.Second, func() bool {
		pane = e.capturePane(name)
		return strings.Contains(pane, "ccmux-stub-agent=")
	}) {
		t.Fatalf("agent stub never wrote its marker\npane:\n%s", pane)
	}
	if !strings.Contains(pane, "ccmux-stub-agent=codex") {
		t.Errorf("session launched the wrong agent — want codex\npane:\n%s", pane)
	}
	if strings.Contains(pane, "ccmux-stub-agent=claude") {
		t.Errorf("session launched claude despite codex being the configured default\npane:\n%s", pane)
	}
}
