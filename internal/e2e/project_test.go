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

// TestProjectDiscovery covers the discovery CUJ: a directory is a
// project iff it has a CLAUDE.md or a .git; hidden directories and
// directories with neither marker are excluded.
func TestProjectDiscovery(t *testing.T) {
	e := newEnv(t)
	writeFile(t, filepath.Join(e.Root, "withcm", "CLAUDE.md"), "# withcm\n")
	mkdirAll(t, filepath.Join(e.Root, "withgit", ".git"))
	writeFile(t, filepath.Join(e.Root, "plaindir", "notes.txt"), "not a project")
	writeFile(t, filepath.Join(e.Root, ".hidden", "CLAUDE.md"), "# hidden\n")

	e.startDaemon()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	projects, err := e.localClient().Projects(ctx)
	if err != nil {
		t.Fatalf("daemon Projects: %v", err)
	}

	got := map[string]bool{}
	for _, p := range projects {
		got[p.Name] = true
	}
	for _, want := range []string{"withcm", "withgit"} {
		if !got[want] {
			t.Errorf("project %q was not discovered", want)
		}
	}
	for _, notWant := range []string{"plaindir", ".hidden"} {
		if got[notWant] {
			t.Errorf("non-project %q was wrongly discovered", notWant)
		}
	}
}

// TestProjectScaffold_New covers `ccmux new`: it scaffolds the project
// directory (docs tree, README, .gitignore, agent sidecar) and starts
// a tmux session. `ccmux new` execs `tmux attach` last, which fails
// without a tty — tolerated; the scaffold + session happen first.
func TestProjectScaffold_New(t *testing.T) {
	e := newEnv(t)
	_, _, _ = e.ccmuxIn(e.Root, "new", "freshproj")

	dir := filepath.Join(e.Root, "freshproj")
	for _, sub := range []string{"docs/01_Specs", "docs/02_Architecture", "docs/03_Agent_Logs"} {
		if fi, err := os.Stat(filepath.Join(dir, sub)); err != nil || !fi.IsDir() {
			t.Errorf("scaffold directory %q is missing", sub)
		}
	}
	for _, f := range []string{"README.md", ".gitignore", ".ccmux/agent"} {
		if !exists(filepath.Join(dir, f)) {
			t.Errorf("scaffold file %q is missing", f)
		}
	}
	if !e.hasSession("c-freshproj") {
		t.Errorf("`ccmux new` did not start session c-freshproj")
	}
}

// TestProjectUpgrade_Idempotent covers `ccmux upgrade`: it injects the
// ccmux structure non-destructively (existing files untouched) and is
// idempotent (a second run reports no changes).
func TestProjectUpgrade_Idempotent(t *testing.T) {
	e := newEnv(t)
	dir := filepath.Join(e.Root, "legacy")
	mkdirAll(t, filepath.Join(dir, ".git"))
	writeFile(t, filepath.Join(dir, "README.md"), "# legacy — keep me\n")

	out1, stderr1, err := e.ccmuxIn(dir, "upgrade")
	if err != nil {
		t.Fatalf("ccmux upgrade (run 1): %v\nstderr: %s", err, stderr1)
	}
	if !strings.Contains(out1, "docs/01_Specs") {
		t.Errorf("first upgrade did not report creating the docs tree:\n%s", out1)
	}
	if body := readFile(t, filepath.Join(dir, "README.md")); !strings.Contains(body, "keep me") {
		t.Errorf("upgrade overwrote the pre-existing README.md (got %q)", body)
	}

	out2, stderr2, err := e.ccmuxIn(dir, "upgrade")
	if err != nil {
		t.Fatalf("ccmux upgrade (run 2): %v\nstderr: %s", err, stderr2)
	}
	if !strings.Contains(out2, "Already up to date") {
		t.Errorf("second upgrade was not a clean no-op:\n%s", out2)
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
