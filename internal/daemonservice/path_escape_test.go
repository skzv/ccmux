package daemonservice

import (
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
)

func TestManagedPath_SkipsRelativeCommands(t *testing.T) {
	got := managedPath("/home/me", agent.Commands{
		Claude: "claude-dev",
		Codex:  "~/custom/bin/codex",
		Muse:   "/opt/muse/bin/muse",
	}, "/usr/bin")
	parts := strings.Split(got, ":")
	for _, p := range parts {
		if p == "." {
			t.Fatalf("managedPath injected CWD '.' into PATH: %q", got)
		}
	}
	for _, want := range []string{"/home/me/custom/bin", "/opt/muse/bin"} {
		if !strings.Contains(got, want) {
			t.Errorf("managedPath %q missing %q", got, want)
		}
	}
}

func TestRenderUnitFile_EscapesSpacesAndPercentInInstalledUnit(t *testing.T) {
	got := renderUnitFile("/home/my user/100%/ccmuxd", "/home/my user/100%/.local/bin:/usr/bin", true, nil)
	if !strings.Contains(got, `ExecStart="/home/my user/100%%/ccmuxd"`) {
		t.Errorf("ExecStart not properly escaped:\n%s", got)
	}
	if !strings.Contains(got, `Environment="PATH=/home/my user/100%%/.local/bin:/usr/bin"`) {
		t.Errorf("Environment PATH not properly escaped:\n%s", got)
	}
}
