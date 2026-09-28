package conversations

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
)

// TestValidateResumeFolder_Missing — resuming a Claude or Codex
// conversation whose project folder has since been deleted or moved
// silently started the session in $HOME, where the agent can't find the
// conversation. ValidateResumeFolder must refuse with a clear error naming
// the folder; an existing folder (or no folder at all) is still fine.
func TestValidateResumeFolder_Missing(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "deleted-project")
	here := t.TempDir()
	for _, id := range []agent.ID{agent.IDClaude, agent.IDCodex, agent.IDPi} {
		c := Conversation{ID: "0f3c2a", Agent: id, Project: gone}
		err := c.ValidateResumeFolder()
		if err == nil {
			t.Errorf("%s: resume in a missing folder %q was allowed", id, gone)
			continue
		}
		if !strings.Contains(err.Error(), gone) || !strings.Contains(err.Error(), "no longer exists") {
			t.Errorf("%s: error %q doesn't say which folder is missing", id, err)
		}
		c.Project = here
		if err := c.ValidateResumeFolder(); err != nil {
			t.Errorf("%s: resume in an existing folder refused: %v", id, err)
		}
	}
	// A conversation with no recorded folder is left to the caller.
	if err := (Conversation{ID: "0f3c2a", Agent: agent.IDClaude}).ValidateResumeFolder(); err != nil {
		t.Errorf("no project folder: %v", err)
	}
}
