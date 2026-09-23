package scaffold

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/project"
)

// hermeticHome redirects $HOME so nothing reads or writes real settings.
func hermeticHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	return dir
}

func TestPrepareDir_NameRequired(t *testing.T) {
	if _, err := PrepareDir(Options{}); err == nil {
		t.Fatal("expected error for empty Name+Dir, got nil")
	}
}

// TestPrepareDir_CreatesDirectoryOnly is the core contract of the
// scaffolding removal: PrepareDir makes the project directory and
// NOTHING else — no docs/ tree, no CLAUDE.md, no README.md, no
// .gitignore, no git repo. Bootstrapping a project is the user's job.
func TestPrepareDir_CreatesDirectoryOnly(t *testing.T) {
	hermeticHome(t)
	target := filepath.Join(t.TempDir(), "myproj")
	dir, err := PrepareDir(Options{Name: "myproj", Dir: target})
	if err != nil {
		t.Fatalf("PrepareDir: %v", err)
	}
	if dir != target {
		t.Errorf("PrepareDir returned %q, want %q", dir, target)
	}
	if fi, err := os.Stat(target); err != nil || !fi.IsDir() {
		t.Fatalf("project dir not created: %v", err)
	}
	// Nothing else may be written. These are exactly the files/dirs the
	// old scaffolder produced — their absence is the whole point.
	for _, forbidden := range []string{
		"CLAUDE.md", "README.md", ".gitignore", ".git",
		"docs", filepath.Join("docs", "01_Specs"),
		filepath.Join("docs", "02_Architecture"), filepath.Join("docs", "03_Agent_Logs"),
	} {
		if _, err := os.Stat(filepath.Join(target, forbidden)); err == nil {
			t.Errorf("PrepareDir created %q — project scaffolding should be fully removed", forbidden)
		}
	}
}

func TestPrepareDir_RelativeNameResolvesToCwd(t *testing.T) {
	hermeticHome(t)
	cwd := t.TempDir()
	t.Chdir(cwd)
	dir, err := PrepareDir(Options{Name: "relsub"})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(cwd, "relsub")
	if dir != want {
		t.Errorf("resolved %q, want %q", dir, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("did not create dir under cwd: %v", err)
	}
}

func TestPrepareDir_Idempotent(t *testing.T) {
	hermeticHome(t)
	target := filepath.Join(t.TempDir(), "p")
	if _, err := PrepareDir(Options{Name: "p", Dir: target}); err != nil {
		t.Fatal(err)
	}
	// A file the user put in the dir must survive a second PrepareDir.
	marker := filepath.Join(target, "user-file.txt")
	if err := os.WriteFile(marker, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareDir(Options{Name: "p", Dir: target}); err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(marker); string(body) != "mine" {
		t.Errorf("PrepareDir disturbed an existing file: %q", body)
	}
}

// TestPrepareDir_KeepsExistingProjectsAgent — `ccmux new <existing>
// --agent X` rewrote the project's .ccmux/agent sidecar, silently and
// permanently switching the project's agent. An existing sidecar is
// left alone; a directory with none yet gets the chosen agent.
func TestPrepareDir_KeepsExistingProjectsAgent(t *testing.T) {
	hermeticHome(t)
	target := filepath.Join(t.TempDir(), "p")
	if err := project.SetAgent(target, agent.IDCodex); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareDir(Options{Name: "p", Dir: target, Agent: agent.IDClaude}); err != nil {
		t.Fatal(err)
	}
	if got := project.ReadAgent(target); got != agent.IDCodex {
		t.Errorf("existing codex project switched to %q", got)
	}

	// Existing directory, no recorded agent yet: the choice is recorded.
	bare := filepath.Join(t.TempDir(), "existing-repo")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareDir(Options{Name: "existing-repo", Dir: bare, Agent: agent.IDCodex}); err != nil {
		t.Fatal(err)
	}
	if got := project.ReadAgent(bare); got != agent.IDCodex {
		t.Errorf("unrecorded dir: ReadAgent = %q, want codex", got)
	}
}

// fakeTmux swaps StartSession's tmux calls for the duration of a test.
func fakeTmux(t *testing.T, newErr error) *[]string {
	t.Helper()
	var tagged []string
	origNew, origTag := newSession, setSessionAgent
	t.Cleanup(func() { newSession, setSessionAgent = origNew, origTag })
	newSession = func(context.Context, string, string, string) error { return newErr }
	setSessionAgent = func(_ context.Context, session, id string) error {
		tagged = append(tagged, session+"="+id)
		return nil
	}
	return &tagged
}

// TestStartSession_FailedStartLeavesAgentUnchanged — a sidecar recorded
// for this start is rolled back when the tmux session can't be created,
// so a failed `ccmux new` doesn't re-assign the project's agent.
func TestStartSession_FailedStartLeavesAgentUnchanged(t *testing.T) {
	hermeticHome(t)
	fakeTmux(t, errors.New("tmux: server exited"))
	dir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := StartSession(context.Background(), Options{Name: "repo", Dir: dir, Agent: agent.IDCodex}); err == nil {
		t.Fatal("StartSession succeeded with a failing tmux")
	}
	if _, err := os.Stat(project.AgentSidecarPath(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("sidecar left behind after a failed start (err=%v); ReadAgent = %q", err, project.ReadAgent(dir))
	}

	// A pre-existing choice survives a failed start untouched.
	if err := project.SetAgent(dir, agent.IDKimi); err != nil {
		t.Fatal(err)
	}
	if _, err := StartSession(context.Background(), Options{Name: "repo", Dir: dir, Agent: agent.IDCodex}); err == nil {
		t.Fatal("StartSession succeeded with a failing tmux")
	}
	if got := project.ReadAgent(dir); got != agent.IDKimi {
		t.Errorf("ReadAgent after failed start = %q, want kimi", got)
	}
}

// TestStartSession_TagsSessionWhenAgentDiffersFromProject — the project
// keeps its recorded agent, so a session started with a different one
// is pinned to what it runs (tmux @ccmux_agent) for the daemon's
// classifier. A session running the project's own agent isn't tagged.
func TestStartSession_TagsSessionWhenAgentDiffersFromProject(t *testing.T) {
	hermeticHome(t)
	tagged := fakeTmux(t, nil)
	dir := filepath.Join(t.TempDir(), "proj")
	if err := project.SetAgent(dir, agent.IDCodex); err != nil {
		t.Fatal(err)
	}
	session, err := StartSession(context.Background(), Options{Name: "proj", Dir: dir, Agent: agent.IDClaude})
	if err != nil {
		t.Fatal(err)
	}
	if got := project.ReadAgent(dir); got != agent.IDCodex {
		t.Errorf("project agent switched to %q", got)
	}
	if want := []string{session + "=claude"}; len(*tagged) != 1 || (*tagged)[0] != want[0] {
		t.Errorf("session tags = %v, want %v", *tagged, want)
	}

	*tagged = nil
	fresh := filepath.Join(t.TempDir(), "fresh")
	if _, err := StartSession(context.Background(), Options{Name: "fresh", Dir: fresh, Agent: agent.IDCodex}); err != nil {
		t.Fatal(err)
	}
	if got := project.ReadAgent(fresh); got != agent.IDCodex {
		t.Errorf("new project ReadAgent = %q, want codex", got)
	}
	if len(*tagged) != 0 {
		t.Errorf("session running the project's own agent was tagged: %v", *tagged)
	}
}

// TestPrepareDir_WritesAgentSidecar — PrepareDir records the chosen
// agent in ccmux's own .ccmux/agent sidecar (infrastructure, not
// project scaffolding) so the dashboard and attach path launch the
// right agent. An empty or bogus agent leaves no sidecar, and
// project.ReadAgent then falls back to Claude.
func TestPrepareDir_WritesAgentSidecar(t *testing.T) {
	cases := []struct {
		name string
		in   agent.ID
		want agent.ID
	}{
		{"claude", agent.IDClaude, agent.IDClaude},
		{"codex", agent.IDCodex, agent.IDCodex},
		{"antigravity", agent.IDAntigravity, agent.IDAntigravity},
		{"empty falls back to claude", "", agent.IDClaude},
		{"unknown falls back to claude", agent.ID("imaginary"), agent.IDClaude},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hermeticHome(t)
			target := filepath.Join(t.TempDir(), "p")
			if _, err := PrepareDir(Options{Name: "p", Dir: target, Agent: tc.in}); err != nil {
				t.Fatal(err)
			}
			if got := project.ReadAgent(target); got != tc.want {
				t.Errorf("ReadAgent = %q, want %q", got, tc.want)
			}
		})
	}
}
