//go:build integration

package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestProjectDiscovery covers the discovery CUJ: every non-hidden
// directory under the projects root surfaces as a project (no marker
// required). Hidden directories and regular files are still excluded.
// The HasGit/HasCM flags are populated when those markers are present
// so the TUI can render them as visual tags.
func TestProjectDiscovery(t *testing.T) {
	e := newEnv(t)
	writeFile(t, filepath.Join(e.Root, "withcm", "CLAUDE.md"), "# withcm\n")
	mkdirAll(t, filepath.Join(e.Root, "withgit", ".git"))
	mkdirAll(t, filepath.Join(e.Root, "plaindir"))
	writeFile(t, filepath.Join(e.Root, "plaindir", "notes.txt"), "not a marker file")
	writeFile(t, filepath.Join(e.Root, ".hidden", "CLAUDE.md"), "# hidden\n")
	writeFile(t, filepath.Join(e.Root, "loose.txt"), "not a directory")

	e.startDaemon()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	projects, err := e.localClient().Projects(ctx)
	if err != nil {
		t.Fatalf("daemon Projects: %v", err)
	}

	got := map[string]bool{}
	flags := map[string]struct{ git, cm bool }{}
	for _, p := range projects {
		got[p.Name] = true
		flags[p.Name] = struct{ git, cm bool }{p.HasGit, p.HasCM}
	}
	for _, want := range []string{"withcm", "withgit", "plaindir"} {
		if !got[want] {
			t.Errorf("directory %q was not surfaced", want)
		}
	}
	for _, notWant := range []string{".hidden", "loose.txt"} {
		if got[notWant] {
			t.Errorf("%q should not appear in the project list", notWant)
		}
	}
	if f := flags["withcm"]; !f.cm || f.git {
		t.Errorf("withcm flags: %+v, want cm=true git=false", f)
	}
	if f := flags["withgit"]; !f.git || f.cm {
		t.Errorf("withgit flags: %+v, want git=true cm=false", f)
	}
	if f := flags["plaindir"]; f.cm || f.git {
		t.Errorf("plaindir flags: %+v, want both false", f)
	}
}

// TestProjectNew covers `ccmux new`: it creates the project directory
// and starts a tmux session — and creates NOTHING else. No docs/ tree,
// no CLAUDE.md, no README.md, no .gitignore, no git repo: project
// scaffolding has been removed. `ccmux new` execs `tmux attach` last,
// which fails without a tty — tolerated; the directory + session
// happen first.
func TestProjectNew(t *testing.T) {
	e := newEnv(t)
	_, _, _ = e.ccmuxIn(e.Root, "new", "freshproj")

	dir := filepath.Join(e.Root, "freshproj")
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Fatalf("`ccmux new` did not create the project directory: %v", err)
	}
	// Scaffolding is removed — none of these may exist.
	for _, forbidden := range []string{
		"docs", filepath.Join("docs", "01_Specs"),
		filepath.Join("docs", "02_Architecture"), filepath.Join("docs", "03_Agent_Logs"),
		"README.md", ".gitignore", "CLAUDE.md", ".git",
	} {
		if exists(filepath.Join(dir, forbidden)) {
			t.Errorf("`ccmux new` created %q — project scaffolding should be gone", forbidden)
		}
	}
	if !e.hasSession("c-freshproj") {
		t.Errorf("`ccmux new` did not start session c-freshproj")
	}
}

// TestProjectAttach_NoDuplicate covers the attach-or-create CUJ from
// the CLI side: attaching twice to the same project rejoins the
// existing session rather than spawning a duplicate.
func TestProjectAttach_NoDuplicate(t *testing.T) {
	e := newEnv(t)
	proj := filepath.Join(e.Root, "dupcheck")
	mkdirAll(t, proj)

	// First attach creates c-dupcheck; second finds it and rejoins.
	// Both exec `tmux attach` (fails w/o a tty — tolerated).
	_, _, _ = e.ccmux("attach", proj)
	_, _, _ = e.ccmux("attach", proj)

	names := e.sessionNames()
	if len(names) != 1 || names[0] != "c-dupcheck" {
		t.Errorf("attach-or-create spawned extra sessions: %v (want exactly [c-dupcheck])", names)
	}
}

// TestProjectCmd_ListsSessionsAndConversations covers `ccmux project
// <name>` — the CLI mirror of the TUI project menu. It must report the
// project's running tmux sessions and its past conversations for that
// folder.
func TestProjectCmd_ListsSessionsAndConversations(t *testing.T) {
	e := newEnv(t)
	proj := filepath.Join(e.Root, "projcmd")
	writeFile(t, filepath.Join(proj, "CLAUDE.md"), "# projcmd\n")
	// A running tmux session whose working directory is the project.
	e.newTmuxSession("c-projcmd", proj)
	// A past Claude conversation recorded against the project folder.
	e.writeClaudeTranscript(
		"projcmd0-1111-2222-3333-444444444444", proj,
		"an older prompt about projcmd", "2026-05-19T10:00:00Z")

	stdout, stderr, err := e.ccmux("project", "projcmd")
	if err != nil {
		t.Fatalf("ccmux project: %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stdout, "c-projcmd") {
		t.Errorf("`ccmux project` output missing the running session c-projcmd:\n%s", stdout)
	}
	if !strings.Contains(stdout, "an older prompt about projcmd") {
		t.Errorf("`ccmux project` output missing the past conversation:\n%s", stdout)
	}
}
