//go:build integration

package e2e

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/conversations"
)

// claudeProjectDir returns the encoded ~/.claude/projects subdirectory
// name for a working directory, matching Claude Code's own encoding
// (leading "/" → "-", every "/" → "-").
func claudeProjectDir(cwd string) string {
	return "-" + strings.ReplaceAll(strings.TrimPrefix(cwd, "/"), "/", "-")
}

// writeClaudeTranscript drops a minimal one-line Claude .jsonl
// transcript fixture into the sandbox's ~/.claude/projects tree.
func (e *Env) writeClaudeTranscript(uuid, cwd, prompt, timestamp string) string {
	e.t.Helper()
	line := map[string]any{
		"type":      "user",
		"cwd":       cwd,
		"timestamp": timestamp,
		"message":   map[string]any{"role": "user", "content": prompt},
	}
	b, err := json.Marshal(line)
	if err != nil {
		e.t.Fatalf("marshal transcript: %v", err)
	}
	path := filepath.Join(e.Home, ".claude", "projects", claudeProjectDir(cwd), uuid+".jsonl")
	writeFile(e.t, path, string(b)+"\n")
	return path
}

// listConversationsJSON runs `ccmux list-conversations --json` and
// parses its JSON-lines output.
func (e *Env) listConversationsJSON() []conversations.Conversation {
	e.t.Helper()
	stdout, stderr, err := e.ccmux("list-conversations", "--json")
	if err != nil {
		e.t.Fatalf("ccmux list-conversations --json: %v\nstderr: %s", err, stderr)
	}
	var list []conversations.Conversation
	for _, ln := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if ln == "" {
			continue
		}
		var c conversations.Conversation
		if err := json.Unmarshal([]byte(ln), &c); err != nil {
			e.t.Fatalf("parse conversation line %q: %v", ln, err)
		}
		list = append(list, c)
	}
	return list
}

// TestConversations_List covers the list CUJ: past transcripts are
// reported, sorted newest-first, with their project.
func TestConversations_List(t *testing.T) {
	e := newEnv(t)
	proj := filepath.Join(e.Root, "conv-proj")
	mkdirAll(t, proj)

	const idOld = "old00000-1111-2222-3333-444444444444"
	const idNew = "new00000-aaaa-bbbb-cccc-dddddddddddd"
	e.writeClaudeTranscript(idOld, proj, "first prompt about auth", "2026-05-18T09:00:00Z")
	e.writeClaudeTranscript(idNew, proj, "newer prompt about storage", "2026-05-19T15:00:00Z")

	list := e.listConversationsJSON()
	if len(list) != 2 {
		t.Fatalf("expected 2 conversations, got %d: %+v", len(list), list)
	}
	if list[0].ID != idNew {
		t.Errorf("first conversation = %q, want newest %q", list[0].ID, idNew)
	}
	if list[0].Project != proj {
		t.Errorf("conversation project = %q, want %q", list[0].Project, proj)
	}
}

// TestConversations_Resume covers the resume CUJ: `ccmux resume <id>`
// creates a tmux session for that conversation. The command execs
// `tmux attach` last (fails w/o a tty — tolerated); the session is
// created first.
func TestConversations_Resume(t *testing.T) {
	e := newEnv(t)
	proj := filepath.Join(e.Root, "conv-proj")
	mkdirAll(t, proj)
	const id = "resume00-aaaa-bbbb-cccc-dddddddddddd"
	e.writeClaudeTranscript(id, proj, "resume me", "2026-05-19T15:00:00Z")

	_, _, _ = e.ccmux("resume", id)

	want := "c-resume-" + id[:8]
	if !e.hasSession(want) {
		t.Errorf("`ccmux resume %s` did not create session %q (sessions: %v)", id, want, e.sessionNames())
	}
}

// TestConversations_Delete covers the delete CUJ: `ccmux delete-
// conversation` removes the transcript from disk and from the listing.
func TestConversations_Delete(t *testing.T) {
	e := newEnv(t)
	proj := filepath.Join(e.Root, "conv-proj")
	mkdirAll(t, proj)
	const id = "delete00-aaaa-bbbb-cccc-dddddddddddd"
	path := e.writeClaudeTranscript(id, proj, "delete me", "2026-05-19T15:00:00Z")

	if _, stderr, err := e.ccmux("delete-conversation", id, "--force"); err != nil {
		t.Fatalf("ccmux delete-conversation: %v\nstderr: %s", err, stderr)
	}
	if exists(path) {
		t.Errorf("transcript %q still on disk after delete", path)
	}
	for _, c := range e.listConversationsJSON() {
		if c.ID == id {
			t.Errorf("deleted conversation %q still listed", id)
		}
	}
}
