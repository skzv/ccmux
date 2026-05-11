package claudeconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// contains is a tiny readable wrapper around strings.Contains so each
// assertion reads like English.
func contains(haystack, needle string) bool { return strings.Contains(haystack, needle) }

// withFakeClaudeDir points $CLAUDE_CONFIG_DIR at a tempdir so all
// Paths()/Read/Write traffic stays local.
func withFakeClaudeDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	return dir
}

func TestPaths_HonorsEnvOverride(t *testing.T) {
	dir := withFakeClaudeDir(t)
	got, err := Paths()
	if err != nil {
		t.Fatal(err)
	}
	if got.Root != dir {
		t.Errorf("Root = %q, want %q", got.Root, dir)
	}
	if got.Settings != filepath.Join(dir, "settings.json") {
		t.Errorf("Settings path wrong: %q", got.Settings)
	}
	if got.BackupsDir != filepath.Join(dir, "backups") {
		t.Errorf("BackupsDir wrong: %q", got.BackupsDir)
	}
}

func TestReadSettings_MissingFile(t *testing.T) {
	withFakeClaudeDir(t)
	s, err := ReadSettings()
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if s == nil || s.Extra == nil {
		t.Fatal("expected non-nil Settings with non-nil Extra")
	}
	if s.Model != "" {
		t.Errorf("missing file should give empty Model, got %q", s.Model)
	}
}

func TestReadSettings_UnknownFieldsPreservedInExtra(t *testing.T) {
	dir := withFakeClaudeDir(t)
	body := `{
  "model": "opus",
  "newFeatureWeDontKnowAbout": {"foo": 1},
  "anotherWildField": "bar"
}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.Model != "opus" {
		t.Errorf("Model = %q, want opus", s.Model)
	}
	if _, ok := s.Extra["newFeatureWeDontKnowAbout"]; !ok {
		t.Error("unknown field newFeatureWeDontKnowAbout was dropped")
	}
	if _, ok := s.Extra["anotherWildField"]; !ok {
		t.Error("unknown field anotherWildField was dropped")
	}
}

func TestWriteSettings_PreservesExtras(t *testing.T) {
	dir := withFakeClaudeDir(t)
	body := `{
  "model": "sonnet",
  "experimentalThing": {"k": "v", "n": 7}
}`
	settingsPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	s.Model = "opus" // mutate a known field
	if _, err := WriteSettings(s); err != nil {
		t.Fatal(err)
	}

	// Reparse the file as a raw map and confirm experimentalThing
	// survived.
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip map[string]any
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if roundTrip["model"] != "opus" {
		t.Errorf("model not updated: %v", roundTrip["model"])
	}
	exp, ok := roundTrip["experimentalThing"].(map[string]any)
	if !ok {
		t.Fatalf("experimentalThing dropped or mangled: %v", roundTrip["experimentalThing"])
	}
	if exp["k"] != "v" || exp["n"].(float64) != 7 {
		t.Errorf("experimentalThing content changed: %v", exp)
	}
}

func TestWriteSettings_CreatesBackup(t *testing.T) {
	dir := withFakeClaudeDir(t)
	settingsPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"model":"sonnet"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	s.Model = "opus"
	backup, err := WriteSettings(s)
	if err != nil {
		t.Fatal(err)
	}
	if backup == "" {
		t.Fatal("expected backup path, got empty string")
	}
	if _, err := os.Stat(backup); err != nil {
		t.Errorf("backup file missing: %v", err)
	}
	body, _ := os.ReadFile(backup)
	if string(body) != `{"model":"sonnet"}` {
		t.Errorf("backup content = %q, want original settings", string(body))
	}
}

func TestWriteSettings_NoBackupWhenSourceMissing(t *testing.T) {
	withFakeClaudeDir(t)
	s := &Settings{Model: "opus"}
	backup, err := WriteSettings(s)
	if err != nil {
		t.Fatal(err)
	}
	if backup != "" {
		t.Errorf("expected empty backup when source didn't exist, got %q", backup)
	}
}

func TestSetModel_RoundTrip(t *testing.T) {
	withFakeClaudeDir(t)
	if _, err := SetModel("opus"); err != nil {
		t.Fatal(err)
	}
	s, err := ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.Model != "opus" {
		t.Errorf("model = %q, want opus", s.Model)
	}
}

func TestSetModel_PreservesUnrelatedFields(t *testing.T) {
	dir := withFakeClaudeDir(t)
	body := `{"theme":"dark","customKey":"keepMe"}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := SetModel("haiku"); err != nil {
		t.Fatal(err)
	}
	s, err := ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.Model != "haiku" || s.Theme != "dark" {
		t.Errorf("got model=%q theme=%q", s.Model, s.Theme)
	}
	if _, ok := s.Extra["customKey"]; !ok {
		t.Error("customKey dropped by SetModel")
	}
}

func TestEffectiveModel_Precedence(t *testing.T) {
	withFakeClaudeDir(t)
	t.Setenv("ANTHROPIC_MODEL", "")

	// Nothing set anywhere → built-in default.
	if v, src := EffectiveModel(); v != "(default)" || src != "Claude Code default" {
		t.Errorf("default: got %q from %q", v, src)
	}

	// settings.json only.
	if _, err := SetModel("opus"); err != nil {
		t.Fatal(err)
	}
	if v, src := EffectiveModel(); v != "opus" || src != "settings.json" {
		t.Errorf("settings-only: got %q from %q", v, src)
	}

	// Env var wins.
	t.Setenv("ANTHROPIC_MODEL", "sonnet")
	if v, src := EffectiveModel(); v != "sonnet" || src != "$ANTHROPIC_MODEL" {
		t.Errorf("env override: got %q from %q", v, src)
	}
}

func TestListCommands(t *testing.T) {
	dir := withFakeClaudeDir(t)
	cmds := filepath.Join(dir, "commands")
	if err := os.MkdirAll(cmds, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cmds, "review.md"), []byte("# review\n\nReview a PR\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cmds, "ship.md"), []byte("# ship\n\nShip a PR\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Non-md file should be ignored.
	if err := os.WriteFile(filepath.Join(cmds, "notes.txt"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ListCommands()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "review" || got[1].Name != "ship" {
		t.Fatalf("ListCommands returned %v", got)
	}
	if got[0].Description != "Review a PR" {
		t.Errorf("description not picked: %q", got[0].Description)
	}
}

func TestListCommands_MissingDirIsEmpty(t *testing.T) {
	withFakeClaudeDir(t)
	got, err := ListCommands()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty list, got %v", got)
	}
}

func TestListSkills(t *testing.T) {
	dir := withFakeClaudeDir(t)
	skillsRoot := filepath.Join(dir, "skills")
	if err := os.MkdirAll(filepath.Join(skillsRoot, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsRoot, "alpha", "SKILL.md"), []byte("# alpha\n\nThe alpha skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// beta has no SKILL.md → should be skipped.
	if err := os.MkdirAll(filepath.Join(skillsRoot, "beta"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := ListSkills()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "alpha" {
		t.Fatalf("ListSkills returned %v", got)
	}
}

func TestFirstDescriptionLine(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "x.md")
	if err := os.WriteFile(f, []byte("# title\n\n\nThe actual description here\nMore detail\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := firstDescriptionLine(f); got != "The actual description here" {
		t.Errorf("got %q", got)
	}

	// Truncated when over 100 chars.
	long := "x"
	for i := 0; i < 150; i++ {
		long += "y"
	}
	if err := os.WriteFile(f, []byte("# t\n\n"+long+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := firstDescriptionLine(f)
	// The implementation cuts to 100 bytes then appends "…" (3 bytes).
	if len([]rune(got)) > 101 {
		t.Errorf("description not truncated: rune-length=%d (%q)", len([]rune(got)), got)
	}
	if !contains(got, "…") {
		t.Errorf("expected ellipsis on truncated line, got %q", got)
	}
}

func TestKnownModels_IncludesEachAlias(t *testing.T) {
	want := map[string]bool{"opus": false, "sonnet": false, "haiku": false, "opusplan": false}
	for _, m := range KnownModels() {
		if _, ok := want[m.Alias]; ok {
			want[m.Alias] = true
		}
	}
	for alias, seen := range want {
		if !seen {
			t.Errorf("KnownModels missing alias %q", alias)
		}
	}
}
