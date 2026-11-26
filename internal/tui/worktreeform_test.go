package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tui/styles"
)

func TestWorktreeForm_ValidationAndSubmit(t *testing.T) {
	st := styles.Default()
	p := project.Project{Name: "ccmux", Path: "/Users/demo/Projects/ccmux", HasGit: true, Agent: agent.IDCodex}
	f := newWorktreeForm(st, p, agent.Commands{})

	// Invalid branch keeps form open with error.
	f.branch.SetValue("../bad")
	f, cmd := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || f.err == "" {
		t.Fatalf("expected inline error for invalid branch, got err=%q cmd=%v", f.err, cmd)
	}

	// Valid branch emits newWorktreeSubmitMsg.
	f.branch.SetValue("feat/parallel")
	f, cmd = f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected submit cmd")
	}
	msg, ok := cmd().(newWorktreeSubmitMsg)
	if !ok {
		t.Fatalf("got %T, want newWorktreeSubmitMsg", msg)
	}
	if msg.Project != "ccmux" || msg.Branch != "feat/parallel" {
		t.Fatalf("unexpected submit payload: %+v", msg)
	}
}

func TestProjects_WKeyOpensWorktreeModalForGitProject(t *testing.T) {
	st := styles.Default()
	m := newProjects(st, DefaultKeymap())
	m.SetProjects([]project.Project{
		{Name: "repo", Path: "/tmp/repo", HasGit: true, Agent: agent.IDClaude},
		{Name: "notes-only", Path: "/tmp/notes-only", HasGit: false, Agent: agent.IDClaude},
	})

	// On git project: opens worktree modal.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	if m.worktreeForm == nil || !m.capturesInput() {
		t.Fatal("pressing w on a git project should open worktreeForm and capture input")
	}

	// Esc dismisses it.
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		m, _ = m.Update(cmd())
	}
	if m.worktreeForm != nil {
		t.Fatal("esc should dismiss worktreeForm")
	}

	// On non-git project: does not open modal, emits warning toast.
	m.cursor = 1
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	if m.worktreeForm != nil {
		t.Fatal("non-git project should not open worktreeForm")
	}
	if cmd == nil {
		t.Fatal("expected toast cmd for non-git project")
	}
	if toast, ok := cmd().(toastMsg); !ok || toast.Kind != toastWarning {
		t.Fatalf("expected warning toast, got %+v", toast)
	}
}

func TestRenderSessionLine_ShowsWorktreeBadge(t *testing.T) {
	st := styles.Default()
	row := renderSessionLine(st, daemon.SessionState{
		Name:  "c-ccmux-feat-auth",
		Host:  "local",
		State: "active",
		Path:  "/Users/demo/Projects/ccmux/.worktrees/feat-auth",
	}, 90)
	if !strings.Contains(row, "⎇ feat-auth") {
		t.Errorf("worktree session row missing branch badge: %q", row)
	}
}
