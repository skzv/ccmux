package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/conversations"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// TestConversationsFilter_FollowsSymlinks — `c` on a project reached
// through a symlink (or anything under /tmp → /private/tmp on macOS)
// showed no conversations: agents record their real working directory,
// the project row has the linked path, and the filter compared the two
// strings. The project menu (conversations.ForProject) resolved
// symlinks and listed them, so the two disagreed. The filter now
// resolves both sides, nested directories included, and still keeps a
// sibling out.
func TestConversationsFilter_FollowsSymlinks(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real", "proj")
	sibling := filepath.Join(base, "real", "proj-other")
	for _, d := range []string{filepath.Join(real, "sub"), sibling} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	resolvedSibling, _ := filepath.EvalSymlinks(sibling)

	now := time.Now()
	list := []conversations.Conversation{
		{ID: "own", Agent: agent.IDClaude, Project: resolved, LastActivity: now},
		{ID: "nested", Agent: agent.IDClaude, Project: filepath.Join(resolved, "sub"), LastActivity: now.Add(-time.Minute)},
		{ID: "sibling", Agent: agent.IDClaude, Project: resolvedSibling, LastActivity: now.Add(-2 * time.Minute)},
	}
	m := newConversations(styles.Default(), DefaultKeymap())
	m.SetList(list)
	m.SetProjectFilter(link)
	var got []string
	for _, c := range m.filtered() {
		got = append(got, c.ID)
	}
	if strings.Join(got, ",") != "own,nested" {
		t.Errorf("filter %s listed %v, want [own nested]", link, got)
	}
	// The project menu agrees on the project's own conversations.
	if menu := conversations.ForProject(list, link); len(menu) != 1 || menu[0].ID != "own" {
		t.Errorf("ForProject(%s) = %v", link, menu)
	}
}
