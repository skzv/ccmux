package tui

import (
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/conversations"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// TestApp_PreviewLoadedMsg_NilMessagesStopsLoading — regression:
// conversations.RecentMessages returns a nil slice for Antigravity
// (opaque protobuf) and for empty transcripts, and the overlay used a
// nil slice as its "still loading" signal, so those previews showed
// "(loading recent messages…)" forever. A completed load must always
// leave the loading placeholder, whatever the slice.
func TestApp_PreviewLoadedMsg_NilMessagesStopsLoading(t *testing.T) {
	cases := []struct {
		name string
		conv conversations.Conversation
		want string
	}{
		{"antigravity", conversations.Conversation{ID: "agy-1", Agent: agent.IDAntigravity}, "preview is unavailable"},
		{"empty claude transcript", conversations.Conversation{ID: "claude-empty", Agent: agent.IDClaude}, "No messages found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newAppForTest(t)
			a.convPreview.Open(tc.conv)
			if v := a.convPreview.View(styles.Default(), 120, 40); !strings.Contains(v, "loading recent messages") {
				t.Fatalf("freshly opened overlay should show the loading placeholder:\n%s", v)
			}

			m, _ := a.Update(conversationPreviewLoadedMsg{ID: tc.conv.ID, Messages: nil})
			a2 := m.(App)
			view := a2.convPreview.View(styles.Default(), 120, 40)
			if strings.Contains(view, "loading recent messages") {
				t.Fatalf("overlay still says loading after the load returned:\n%s", view)
			}
			if !strings.Contains(view, tc.want) {
				t.Errorf("overlay should explain the empty preview (%q):\n%s", tc.want, view)
			}
		})
	}
}

// TestPreviewOverlay_ReopenResetsLoaded — re-arming the overlay for a
// different conversation must go back to the loading placeholder, not
// keep the previous conversation's "loaded" state.
func TestPreviewOverlay_ReopenResetsLoaded(t *testing.T) {
	var o conversationPreviewOverlay
	o.Open(conversations.Conversation{ID: "a", Agent: agent.IDClaude})
	o.SetMessages("a", nil)
	o.Close()
	o.Open(conversations.Conversation{ID: "b", Agent: agent.IDClaude})
	if v := o.View(styles.Default(), 120, 40); !strings.Contains(v, "loading recent messages") {
		t.Errorf("re-opened overlay should be loading again:\n%s", v)
	}
}
