package tui

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/tui/styles"
)

// TestConversationsLoading_LegendFollowsAgentHomes — the loading
// legend names the directory each walker opens. With CLAUDE_CONFIG_DIR
// or CURSOR_HOME set the walkers now read the custom roots, so the
// legend must name those rather than ~/.claude/projects and
// ~/.cursor/projects, which ccmux no longer looks at. Without the
// variables the legend is unchanged.
func TestConversationsLoading_LegendFollowsAgentHomes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture paths are POSIX")
	}
	prev := homeDirForDisplay
	homeDirForDisplay = func() string { return "/home/user" }
	t.Cleanup(func() { homeDirForDisplay = prev })

	render := func() string {
		m := newConversations(styles.Default(), DefaultKeymap())
		m.SetLoading(true)
		return m.View(160, 40)
	}

	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CURSOR_HOME", "")
	out := render()
	for _, want := range []string{"~/.claude/projects", "~/.cursor/projects"} {
		if !strings.Contains(out, want) {
			t.Errorf("default legend missing %q:\n%s", want, out)
		}
	}

	t.Setenv("CLAUDE_CONFIG_DIR", "/home/user/.claude-work")
	t.Setenv("CURSOR_HOME", "/srv/cursor")
	out = render()
	for _, want := range []string{"~/.claude-work/projects", filepath.Join("/srv/cursor", "projects")} {
		if !strings.Contains(out, want) {
			t.Errorf("legend should name the custom root %q:\n%s", want, out)
		}
	}
	for _, stale := range []string{"~/.claude/projects", "~/.cursor/projects"} {
		if strings.Contains(out, stale) {
			t.Errorf("legend still names %q although the variable moves it:\n%s", stale, out)
		}
	}
}
