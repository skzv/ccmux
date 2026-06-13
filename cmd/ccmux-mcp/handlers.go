package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/skzv/ccmux/internal/daemon"
)

// handleListSessions returns daemon.Sessions verbatim. The result is
// already in a clean JSON shape the agent can consume directly.
func (s *Server) handleListSessions(ctx context.Context, _ json.RawMessage) (any, error) {
	out, err := s.client.Sessions(ctx)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	if out == nil {
		out = []daemon.SessionState{}
	}
	return out, nil
}

type readPaneArgs struct {
	Name  string `json:"name"`
	Lines int    `json:"lines,omitempty"`
}

func (s *Server) handleReadPane(ctx context.Context, raw json.RawMessage) (any, error) {
	var a readPaneArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, &invalidArgs{msg: "read_pane: " + err.Error()}
	}
	if a.Name == "" {
		return nil, &invalidArgs{msg: "read_pane: 'name' is required"}
	}
	if a.Lines < 0 {
		return nil, &invalidArgs{msg: "read_pane: 'lines' must be >= 0"}
	}
	if a.Lines > 500 {
		// Cap so a malicious or buggy agent can't drag down the
		// daemon by asking for the full scrollback every call.
		a.Lines = 500
	}
	out, err := s.client.Preview(ctx, a.Name, a.Lines)
	if err != nil {
		return nil, fmt.Errorf("read pane %q: %w", a.Name, err)
	}
	return out, nil
}

func (s *Server) handleListProjects(ctx context.Context, _ json.RawMessage) (any, error) {
	out, err := s.client.Projects(ctx)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	if out == nil {
		out = []daemon.ProjectInfo{}
	}
	return out, nil
}

func (s *Server) handleListConversations(ctx context.Context, _ json.RawMessage) (any, error) {
	out, err := s.client.Conversations(ctx)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	if out == nil {
		out = []daemon.Conversation{}
	}
	return out, nil
}

func (s *Server) handleGetUsage(ctx context.Context, _ json.RawMessage) (any, error) {
	out, err := s.client.Usage(ctx)
	if err != nil {
		return nil, fmt.Errorf("get usage: %w", err)
	}
	return out, nil
}

func (s *Server) handleListMachines(ctx context.Context, _ json.RawMessage) (any, error) {
	out, err := s.client.Peers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list machines: %w", err)
	}
	if out == nil {
		out = []daemon.PeerInfo{}
	}
	return out, nil
}

type projectArgs struct {
	Project string `json:"project"`
}

func (s *Server) handleListNotes(ctx context.Context, raw json.RawMessage) (any, error) {
	var a projectArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, &invalidArgs{msg: "list_notes: " + err.Error()}
	}
	if a.Project == "" {
		return nil, &invalidArgs{msg: "list_notes: 'project' is required"}
	}
	out, err := s.client.Notes(ctx, a.Project)
	if err != nil {
		return nil, fmt.Errorf("list notes %q: %w", a.Project, err)
	}
	if out == nil {
		out = []daemon.NoteEntry{}
	}
	return out, nil
}

type readNoteArgs struct {
	Project string `json:"project"`
	Path    string `json:"path"`
}

func (s *Server) handleReadNote(ctx context.Context, raw json.RawMessage) (any, error) {
	var a readNoteArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, &invalidArgs{msg: "read_note: " + err.Error()}
	}
	if a.Project == "" || a.Path == "" {
		return nil, &invalidArgs{msg: "read_note: 'project' and 'path' are required"}
	}
	out, err := s.client.NoteContent(ctx, a.Project, a.Path)
	if err != nil {
		return nil, fmt.Errorf("read note %q/%q: %w", a.Project, a.Path, err)
	}
	return out, nil
}

type searchNotesArgs struct {
	Project string `json:"project"`
	Query   string `json:"query"`
}

func (s *Server) handleSearchNotes(ctx context.Context, raw json.RawMessage) (any, error) {
	var a searchNotesArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, &invalidArgs{msg: "search_notes: " + err.Error()}
	}
	if a.Project == "" || a.Query == "" {
		return nil, &invalidArgs{msg: "search_notes: 'project' and 'query' are required"}
	}
	out, err := s.client.SearchNotes(ctx, a.Project, a.Query)
	if err != nil {
		return nil, fmt.Errorf("search notes %q (%q): %w", a.Project, a.Query, err)
	}
	if out == nil {
		out = []daemon.SearchHit{}
	}
	return out, nil
}

func (s *Server) handleGetHealth(ctx context.Context, _ json.RawMessage) (any, error) {
	out, err := s.client.Health(ctx)
	if err != nil {
		return nil, fmt.Errorf("daemon health: %w", err)
	}
	return out, nil
}

// --- mutating handlers (only registered when --allow-mutate) ---------

type spawnSessionArgs struct {
	Project  string `json:"project"`
	Path     string `json:"path,omitempty"`
	Agent    string `json:"agent,omitempty"`
	Continue bool   `json:"continue,omitempty"`
	Name     string `json:"name,omitempty"`
}

func (s *Server) handleSpawnSession(ctx context.Context, raw json.RawMessage) (any, error) {
	var a spawnSessionArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, &invalidArgs{msg: "spawn_session: " + err.Error()}
	}
	if a.Project == "" {
		return nil, &invalidArgs{msg: "spawn_session: 'project' is required"}
	}
	out, err := s.client.NewSession(ctx, daemon.NewSessionRequest{
		Project:  a.Project,
		Path:     a.Path,
		Agent:    a.Agent,
		Continue: a.Continue,
		Name:     a.Name,
	})
	if err != nil {
		return nil, fmt.Errorf("spawn session in %q: %w", a.Project, err)
	}
	return out, nil
}

type spawnBareArgs struct {
	Name  string `json:"name,omitempty"`
	Path  string `json:"path,omitempty"`
	Agent string `json:"agent,omitempty"`
}

func (s *Server) handleSpawnBareSession(ctx context.Context, raw json.RawMessage) (any, error) {
	var a spawnBareArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, &invalidArgs{msg: "spawn_bare_session: " + err.Error()}
	}
	out, err := s.client.NewBareSession(ctx, daemon.NewBareSessionRequest{
		Name:  a.Name,
		Path:  a.Path,
		Agent: a.Agent,
	})
	if err != nil {
		return nil, fmt.Errorf("spawn bare session: %w", err)
	}
	return out, nil
}

type sendKeysArgs struct {
	Name string `json:"name"`
	Keys string `json:"keys"`
}

func (s *Server) handleSendKeys(ctx context.Context, raw json.RawMessage) (any, error) {
	var a sendKeysArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, &invalidArgs{msg: "send_keys: " + err.Error()}
	}
	if a.Name == "" || a.Keys == "" {
		return nil, &invalidArgs{msg: "send_keys: 'name' and 'keys' are required"}
	}
	if err := s.client.SendKeys(ctx, a.Name, a.Keys); err != nil {
		return nil, fmt.Errorf("send keys to %q: %w", a.Name, err)
	}
	return map[string]string{"status": "sent", "name": a.Name}, nil
}

type killArgs struct {
	Name string `json:"name"`
}

func (s *Server) handleKillSession(ctx context.Context, raw json.RawMessage) (any, error) {
	var a killArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, &invalidArgs{msg: "kill_session: " + err.Error()}
	}
	if a.Name == "" {
		return nil, &invalidArgs{msg: "kill_session: 'name' is required"}
	}
	if err := s.client.Kill(ctx, a.Name); err != nil {
		return nil, fmt.Errorf("kill %q: %w", a.Name, err)
	}
	return map[string]string{"status": "killed", "name": a.Name}, nil
}
