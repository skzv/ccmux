package main

import (
	"net/http"

	"github.com/skzv/ccmux/internal/conversations"
	"github.com/skzv/ccmux/internal/daemon"
)

// handleConversations returns past agent transcripts (Claude, Codex,
// Cursor, Antigravity) from the daemon's home directory. Sorted most-recent
// first; clients can do their own filtering. Headless / SDK runs are
// excluded by default — they pile up fast in automation and aren't
// usually what a user means by "my conversations".
func (s *server) handleConversations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	convos, err := conversations.All(conversations.Options{ExcludeHeadless: true})
	if err != nil {
		// Best-effort: even if one walker failed, others may have
		// returned rows. conversations.All only errors when ALL walkers
		// failed, which usually means home dir resolution broke.
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]daemon.Conversation, 0, len(convos))
	for _, c := range convos {
		out = append(out, daemon.Conversation{
			ID: c.ID, Agent: string(c.Agent), Project: c.Project,
			Path: c.Path, Preview: c.Preview, Modified: c.LastActivity,
		})
	}
	writeJSON(w, out)
}
