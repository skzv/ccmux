//go:build integration

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func (e *Env) writeCodexTranscript(uuid, cwd, prompt, timestamp string) string {
	e.t.Helper()
	ts, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		e.t.Fatalf("parse timestamp: %v", err)
	}
	name := "rollout-" + ts.Format("2006-01-02T15-04-05") + "-" + uuid + ".jsonl"
	line1 := map[string]any{
		"type":      "session_meta",
		"timestamp": timestamp,
		"cwd":       cwd,
		"payload":   map[string]any{"originator": "codex-tui", "source": "cli"},
	}
	line2 := map[string]any{
		"type":      "user_message",
		"timestamp": timestamp,
		"text":      prompt,
	}
	b1, err := json.Marshal(line1)
	if err != nil {
		e.t.Fatalf("marshal codex session_meta: %v", err)
	}
	b2, err := json.Marshal(line2)
	if err != nil {
		e.t.Fatalf("marshal codex user_message: %v", err)
	}
	path := filepath.Join(e.Home, ".codex", "sessions", ts.Format("2006"), ts.Format("01"), ts.Format("02"), name)
	writeFile(e.t, path, string(b1)+"\n"+string(b2)+"\n")
	_ = os.Chtimes(path, ts, ts)
	return path
}

func (e *Env) writeAntigravityTranscript(uuid, timestamp string) string {
	e.t.Helper()
	ts, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		e.t.Fatalf("parse timestamp: %v", err)
	}
	path := filepath.Join(e.Home, ".gemini", "antigravity-cli", "conversations", uuid+".pb")
	writeFile(e.t, path, "opaque")
	_ = os.Chtimes(path, ts, ts)
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

func TestTUIFlow_ConversationsGroupedByAgent(t *testing.T) {
	e := newEnv(t)
	cfg := e.defaultConfig()
	cfg.Tour.Shown = true
	cfg.Update.AutoCheck = false
	e.writeConfig(cfg)

	claudeProj := filepath.Join(e.Root, "claude-proj")
	codexProj := filepath.Join(e.Root, "codex-proj")
	mkdirAll(t, claudeProj)
	mkdirAll(t, codexProj)
	const claudeID = "claudeg0-1111-2222-3333-444444444444"
	const codexID = "codexg00-aaaa-bbbb-cccc-dddddddddddd"
	e.writeClaudeTranscript(claudeID, claudeProj, "claude grouped prompt", "2026-05-19T15:00:00Z")
	e.writeCodexTranscript(codexID, codexProj, "codex grouped prompt", "2026-05-19T16:00:00Z")
	e.writeAntigravityTranscript("agyg0000-aaaa-bbbb-cccc-dddddddddddd", "2026-05-19T17:00:00Z")

	d := newTUIDriver(t, e, 40, 140)
	d.WaitFor("Sessions")
	d.Send("3")
	for _, want := range []string{
		"Conversations",
		"Claude",
		"Codex",
		"Cursor",
		"Agy",
		"claude grouped prompt",
	} {
		d.WaitForTimeout(want, 8*time.Second)
	}

	d.Send(KeyTab)
	d.WaitForTimeout("codex grouped prompt", 8*time.Second)
	d.WaitForTimeout("codex-proj", 8*time.Second)
	d.Send(KeyTab)
	d.WaitForTimeout("No conversations for Cursor.", 8*time.Second)
	d.Send(KeyTab)
	d.WaitForTimeout("agy", 8*time.Second)
	d.Quit()
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
