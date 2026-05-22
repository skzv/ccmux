package scaffold

import (
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
