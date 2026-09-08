package tui

import (
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/conversations"
)

func TestConversationSearchCapturesKeysAndDisarmsDeletion(t *testing.T) {
	a := New(config.Defaults(), "test")
	a.screen = ScreenConversations
	a.tour.Close()
	a.conversationsM.SetList([]conversations.Conversation{
		{ID: "muse-1", Agent: agent.IDMuse, Project: "/projects/demo", Preview: "Русский 123 xq?TMi"},
		{ID: "muse-2", Agent: agent.IDMuse, Project: "/projects/other", Preview: "unrelated"},
	})
	setFocusedConversationSection(t, &a.conversationsM, agent.IDMuse)
	a.conversationsM.pendingDelete = "muse-1"
	a, _ = updateApp(t, a, keyMsg("/"))
	for _, k := range []string{"1", "2", "3", " ", "x", "q", "?", "T", "M", "i"} {
		a, _ = updateApp(t, a, keyMsg(k))
	}
	if a.screen != ScreenConversations || a.helpOpen || a.tour.Active() || a.matrix.Active() {
		t.Fatal("query triggered a global shortcut")
	}
	if got := a.conversationsM.search.Value(); got != "123 xq?TMi" {
		t.Fatal(got)
	}
	if a.conversationsM.pendingDelete != "" {
		t.Fatal("search retained armed deletion")
	}
	if got := a.conversationsM.Selected(); got == nil || got.ID != "muse-1" {
		t.Fatal(got)
	}
	a, _ = updateApp(t, a, keyMsg("enter"))
	if a.conversationsM.searchActive || a.conversationsM.search.Value() == "" {
		t.Fatal("Enter did not commit query")
	}
	a, _ = updateApp(t, a, keyMsg("esc"))
	if a.conversationsM.search.Value() != "" || len(a.conversationsM.filtered()) != 2 {
		t.Fatal("Escape did not restore rows")
	}
	a, _ = updateApp(t, a, keyMsg("/"))
	a.conversationsM.search.SetValue("РУССКИЙ")
	if got := a.conversationsM.Selected(); got == nil || !strings.Contains(got.Preview, "Русский") {
		t.Fatal("Unicode search failed", got)
	}
}
