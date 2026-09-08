package conversations

import (
	"strings"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/muse"
)

func ListMuse(home string) ([]Conversation, error) {
	sessions, err := muse.List(home)
	if err != nil {
		return nil, err
	}
	var out []Conversation
	for _, s := range sessions {
		preview := s.Title
		for _, m := range s.Messages {
			if m.Role == "user" {
				preview = m.Content
				break
			}
		}
		preview = strings.Join(strings.Fields(preview), " ")
		runes := []rune(preview)
		if len(runes) > 100 {
			preview = string(runes[:100]) + "…"
		}
		out = append(out, Conversation{ID: s.ID, Agent: agent.IDMuse, Project: s.Workspace, LastActivity: s.LastActivity, Preview: preview, Path: s.Path, Paths: s.Paths})
	}
	return out, nil
}
