package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/project"
)

// fakeTmuxLog puts a `tmux` on PATH that succeeds at everything and
// logs each invocation as one line of "|"-terminated arguments, and
// gives the test a private HOME. Nothing reaches a real tmux server.
func fakeTmuxLog(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("sh-script tmux fake is unix-only")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TMUX", "")
	dir := t.TempDir()
	logPath := filepath.Join(dir, "tmux.log")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s|' \"$a\"; done >> '" + logPath + "'\necho >> '" + logPath + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return logPath
}

// tmuxCallsWith returns the logged tmux invocations containing substr.
func tmuxCallsWith(t *testing.T, logPath, substr string) []string {
	t.Helper()
	raw, err := os.ReadFile(logPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil // tmux never ran
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.Contains(line, substr) {
			out = append(out, line)
		}
	}
	return out
}

// assertTaggedAtCreation checks that the session's @ccmux_agent tag was
// set by the very tmux call that created it — a second call left a
// window in which a poll tick classified the untagged session with the
// wrong agent's rules.
func assertTaggedAtCreation(t *testing.T, logPath, session, tag string) {
	t.Helper()
	creates := tmuxCallsWith(t, logPath, "new-session|")
	if len(creates) != 1 {
		t.Fatalf("new-session calls = %q, want exactly one", creates)
	}
	want := "|;|set-option|-t|=" + session + ":|@ccmux_agent|" + tag + "|"
	if !strings.HasSuffix(creates[0], want) {
		t.Errorf("new-session call %q doesn't set the agent tag itself (want suffix %q)", creates[0], want)
	}
	if tags := tmuxCallsWith(t, logPath, "@ccmux_agent"); len(tags) != 1 {
		t.Errorf("tmux calls touching @ccmux_agent = %q, want only the new-session call", tags)
	}
}

// TestCreateBareSession_TagsInTheCreatingTmuxCall — a bare shell session
// was created untagged and tagged by a second tmux call; a poll tick in
// between judged the shell prompt by Claude's rules ("crashed").
func TestCreateBareSession_TagsInTheCreatingTmuxCall(t *testing.T) {
	logPath := fakeTmuxLog(t)
	s := &server{cfg: config.Config{}}
	s.has = func(context.Context, string) (bool, error) { return false, nil }

	body, _ := json.Marshal(daemon.NewBareSessionRequest{Name: "c-shell-t", Path: t.TempDir(), Agent: "shell"})
	rec := httptest.NewRecorder()
	s.createBareSession(rec, httptest.NewRequest(http.MethodPost, "/v1/sessions/bare", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	assertTaggedAtCreation(t, logPath, "c-shell-t", "shell")
}

// TestCreateProject_TagsInTheCreatingTmuxCall — same race for a project
// session running a different agent than the project records.
func TestCreateProject_TagsInTheCreatingTmuxCall(t *testing.T) {
	logPath := fakeTmuxLog(t)
	root := t.TempDir()
	dir := filepath.Join(root, "proj")
	if err := project.SetAgent(dir, agent.IDCodex); err != nil {
		t.Fatal(err)
	}
	s := &server{cfg: config.Config{Projects: config.ProjectsConfig{Root: root}}}

	body, _ := json.Marshal(daemon.NewProjectRequest{Name: "proj", Agent: "claude"})
	rec := httptest.NewRecorder()
	s.createProject(rec, httptest.NewRequest(http.MethodPost, "/v1/projects", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	assertTaggedAtCreation(t, logPath, "c-proj", "claude")
}
