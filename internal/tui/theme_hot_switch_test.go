package tui

import (
	"testing"

	"github.com/skzv/ccmux/internal/config"
)

func TestApp_ThemeHotSwitchPropagatesToAllScreens(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := config.Defaults()
	cfg.Theme = "dracula"

	a := New(cfg, "test")
	if a.styles.P.Name != "dracula" {
		t.Fatalf("startup theme = %q, want dracula", a.styles.P.Name)
	}
	if a.sessionsM.st.P.Name != "dracula" || a.settings.st.P.Name != "dracula" {
		t.Fatalf("child screens didn't inherit startup theme")
	}

	next := cfg
	next.Theme = "catppuccin-latte"
	a, _ = updateApp(t, a, configSavedMsg{Cfg: next})

	if a.styles.P.Name != "catppuccin-latte" || !a.styles.P.IsLight {
		t.Fatalf("after configSavedMsg, App theme = %q (IsLight=%v), want catppuccin-latte (true)", a.styles.P.Name, a.styles.P.IsLight)
	}
	if a.dashboard.st.P.Name != "catppuccin-latte" ||
		a.sessionsM.st.P.Name != "catppuccin-latte" ||
		a.projectsM.st.P.Name != "catppuccin-latte" ||
		a.conversationsM.st.P.Name != "catppuccin-latte" ||
		a.notes.st.P.Name != "catppuccin-latte" ||
		a.agentsM.st.P.Name != "catppuccin-latte" ||
		a.agentsM.claude.browser.st.P.Name != "catppuccin-latte" ||
		a.settings.st.P.Name != "catppuccin-latte" ||
		a.network.st.P.Name != "catppuccin-latte" {
		t.Fatal("theme hot-switch did not propagate to every child screen")
	}
}
