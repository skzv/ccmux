package tui

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLocalNoteContentCmd_ReadsOffUIGoroutine — the local note preview
// read was moved off the Bubble Tea UI goroutine (it was synchronous,
// blocking the render loop on large or slow-disk files). It now returns
// a tea.Cmd delivering the same notesPreviewLoadedMsg the remote path
// uses. This verifies the cmd reads the right file and tags the message
// with the path/rel the staleness guard checks against.
func TestLocalNoteContentCmd_ReadsOffUIGoroutine(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	const body = "# Title\n\nbody text\n"
	if err := os.WriteFile(filepath.Join(dir, "docs", "x.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := localNoteContentCmd(dir, "docs/x.md")
	if cmd == nil {
		t.Fatal("localNoteContentCmd returned nil")
	}
	msg, ok := cmd().(notesPreviewLoadedMsg)
	if !ok {
		t.Fatalf("expected notesPreviewLoadedMsg, got %T", cmd())
	}
	if msg.Err != "" {
		t.Fatalf("unexpected error: %s", msg.Err)
	}
	if msg.Content != body {
		t.Errorf("content = %q, want %q", msg.Content, body)
	}
	// The staleness guard in the Update handler keys on Path + Rel.
	if msg.Path != dir || msg.Rel != "docs/x.md" {
		t.Errorf("msg path/rel = %q/%q, want %q/docs/x.md", msg.Path, msg.Rel, dir)
	}
}

// TestLocalNoteContentCmd_MissingFileReportsError — a read failure must
// come back as an Err on the message (rendered as an error in the
// preview), not a crash or empty content.
func TestLocalNoteContentCmd_MissingFileReportsError(t *testing.T) {
	dir := t.TempDir()
	msg := localNoteContentCmd(dir, "docs/nope.md")().(notesPreviewLoadedMsg)
	if msg.Err == "" {
		t.Error("expected an error for a missing file, got none")
	}
	if msg.Content != "" {
		t.Errorf("missing-file message should carry no content, got %q", msg.Content)
	}
}
