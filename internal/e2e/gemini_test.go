//go:build integration

package e2e

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/agent"
)

func TestGeminiResumeUsesNativeIDAndProject(t *testing.T) {
	e := newEnv(t)
	project := filepath.Join(e.Root, "gemini-project")
	mkdirAll(t, project)
	writeFile(t, filepath.Join(project, ".ccmux", "agent"), "gemini\n")
	dir := filepath.Join(e.Home, ".gemini", "tmp", "gemini-project")
	writeFile(t, filepath.Join(dir, ".project_root"), project)
	const id = "gemini01-1111-2222-3333-444444444444"
	writeFile(t, filepath.Join(dir, "chats", "session-one.json"), `{"sessionId":"`+id+`","projectHash":"hash","messages":[{"type":"user","content":"restore Gemini"}]}`)
	list := e.listConversationsJSON()
	if len(list) != 1 || list[0].Agent != agent.IDGemini || list[0].Project != project {
		t.Fatal(list)
	}
	_, _, _ = e.ccmux("resume", id) // final attach has no tty, as in other CLI tests
	session := "c-resume-" + id[:8]
	if !e.hasSession(session) {
		t.Fatal("Gemini resume session was not created")
	}
	if !waitFor(5*time.Second, func() bool {
		pane := e.capturePane(session)
		return strings.Contains(pane, "ccmux-stub-agent=gemini") && strings.Contains(pane, "ccmux-stub-args=--resume "+id)
	}) {
		t.Fatal(e.capturePane(session))
	}
	cwd, err := e.tmux("display-message", "-p", "-t", session, "#{pane_current_path}")
	wantCWD, resolveErr := filepath.EvalSymlinks(project)
	if err != nil || resolveErr != nil || strings.TrimSpace(cwd) != wantCWD {
		t.Fatal(cwd, err)
	}
	e.assertChromed(session)
}
