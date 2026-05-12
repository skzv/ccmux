package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/project"
)

// hermeticHome redirects $HOME so config.Load() reads no real settings.
func hermeticHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	return dir
}

func TestScaffold_NameRequired(t *testing.T) {
	hermeticHome(t)
	if _, err := Scaffold(&Options{}); err == nil {
		t.Fatal("expected error for empty Name, got nil")
	}
}

func TestScaffold_CreatesDefaultLayout(t *testing.T) {
	hermeticHome(t)
	target := filepath.Join(t.TempDir(), "myproj")
	_, err := Scaffold(&Options{Name: "myproj", Dir: target, SkipGit: true})
	if err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	for _, want := range DefaultDirs {
		p := filepath.Join(target, want)
		if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
			t.Errorf("missing dir %s (err=%v)", p, err)
		}
	}
	// README + .gitignore must exist.
	for _, name := range []string{"README.md", ".gitignore"} {
		if _, err := os.Stat(filepath.Join(target, name)); err != nil {
			t.Errorf("missing file %s: %v", name, err)
		}
	}
	// Crucially: NO CLAUDE.md gets pre-written (the doc comment explains why).
	if _, err := os.Stat(filepath.Join(target, "CLAUDE.md")); err == nil {
		t.Error("Scaffold should NOT pre-write CLAUDE.md (lets /init create it cleanly)")
	}
	// SkipGit=true means no .git dir.
	if _, err := os.Stat(filepath.Join(target, ".git")); err == nil {
		t.Error("SkipGit=true should leave .git untouched/absent")
	}
}

func TestScaffold_Idempotent(t *testing.T) {
	hermeticHome(t)
	target := filepath.Join(t.TempDir(), "p")

	// Run once.
	if _, err := Scaffold(&Options{Name: "p", Dir: target, SkipGit: true}); err != nil {
		t.Fatal(err)
	}
	// Modify README — re-running should NOT overwrite it.
	customREADME := "# CUSTOM\n"
	if err := os.WriteFile(filepath.Join(target, "README.md"), []byte(customREADME), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Scaffold(&Options{Name: "p", Dir: target, SkipGit: true}); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(target, "README.md"))
	if string(body) != customREADME {
		t.Errorf("README was overwritten: %q", body)
	}
}

func TestScaffold_RelativeNameResolvedToAbsoluteCwd(t *testing.T) {
	hermeticHome(t)
	// Use a temp cwd so the resolved abs path lands somewhere we own.
	cwd := t.TempDir()
	t.Chdir(cwd)
	if _, err := Scaffold(&Options{Name: "relsub", SkipGit: true}); err != nil {
		t.Fatal(err)
	}
	// Use the first DefaultDirs entry as the marker — robust against
	// future tweaks to the default layout.
	if _, err := os.Stat(filepath.Join(cwd, "relsub", DefaultDirs[0])); err != nil {
		t.Errorf("did not scaffold under cwd: %v", err)
	}
}

func TestInitialPrompt_SubstitutesPlaceholders(t *testing.T) {
	hermeticHome(t)
	got := InitialPrompt(Options{Name: "alpha", Description: "build the alpha"})
	if !strings.Contains(got, "alpha") {
		t.Errorf("name not substituted: %s", got)
	}
	if !strings.Contains(got, "build the alpha") {
		t.Errorf("description not substituted: %s", got)
	}
	// Mustache placeholders should be fully resolved.
	if strings.Contains(got, "{{name}}") || strings.Contains(got, "{{description}}") {
		t.Errorf("placeholders not all replaced: %s", got)
	}
}

func TestInitialPrompt_EmptyDescriptionGetsSafeDefault(t *testing.T) {
	hermeticHome(t)
	got := InitialPrompt(Options{Name: "x"})
	if !strings.Contains(got, "no description yet") {
		t.Errorf("missing fallback hint for empty description: %s", got)
	}
}

func TestWriteIfMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")

	// First write creates the file.
	created, err := writeIfMissing(path, "first")
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Error("first write should report created=true")
	}
	body, _ := os.ReadFile(path)
	if string(body) != "first" {
		t.Errorf("first write missed: %q", body)
	}
	// Second write is a no-op.
	created, err = writeIfMissing(path, "second")
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Error("second call should report created=false")
	}
	body, _ = os.ReadFile(path)
	if string(body) != "first" {
		t.Errorf("second call overwrote: %q", body)
	}
}

func TestDefaultGitignore_HasObvious(t *testing.T) {
	for _, must := range []string{".DS_Store", "node_modules/", ".venv/"} {
		if !strings.Contains(DefaultGitignore, must) {
			t.Errorf("DefaultGitignore missing %q", must)
		}
	}
}

func TestDefaultInitialPrompt_MentionsKeyPieces(t *testing.T) {
	// Doc behavior contract: the default prompt should ask Claude to
	// run /init and reference the scaffold layout. Loose substring
	// asserts so we don't trip on minor wording tweaks.
	mustContain := []string{"/init", "CLAUDE.md", "docs/01_Specs", "docs/02_Architecture", "docs/03_Agent_Logs"}
	for _, s := range mustContain {
		if !strings.Contains(DefaultInitialPrompt, s) {
			t.Errorf("DefaultInitialPrompt missing %q", s)
		}
	}
}

// TestDefaultInitialPrompt_NoGitHubPush — the default flow keeps things
// local; pushing to GitHub is a user-initiated step. If anyone later
// re-adds a `gh repo create` line to the default, this test forces a
// conversation about whether that's the right default.
func TestDefaultInitialPrompt_NoGitHubPush(t *testing.T) {
	forbidden := []string{"gh repo create", "GitHub repo", "push the initial commit"}
	for _, s := range forbidden {
		if strings.Contains(DefaultInitialPrompt, s) {
			t.Errorf("DefaultInitialPrompt should stay local-only, but contains %q", s)
		}
	}
}

// TestScaffold_UpgradeEmptyDir verifies that running scaffold against a
// completely empty existing directory (the "upgrade an existing project"
// path) reports every default dir + both files as freshly created. This
// is the case where `ccmux upgrade` should obviously do work — if this
// regresses to silence, the friend-reported "doesn't do anything" bug is
// back.
func TestScaffold_UpgradeEmptyDir(t *testing.T) {
	hermeticHome(t)
	target := t.TempDir() // already exists, but empty
	res, err := Scaffold(&Options{Name: filepath.Base(target), Dir: target, SkipGit: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed() {
		t.Fatalf("upgrade on empty dir should report changes; got %+v", res)
	}
	if len(res.CreatedDirs) != len(DefaultDirs) {
		t.Errorf("expected %d CreatedDirs, got %d: %v", len(DefaultDirs), len(res.CreatedDirs), res.CreatedDirs)
	}
	if len(res.SkippedDirs) != 0 {
		t.Errorf("expected 0 SkippedDirs, got %v", res.SkippedDirs)
	}
	wantFiles := map[string]bool{"README.md": false, ".gitignore": false}
	for _, f := range res.CreatedFiles {
		wantFiles[f] = true
	}
	for f, seen := range wantFiles {
		if !seen {
			t.Errorf("expected %s in CreatedFiles, got %v", f, res.CreatedFiles)
		}
	}
	if res.GitInit {
		t.Errorf("SkipGit=true should leave GitInit=false")
	}
}

// TestScaffold_UpgradeFullyScaffolded is the case that originally
// looked broken: every dir + file already exists, so a re-run is a
// pure no-op. The result should report no changes and Summary() should
// be the "already up to date" sentinel — that's what lets the CLI and
// TUI tell the user "nothing to do" instead of staying silent.
func TestScaffold_UpgradeFullyScaffolded(t *testing.T) {
	hermeticHome(t)
	target := filepath.Join(t.TempDir(), "p")

	// First run scaffolds everything.
	if _, err := Scaffold(&Options{Name: "p", Dir: target, SkipGit: true}); err != nil {
		t.Fatal(err)
	}
	// Second run on the same dir must be a clean no-op.
	res, err := Scaffold(&Options{Name: "p", Dir: target, SkipGit: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed() {
		t.Fatalf("idempotent re-run should not Change(); got %+v", res)
	}
	if len(res.CreatedDirs) != 0 || len(res.CreatedFiles) != 0 {
		t.Errorf("idempotent re-run created things: dirs=%v files=%v", res.CreatedDirs, res.CreatedFiles)
	}
	if len(res.SkippedDirs) != len(DefaultDirs) {
		t.Errorf("expected all %d dirs Skipped, got %v", len(DefaultDirs), res.SkippedDirs)
	}
	if got, want := res.Summary(), "already up to date"; got != want {
		t.Errorf("Summary on no-op = %q, want %q", got, want)
	}
}

// TestScaffold_UpgradePartial is the realistic upgrade-an-old-project
// case: dirs are missing but a README the user wrote themselves is
// already in place. We expect dirs to be Created, README to be Skipped
// (preserving the user's content), .gitignore to be Created.
func TestScaffold_UpgradePartial(t *testing.T) {
	hermeticHome(t)
	target := t.TempDir()
	// Pre-existing README — must survive untouched.
	existingREADME := "# my pre-existing project\n"
	if err := os.WriteFile(filepath.Join(target, "README.md"), []byte(existingREADME), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Scaffold(&Options{Name: filepath.Base(target), Dir: target, SkipGit: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed() {
		t.Fatal("partial upgrade should Change()")
	}
	// README must be in SkippedFiles, NOT CreatedFiles.
	for _, f := range res.CreatedFiles {
		if f == "README.md" {
			t.Errorf("Scaffold overwrote pre-existing README")
		}
	}
	foundSkipped := false
	for _, f := range res.SkippedFiles {
		if f == "README.md" {
			foundSkipped = true
		}
	}
	if !foundSkipped {
		t.Errorf("README.md not in SkippedFiles: %v", res.SkippedFiles)
	}
	body, _ := os.ReadFile(filepath.Join(target, "README.md"))
	if string(body) != existingREADME {
		t.Errorf("pre-existing README clobbered: got %q want %q", body, existingREADME)
	}
}

// TestResult_Summary exercises the human-readable Summary() string the
// CLI and TUI both depend on for upgrade reporting.
func TestResult_Summary(t *testing.T) {
	cases := []struct {
		name string
		in   Result
		want string
	}{
		{"empty no-op", Result{}, "already up to date"},
		{"one dir created", Result{CreatedDirs: []string{"docs/01_Specs"}}, "added 1 dir"},
		{"two dirs created", Result{CreatedDirs: []string{"a", "b"}}, "added 2 dirs"},
		{"only files", Result{CreatedFiles: []string{"README.md", ".gitignore"}}, "added README.md, .gitignore"},
		{"dirs + files + git", Result{
			CreatedDirs:  []string{"a"},
			CreatedFiles: []string{"README.md"},
			GitInit:      true,
		}, "added 1 dir, README.md, git init"},
		{"git only", Result{GitInit: true}, "added git init"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.in.Summary()
			if got != tc.want {
				t.Errorf("Summary = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestResult_Changed pins the predicate the toast and CLI use to decide
// "no-op" vs "did work". GitInit alone counts as a change.
func TestResult_Changed(t *testing.T) {
	cases := []struct {
		name string
		in   Result
		want bool
	}{
		{"empty", Result{}, false},
		{"only skipped", Result{SkippedDirs: []string{"a"}, SkippedFiles: []string{"README.md"}}, false},
		{"created dir", Result{CreatedDirs: []string{"a"}}, true},
		{"created file", Result{CreatedFiles: []string{"README.md"}}, true},
		{"git init alone", Result{GitInit: true}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.Changed(); got != tc.want {
				t.Errorf("Changed = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestScaffold_WritesAgentSidecar — Scaffold must persist the chosen
// agent so the daemon's poll loop, attach path, and dashboard can all
// read it back. The sidecar contract is `<dir>/.ccmux/agent` carrying
// the agent ID, validated via project.ReadAgent.
func TestScaffold_WritesAgentSidecar(t *testing.T) {
	cases := []struct {
		name string
		in   agent.ID
		want agent.ID
	}{
		{"claude explicit", agent.IDClaude, agent.IDClaude},
		{"codex", agent.IDCodex, agent.IDCodex},
		{"gemini", agent.IDGemini, agent.IDGemini},
		// Back-compat: callers that haven't migrated still pass "" —
		// Scaffold defaults to claude.
		{"empty defaults to claude", "", agent.IDClaude},
		// Invalid input must not write garbage — Scaffold should
		// fall back to claude (the safe default) instead of
		// erroring or persisting "imaginary".
		{"unknown defaults to claude", agent.ID("imaginary"), agent.IDClaude},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hermeticHome(t)
			target := filepath.Join(t.TempDir(), "p")
			if _, err := Scaffold(&Options{
				Name:    "p",
				Dir:     target,
				SkipGit: true,
				Agent:   tc.in,
			}); err != nil {
				t.Fatal(err)
			}
			if got := project.ReadAgent(target); got != tc.want {
				t.Errorf("sidecar = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestScaffold_PreservesExistingAgentOnUpgrade — `ccmux upgrade` runs
// Scaffold(Options{SkipGit: true, Agent: ""}). A user who previously
// chose Codex for a project must NOT have their choice silently
// flipped to Claude by an upgrade-style re-run. The contract is:
// empty Agent + existing sidecar = leave the sidecar alone.
func TestScaffold_PreservesExistingAgentOnUpgrade(t *testing.T) {
	hermeticHome(t)
	target := filepath.Join(t.TempDir(), "p")

	// First pass: scaffold with Codex.
	if _, err := Scaffold(&Options{
		Name: "p", Dir: target, SkipGit: true, Agent: agent.IDCodex,
	}); err != nil {
		t.Fatal(err)
	}
	if got := project.ReadAgent(target); got != agent.IDCodex {
		t.Fatalf("setup: got %q after first scaffold, want codex", got)
	}

	// Second pass: simulate `ccmux upgrade` (no Agent specified).
	if _, err := Scaffold(&Options{
		Name: "p", Dir: target, SkipGit: true, // Agent left zero
	}); err != nil {
		t.Fatal(err)
	}
	if got := project.ReadAgent(target); got != agent.IDCodex {
		t.Errorf("upgrade clobbered agent: got %q, want codex preserved", got)
	}
}

// TestScaffold_OverwritesAgentWhenExplicit — the inverse of the
// preserve case. When the caller DOES specify an Agent (e.g. user
// picked a new one in the new-project form, or daemon got it from
// the POST body), Scaffold must persist that choice over any
// pre-existing sidecar. Without this, switching the agent from the
// "new project" form would silently fail for upgrade-style re-creates.
func TestScaffold_OverwritesAgentWhenExplicit(t *testing.T) {
	hermeticHome(t)
	target := filepath.Join(t.TempDir(), "p")

	// Seed with Claude.
	if _, err := Scaffold(&Options{
		Name: "p", Dir: target, SkipGit: true, Agent: agent.IDClaude,
	}); err != nil {
		t.Fatal(err)
	}
	// Now scaffold again, this time choosing Gemini.
	if _, err := Scaffold(&Options{
		Name: "p", Dir: target, SkipGit: true, Agent: agent.IDGemini,
	}); err != nil {
		t.Fatal(err)
	}
	if got := project.ReadAgent(target); got != agent.IDGemini {
		t.Errorf("explicit overwrite failed: got %q, want gemini", got)
	}
}
