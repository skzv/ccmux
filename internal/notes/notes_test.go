package notes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func makeProject(t *testing.T) (project string, v Vault) {
	t.Helper()
	project = t.TempDir()
	v = Open(project)
	return
}

func TestSection_DirAndLabel(t *testing.T) {
	cases := []struct {
		s              Section
		wantDir, wantL string
	}{
		{SectionSpecs, "01_Specs", "Specs"},
		{SectionArchitecture, "02_Architecture", "Architecture"},
		{SectionAgentLogs, "03_Agent_Logs", "Agent Logs"},
		{SectionOther, "", "Other"},
	}
	for _, tc := range cases {
		if got := tc.s.Dir(); got != tc.wantDir {
			t.Errorf("%v.Dir() = %q, want %q", tc.s, got, tc.wantDir)
		}
		if got := tc.s.Label(); got != tc.wantL {
			t.Errorf("%v.Label() = %q, want %q", tc.s, got, tc.wantL)
		}
	}
}

func TestSlugify(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Auth Flow", "auth_flow"},
		{"Auth-Flow!", "auth_flow"},
		{"  many   spaces ", "many_spaces"},
		{"weird_chars_$&@", "weird_chars"},
		{"already_slugged", "already_slugged"},
		{"UPPER CASE", "upper_case"},
		{"", ""},
		{"!!!", ""},
		{"123 numbers ok", "123_numbers_ok"},
	}
	for _, tc := range cases {
		if got := slugify(tc.in); got != tc.want {
			t.Errorf("slugify(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
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
	}
	for _, tc := range cases {
		if got := displayFor(tc.in); got != tc.want {
			t.Errorf("displayFor(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNextNumberedID(t *testing.T) {
	dir := t.TempDir()
	if got := nextNumberedID(dir); got != 0 {
		t.Errorf("empty dir: %d, want 0", got)
	}
	for _, name := range []string{"00_alpha.md", "01_beta.md", "07_gamma.md", "ignored.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := nextNumberedID(dir); got != 8 {
		t.Errorf("nextNumberedID = %d, want 8", got)
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

func TestNewSpec_CreatesFileWithTemplate(t *testing.T) {
	_, v := makeProject(t)
	path, err := v.NewSpec("Auth Flow")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "00_auth_flow.md" {
		t.Errorf("filename = %s, want 00_auth_flow.md", filepath.Base(path))
	}
	body, _ := os.ReadFile(path)
	for _, must := range []string{"# Auth Flow", "## Problem", "## Approach", "status: draft"} {
		if !strings.Contains(string(body), must) {
			t.Errorf("spec body missing %q\n--- body ---\n%s", must, body)
		}
	}
}

func TestNewSpec_IncrementsID(t *testing.T) {
	_, v := makeProject(t)
	_, _ = v.NewSpec("A")
	_, _ = v.NewSpec("B")
	p3, _ := v.NewSpec("C")
	if filepath.Base(p3) != "02_c.md" {
		t.Errorf("third spec = %s, want 02_c.md", filepath.Base(p3))
	}
}

func TestNewSpec_RequiresTitle(t *testing.T) {
	_, v := makeProject(t)
	if _, err := v.NewSpec(""); err == nil {
		t.Fatal("expected error for empty title")
	}
	if _, err := v.NewSpec("   "); err == nil {
		t.Fatal("expected error for whitespace-only title")
	}
}

func TestNewADR_HasADRTemplate(t *testing.T) {
	_, v := makeProject(t)
	path, err := v.NewADR("Adopt Foo")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	for _, must := range []string{"# Adopt Foo", "## Status", "Proposed", "## Context", "## Decision", "## Consequences"} {
		if !strings.Contains(string(body), must) {
			t.Errorf("ADR body missing %q", must)
		}
	}
}

func TestNewAgentLog_CreatesTodaysFile(t *testing.T) {
	_, v := makeProject(t)
	path, created, err := v.NewAgentLog("")
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Error("first call should report created=true")
	}
	today := time.Now().Format("2006-01-02")
	if filepath.Base(path) != today+".md" {
		t.Errorf("filename = %s, want %s.md", filepath.Base(path), today)
	}
	body, _ := os.ReadFile(path)
	for _, must := range []string{"date: " + today, "# Agent Log — " + today} {
		if !strings.Contains(string(body), must) {
			t.Errorf("agent log missing %q\n--- body ---\n%s", must, body)
		}
	}
}

func TestNewAgentLog_AppendsSessionEntry(t *testing.T) {
	_, v := makeProject(t)
	path, _, _ := v.NewAgentLog("")
	_, _, err := v.NewAgentLog("c-foo")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), "Started session `c-foo`") {
		t.Errorf("session line not appended:\n%s", body)
	}
}

func TestNewAgentLog_SecondCallSameDayDoesNotRecreate(t *testing.T) {
	_, v := makeProject(t)
	_, created1, _ := v.NewAgentLog("")
	_, created2, _ := v.NewAgentLog("")
	if !created1 {
		t.Error("first call should report created=true")
	}
	if created2 {
		t.Error("second call should report created=false")
	}
}
