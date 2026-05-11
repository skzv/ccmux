package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if err := Scaffold(&Options{}); err == nil {
		t.Fatal("expected error for empty Name, got nil")
	}
}

func TestScaffold_CreatesDefaultLayout(t *testing.T) {
	hermeticHome(t)
	target := filepath.Join(t.TempDir(), "myproj")
	err := Scaffold(&Options{Name: "myproj", Dir: target, SkipGit: true})
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
	if err := Scaffold(&Options{Name: "p", Dir: target, SkipGit: true}); err != nil {
		t.Fatal(err)
	}
	// Modify README — re-running should NOT overwrite it.
	customREADME := "# CUSTOM\n"
	if err := os.WriteFile(filepath.Join(target, "README.md"), []byte(customREADME), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Scaffold(&Options{Name: "p", Dir: target, SkipGit: true}); err != nil {
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
	if err := Scaffold(&Options{Name: "relsub", SkipGit: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cwd, "relsub", "src")); err != nil {
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
	if err := writeIfMissing(path, "first"); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	if string(body) != "first" {
		t.Errorf("first write missed: %q", body)
	}
	// Second write is a no-op.
	if err := writeIfMissing(path, "second"); err != nil {
		t.Fatal(err)
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
