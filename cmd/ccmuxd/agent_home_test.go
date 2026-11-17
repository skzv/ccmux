package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/daemon"
)

// moveClaudeHome leaves the transcripts writeClaudeTranscripts wrote
// where they are but makes them reachable only through
// CLAUDE_CONFIG_DIR: the variable names the old ~/.claude and HOME
// moves to an empty directory. Returns the Claude root.
func moveClaudeHome(t *testing.T) string {
	t.Helper()
	root := filepath.Join(os.Getenv("HOME"), ".claude")
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	t.Setenv("HOME", t.TempDir())
	return root
}

// TestHandleUsage_HonoursClaudeConfigDir — ccmux daemon install now
// hands CLAUDE_CONFIG_DIR to the service (#246), but /v1/usage still
// walked ~/.claude/projects, so a user whose Claude Code lives in a
// custom config dir saw zero usage on the phone card and in
// `ccmux usage`. Both the rolling summary and the session block read
// the custom root.
func TestHandleUsage_HonoursClaudeConfigDir(t *testing.T) {
	writeClaudeTranscripts(t, []time.Time{time.Now().Add(-20 * time.Minute)})
	moveClaudeHome(t)

	_, got := getUsage(t, "")
	if !got.Claude.HasData || got.Claude.InputTokens != 10 || got.Claude.OutputTokens != 2 {
		t.Errorf("claude summary %+v, want the CLAUDE_CONFIG_DIR transcript's 10 in / 2 out", got.Claude)
	}
	if b := got.ClaudeBlock; b == nil || !b.Active || b.Prompts != 1 {
		t.Errorf("claude_block %+v, want an active block holding the one prompt", b)
	}
}

// TestHandleConversations_HonoursClaudeConfigDir — /v1/conversations
// (what ccmux-mcp's list_conversations and remote clients read) listed
// only ~/.claude/projects. It now lists the transcripts Claude Code
// wrote under CLAUDE_CONFIG_DIR.
func TestHandleConversations_HonoursClaudeConfigDir(t *testing.T) {
	writeClaudeTranscripts(t, []time.Time{time.Now().Add(-time.Hour)})
	root := moveClaudeHome(t)

	s := &server{}
	w := httptest.NewRecorder()
	s.handleConversations(w, httptest.NewRequest(http.MethodGet, "/v1/conversations", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /v1/conversations: %d %s", w.Code, w.Body.String())
	}
	var out []daemon.Conversation
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body.String())
	}
	want := filepath.Join(root, "projects", "-Users-me-Projects-demo", "s.jsonl")
	for _, c := range out {
		if c.Agent == "claude" && c.Path == want {
			return
		}
	}
	t.Errorf("no claude conversation at %s; got %+v", want, out)
}
