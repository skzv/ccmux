package notes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func makeProject(t *testing.T) (project string, v Vault) {
	t.Helper()
	project = t.TempDir()
	v = Open(project)
	return
}

func TestDirOf(t *testing.T) {
	cases := []struct{ in, want string }{
		{"README.md", ""},
		{"CLAUDE.md", ""},
		{"docs/01_Specs/00_Vision.md", "docs/01_Specs"},
		{"openspec/specs/spec.md", "openspec/specs"},
	}
	for _, tc := range cases {
		if got := dirOf(tc.in); got != tc.want {
			t.Errorf("dirOf(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSkipDir(t *testing.T) {
	// Pruned: version control, dependency, and build-output trees.
	for _, d := range []string{".git", ".obsidian", ".ccmux", "node_modules", "vendor", "dist", "build", "target"} {
		if !skipDir(d) {
			t.Errorf("skipDir(%q) = false, want true", d)
		}
	}
	// Kept: the project's own source and docs directories.
	for _, d := range []string{"docs", "openspec", "internal", "src", "cmd"} {
		if skipDir(d) {
			t.Errorf("skipDir(%q) = true, want false", d)
		}
	}
}

func TestDisplayFor(t *testing.T) {
	cases := []struct{ in, want string }{
		{"01_Specs/00_Vision.md", "Vision"},
		{"01_Specs/01_Feature_Catalog.md", "Feature Catalog"},
		{"02_Architecture/00_System_Design.md", "System Design"},
		{"03_Agent_Logs/2026-05-11.md", "2026-05-11"},
		{"misc/no-numbers.md", "no-numbers"},
		{"README.md", "README"},
	}
	for _, tc := range cases {
		if got := displayFor(tc.in); got != tc.want {
			t.Errorf("displayFor(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestList_EmptyVault(t *testing.T) {
	_, v := makeProject(t)
	got, err := v.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty list, got %v", got)
	}
}

func TestList_GroupsByFolder(t *testing.T) {
	project, v := makeProject(t)
	// Markdown spread across the project — root level, docs/, openspec/
	// — plus noise (a hidden dir, a dependency tree, a non-md file)
	// that List must prune.
	files := map[string]string{
		"README.md":                        "# r",
		"CLAUDE.md":                        "# c",
		"docs/01_Specs/00_Vision.md":       "# v",
		"docs/03_Agent_Logs/2026-05-11.md": "# log",
		"docs/03_Agent_Logs/2026-05-10.md": "# older log",
		"openspec/specs/spec.md":           "# s",
		"node_modules/dep/README.md":       "# vendored",
		".obsidian/workspace.md":           "# hidden",
		"docs/notes.txt":                   "not markdown",
	}
	for rel, body := range files {
		full := filepath.Join(project, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, err := v.List()
	if err != nil {
		t.Fatal(err)
	}

	// Pruned trees and non-markdown files never appear.
	for _, e := range got {
		if strings.Contains(e.Rel, "node_modules") {
			t.Errorf("dependency dir leaked: %s", e.Rel)
		}
		if strings.Contains(e.Rel, ".obsidian") {
			t.Errorf("hidden dir leaked: %s", e.Rel)
		}
		if !strings.HasSuffix(e.Rel, ".md") {
			t.Errorf("non-markdown leaked: %s", e.Rel)
		}
	}

	// 6 markdown files survive the prune (README, CLAUDE, Vision, 2 logs, spec).
	if len(got) != 6 {
		t.Fatalf("List() = %d entries, want 6:\n%+v", len(got), got)
	}

	// Root-level files (Dir == "") sort first.
	if got[0].Dir != "" || got[1].Dir != "" {
		t.Errorf("root-level files should sort first, got dirs %q, %q", got[0].Dir, got[1].Dir)
	}

	// Entries are ordered by containing directory.
	for i := 1; i < len(got); i++ {
		if got[i].Dir < got[i-1].Dir {
			t.Errorf("folder ordering broken at %d: %q before %q", i, got[i-1].Dir, got[i].Dir)
		}
	}

	// Within an Agent Logs folder, newest-first (filename is the date).
	var logs []Entry
	for _, e := range got {
		if e.Dir == "docs/03_Agent_Logs" {
			logs = append(logs, e)
		}
	}
	if len(logs) != 2 {
		t.Fatalf("expected 2 agent logs, got %d", len(logs))
	}
	if logs[0].Rel < logs[1].Rel {
		t.Errorf("agent logs not newest-first: %v", logs)
	}
}

func TestRead(t *testing.T) {
	project, v := makeProject(t)
	// A nested path exercises the slash → filepath conversion: the TUI
	// passes vault-relative paths like "docs/01_Specs/00_Vision.md".
	nested := filepath.Join(project, "docs", "01_Specs", "x.md")
	if err := os.MkdirAll(filepath.Dir(nested), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nested, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	body, err := v.Read("docs/01_Specs/x.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "hello" {
		t.Errorf("Read = %q", body)
	}
}
