package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tui/styles"
)

func TestLegacyGeminiProjectStartsGemini(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".ccmux"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".ccmux", "agent"), []byte("gemini\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if id := project.ReadAgent(dir); id != agent.IDGemini {
		t.Fatal(id)
	}
	cmd := launchCmdForProjectPathWithCommands(dir, agent.Commands{Gemini: "/custom/gemini", Antigravity: "/custom/agy"})
	if cmd != "/custom/gemini || zsh || bash || sh" {
		t.Fatal(cmd)
	}
}

func TestGeminiSettingsAndTabLayout(t *testing.T) {
	m := newAgents(styles.Default(), DefaultKeymap())
	m.active = agent.IDGemini
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	if cmd == nil {
		t.Fatal("missing settings editor action")
	}
	msg, ok := cmd().(openEditorMsg)
	if !ok || msg.Path != filepath.Join(home, ".gemini", "settings.json") {
		t.Fatal(msg)
	}
	for _, width := range []int{60, 100, 120, 160} {
		m.SetSize(width, 40)
		view := m.View(width, 40)
		if !strings.Contains(view, "Gemini") || lipgloss.Width(view) > width || lipgloss.Height(view) > 40 {
			t.Fatalf("layout %d: %dx%d", width, lipgloss.Width(view), lipgloss.Height(view))
		}
	}
}
