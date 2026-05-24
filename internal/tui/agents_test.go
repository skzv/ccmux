package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/antigravityconfig"
	"github.com/skzv/ccmux/internal/codexconfig"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// TestAgents_NarrowLayout — at phone width the Agents screen keeps the
// subtab labels and the active agent's config block headings (T0/T1)
// but drops the subtab hint, the settings-file path, and the per-agent
// Keys cheatsheet (T2), with no line overflowing the terminal.
func TestAgents_NarrowLayout(t *testing.T) {
	m := newAgents(styles.Default(), DefaultKeymap())
	out := m.View(50, 60)
	assertNoOverflow(t, out, 50)
	assertPresent(t, out, "Claude", "Codex", "Antigravity", "Default model")
	assertAbsent(t, out, "switch agent", "settings:", "pick default model")
}

func TestAgents_CodexThinkingModeKeyPersistsAndRefreshes(t *testing.T) {
	setIsolatedAgentHomes(t)
	m := newAgents(styles.Default(), DefaultKeymap())
	m = switchAgentsSubtab(t, m, agent.IDCodex)

	m, _ = m.Update(keyMsg("r"))

	settings, err := codexconfig.ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.ModelReasoningEffort != "high" {
		t.Fatalf("model_reasoning_effort = %q, want high", settings.ModelReasoningEffort)
	}
	if m.codex.settings == nil || m.codex.settings.ModelReasoningEffort != "high" {
		t.Fatalf("model effort = %q, want refreshed high", m.codex.settings.ModelReasoningEffort)
	}
	if !strings.Contains(m.codex.saveMsg, "Codex effort") {
		t.Fatalf("saveMsg = %q, want Codex effort success", m.codex.saveMsg)
	}
}

func TestAgents_AntigravityThinkingModeKeyPersistsAndRefreshes(t *testing.T) {
	setIsolatedAgentHomes(t)
	m := newAgents(styles.Default(), DefaultKeymap())
	m = switchAgentsSubtab(t, m, agent.IDAntigravity)

	m, _ = m.Update(keyMsg("r"))

	settings, err := antigravityconfig.ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.ReasoningEffort != "high" {
		t.Fatalf("reasoningEffort = %q, want high", settings.ReasoningEffort)
	}
	if m.antigravity.settings == nil || m.antigravity.settings.ReasoningEffort != "high" {
		t.Fatalf("model effort = %q, want refreshed high", m.antigravity.settings.ReasoningEffort)
	}
	if !strings.Contains(m.antigravity.saveMsg, "Antigravity effort") {
		t.Fatalf("saveMsg = %q, want Antigravity effort success", m.antigravity.saveMsg)
	}
}

func TestAgents_ThinkingModeKeysAreScopedToActiveAgent(t *testing.T) {
	homes := setIsolatedAgentHomes(t)
	if err := os.WriteFile(filepath.Join(homes.codex, "config.toml"), []byte("model_reasoning_effort = \"medium\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homes.antigravity, "settings.json"), []byte(`{"reasoningEffort":"medium"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newAgents(styles.Default(), DefaultKeymap())

	m = switchAgentsSubtab(t, m, agent.IDCodex)
	m, _ = m.Update(keyMsg("r"))
	codexSettings, err := codexconfig.ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	antigravitySettings, err := antigravityconfig.ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if codexSettings.ModelReasoningEffort != "low" {
		t.Fatalf("Codex effort = %q, want low", codexSettings.ModelReasoningEffort)
	}
	if antigravitySettings.ReasoningEffort != "medium" {
		t.Fatalf("Antigravity effort changed while Codex was active: %q", antigravitySettings.ReasoningEffort)
	}

	m = switchAgentsSubtab(t, m, agent.IDAntigravity)
	m, _ = m.Update(keyMsg("r"))
	codexSettings, err = codexconfig.ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	antigravitySettings, err = antigravityconfig.ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if codexSettings.ModelReasoningEffort != "low" {
		t.Fatalf("Codex effort changed while Antigravity was active: %q", codexSettings.ModelReasoningEffort)
	}
	if antigravitySettings.ReasoningEffort != "low" {
		t.Fatalf("Antigravity effort = %q, want low", antigravitySettings.ReasoningEffort)
	}
}

func TestAgents_CodexThinkingModeKeyCyclesRepeatedly(t *testing.T) {
	homes := setIsolatedAgentHomes(t)
	if err := os.WriteFile(filepath.Join(homes.codex, "config.toml"), []byte("model_reasoning_effort = \"medium\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newAgents(styles.Default(), DefaultKeymap())
	m = switchAgentsSubtab(t, m, agent.IDCodex)

	for _, want := range []string{"low", "minimal", ""} {
		m, _ = m.Update(keyMsg("r"))
		settings, err := codexconfig.ReadSettings()
		if err != nil {
			t.Fatal(err)
		}
		if settings.ModelReasoningEffort != want {
			t.Fatalf("model_reasoning_effort = %q, want %q", settings.ModelReasoningEffort, want)
		}
		if m.codex.settings == nil || m.codex.settings.ModelReasoningEffort != want {
			t.Fatalf("model effort = %q, want refreshed %q", m.codex.settings.ModelReasoningEffort, want)
		}
	}
}

func TestApp_AgentsCodexThinkingModeKeyBypassesGlobalRefresh(t *testing.T) {
	homes := setIsolatedAgentHomes(t)
	if err := os.WriteFile(filepath.Join(homes.codex, "config.toml"), []byte("model_reasoning_effort = \"medium\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := newAppForTest(t)
	a.screen = ScreenAgents
	a.agentsM = switchAgentsSubtab(t, a.agentsM, agent.IDCodex)

	model, cmd := a.Update(keyMsg("r"))
	if cmd != nil {
		t.Fatal("Agents r key returned a command; likely global refresh intercepted it")
	}
	a = model.(App)

	settings, err := codexconfig.ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.ModelReasoningEffort != "low" {
		t.Fatalf("model_reasoning_effort = %q, want low", settings.ModelReasoningEffort)
	}
	if a.agentsM.codex.settings == nil || a.agentsM.codex.settings.ModelReasoningEffort != "low" {
		t.Fatalf("model effort = %q, want refreshed low", a.agentsM.codex.settings.ModelReasoningEffort)
	}
}

// TestCodex_YoloToggleWritesConfig — pressing "y" on the Codex subtab
// writes the YOLO combo (approval_policy="never" + sandbox_mode=
// "danger-full-access") into config.toml. Pressing again clears them
// back to empty. Verifies both the on-disk state and that the screen
// reloaded and surfaced a save-success flash.
func TestCodex_YoloToggleWritesConfig(t *testing.T) {
	setIsolatedAgentHomes(t)
	m := newAgents(styles.Default(), DefaultKeymap())
	m = switchAgentsSubtab(t, m, agent.IDCodex)

	// On.
	m, _ = m.Update(keyMsg("y"))
	got, err := codexconfig.ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.ApprovalPolicy != codexconfig.YoloApprovalPolicy {
		t.Errorf("approval_policy = %q, want %q", got.ApprovalPolicy, codexconfig.YoloApprovalPolicy)
	}
	if got.SandboxMode != codexconfig.YoloSandboxMode {
		t.Errorf("sandbox_mode = %q, want %q", got.SandboxMode, codexconfig.YoloSandboxMode)
	}
	if m.codex.err != "" {
		t.Errorf("codex.err = %q, want empty", m.codex.err)
	}
	if !strings.Contains(m.codex.saveMsg, "Codex YOLO") {
		t.Errorf("saveMsg = %q, want Codex YOLO success", m.codex.saveMsg)
	}

	// Off.
	m, _ = m.Update(keyMsg("y"))
	got, err = codexconfig.ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.ApprovalPolicy == codexconfig.YoloApprovalPolicy {
		t.Errorf("approval_policy still %q after second toggle, want cleared", got.ApprovalPolicy)
	}
	if got.SandboxMode == codexconfig.YoloSandboxMode {
		t.Errorf("sandbox_mode still %q after second toggle, want cleared", got.SandboxMode)
	}
}

// TestAntigravity_YoloToggleWritesSettings — pressing "y" on the
// Antigravity subtab flips the yolo bool in settings.json. Pressing
// again reverts. Mirrors the codex YOLO toggle test.
func TestAntigravity_YoloToggleWritesSettings(t *testing.T) {
	setIsolatedAgentHomes(t)
	m := newAgents(styles.Default(), DefaultKeymap())
	m = switchAgentsSubtab(t, m, agent.IDAntigravity)

	// On.
	m, _ = m.Update(keyMsg("y"))
	got, err := antigravityconfig.ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !got.Yolo {
		t.Errorf("antigravity yolo = false, want true after toggle")
	}
	if m.antigravity.err != "" {
		t.Errorf("antigravity.err = %q, want empty", m.antigravity.err)
	}
	if !strings.Contains(m.antigravity.saveMsg, "Antigravity YOLO") {
		t.Errorf("saveMsg = %q, want Antigravity YOLO success", m.antigravity.saveMsg)
	}

	// Off.
	m, _ = m.Update(keyMsg("y"))
	got, err = antigravityconfig.ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.Yolo {
		t.Errorf("antigravity yolo still true after second toggle, want false")
	}
}

func TestAgents_ThinkingModeWriteErrorDoesNotKeepSuccessState(t *testing.T) {
	homes := setIsolatedAgentHomes(t)
	m := newAgents(styles.Default(), DefaultKeymap())
	m = switchAgentsSubtab(t, m, agent.IDCodex)

	m, _ = m.Update(keyMsg("r"))
	if m.codex.saveMsg == "" {
		t.Fatal("expected initial successful save")
	}
	if err := os.RemoveAll(homes.codex); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(homes.codex, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}

	m, _ = m.Update(keyMsg("r"))

	if m.codex.err == "" {
		t.Fatal("expected write error")
	}
	if m.codex.saveMsg != "" {
		t.Fatalf("saveMsg = %q, want cleared after write error", m.codex.saveMsg)
	}
}

type agentHomes struct {
	codex       string
	antigravity string
}

func setIsolatedAgentHomes(t *testing.T) agentHomes {
	t.Helper()
	homes := agentHomes{
		codex:       t.TempDir(),
		antigravity: t.TempDir(),
	}
	t.Setenv("CODEX_HOME", homes.codex)
	t.Setenv("ANTIGRAVITY_HOME", homes.antigravity)
	return homes
}

func switchAgentsSubtab(t *testing.T, m agentsModel, want agent.ID) agentsModel {
	t.Helper()
	for range agent.All() {
		if m.active == want {
			return m
		}
		var cmd tea.Cmd
		m, cmd = m.Update(keyMsg("l"))
		if cmd != nil {
			t.Fatal("subtab switch returned unexpected command")
		}
	}
	t.Fatalf("could not switch to %s", want)
	return m
}
