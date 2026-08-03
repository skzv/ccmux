package tui

import (
	"strings"
	"testing"
)

// TestHelpForScreen_AgentsNoStaleJShortcut is the regression test for
// the help overlay advertising `j` = "edit ~/.claude/settings.json
// directly": since the PR #164 rework, `j` is consumed as pure
// down-navigation on the Agents screen and never opens settings.json.
// Help must describe the actual binding (row navigation + Enter).
func TestHelpForScreen_AgentsNoStaleJShortcut(t *testing.T) {
	items := helpForScreen(ScreenAgents, DefaultKeymap())
	if len(items) == 0 {
		t.Fatal("Agents screen has no help items")
	}
	for _, it := range items {
		if it.Key == "j" {
			t.Errorf("Agents help still advertises a bare `j` shortcut (%q) — j is down-navigation now", it.Desc)
		}
	}
	// settings.json editing still exists (Enter on its row) and must
	// stay discoverable from the help overlay.
	var mentionsSettings bool
	for _, it := range items {
		if strings.Contains(it.Desc, "settings.json") {
			mentionsSettings = true
		}
	}
	if !mentionsSettings {
		t.Error("Agents help no longer mentions how to edit settings.json (Enter on its row)")
	}
}
