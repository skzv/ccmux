package geminiconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func withFakeGeminiDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("GEMINI_HOME", dir)
	return dir
}

func TestPaths_HonorsEnvOverride(t *testing.T) {
	dir := withFakeGeminiDir(t)
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
}

func TestReadSettings_MissingFile(t *testing.T) {
	withFakeGeminiDir(t)
	s, err := ReadSettings()
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if s == nil || s.Extra == nil {
		t.Fatal("expected non-nil Settings with non-nil Extra")
	}
}

func TestReadSettings_UnknownFieldsPreservedInExtra(t *testing.T) {
	dir := withFakeGeminiDir(t)
	body := `{
  "model": "gemini-2.5-pro",
  "theme": "Default",
  "futureSetting": {"foo": 1}
}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.Model != "gemini-2.5-pro" {
		t.Errorf("Model = %q", s.Model)
	}
	if _, ok := s.Extra["theme"]; !ok {
		t.Error("theme was dropped")
	}
	if _, ok := s.Extra["futureSetting"]; !ok {
		t.Error("futureSetting was dropped")
	}
}

func TestWriteSettings_PreservesExtras(t *testing.T) {
	dir := withFakeGeminiDir(t)
	body := `{
  "model": "gemini-2.5-pro",
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
	s.Model = "gemini-3-pro"
	if _, err := WriteSettings(s); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip map[string]any
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if roundTrip["model"] != "gemini-3-pro" {
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
	dir := withFakeGeminiDir(t)
	settingsPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"model":"gemini-2.5-pro"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	s.Model = "gemini-3-pro"
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
}

func TestWriteSettings_NoBackupWhenSourceMissing(t *testing.T) {
	withFakeGeminiDir(t)
	s := &Settings{Model: "gemini-3-pro"}
	backup, err := WriteSettings(s)
	if err != nil {
		t.Fatal(err)
	}
	if backup != "" {
		t.Errorf("expected empty backup when source didn't exist, got %q", backup)
	}
}

func TestSetEffortLevel_RoundTrip(t *testing.T) {
	withFakeGeminiDir(t)
	if _, err := SetEffortLevel("high"); err != nil {
		t.Fatal(err)
	}
	s, err := ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.ReasoningEffort != "high" {
		t.Errorf("reasoningEffort = %q, want high", s.ReasoningEffort)
	}
}

func TestSetEffortLevel_PreservesUnrelatedFields(t *testing.T) {
	dir := withFakeGeminiDir(t)
	body := `{"theme":"Default","customKey":"keepMe","model":"gemini-2.5-pro"}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := SetEffortLevel("medium"); err != nil {
		t.Fatal(err)
	}
	s, err := ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.ReasoningEffort != "medium" || s.Model != "gemini-2.5-pro" {
		t.Errorf("got effort=%q model=%q", s.ReasoningEffort, s.Model)
	}
	if _, ok := s.Extra["customKey"]; !ok {
		t.Error("customKey dropped by SetEffortLevel")
	}
}

func TestSetEffortLevel_ClearOverride(t *testing.T) {
	withFakeGeminiDir(t)
	if _, err := SetEffortLevel("high"); err != nil {
		t.Fatal(err)
	}
	if _, err := SetEffortLevel(""); err != nil {
		t.Fatal(err)
	}
	p, _ := Paths()
	raw, err := os.ReadFile(p.Settings)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip map[string]any
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if _, ok := roundTrip["reasoningEffort"]; ok {
		t.Errorf("reasoningEffort should have been omitted after clear, file: %v", roundTrip)
	}
}

func TestEffectiveEffortLevel_Precedence(t *testing.T) {
	withFakeGeminiDir(t)
	if v, src := EffectiveEffortLevel(); v != "(default)" || src != "Gemini CLI default" {
		t.Errorf("default: got %q from %q", v, src)
	}
	if _, err := SetEffortLevel("medium"); err != nil {
		t.Fatal(err)
	}
	if v, src := EffectiveEffortLevel(); v != "medium" || src != "settings.json" {
		t.Errorf("settings: got %q from %q", v, src)
	}
}

func TestKnownEffortLevels_IncludesEachLevel(t *testing.T) {
	want := map[string]bool{"low": false, "medium": false, "high": false}
	for _, e := range KnownEffortLevels() {
		if _, ok := want[e.Value]; ok {
			want[e.Value] = true
		}
	}
	for v, seen := range want {
		if !seen {
			t.Errorf("KnownEffortLevels missing value %q", v)
		}
	}
}

func TestSetYoloMode_RoundTrip(t *testing.T) {
	withFakeGeminiDir(t)
	if _, err := SetYoloMode(true); err != nil {
		t.Fatal(err)
	}
	s, err := ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !s.Yolo {
		t.Error("yolo = false, want true")
	}
	if enabled, src := EffectiveYoloMode(); !enabled || src != "settings.json" {
		t.Errorf("EffectiveYoloMode = (%v, %q)", enabled, src)
	}
}

func TestSetYoloMode_DisableOmitsKey(t *testing.T) {
	withFakeGeminiDir(t)
	if _, err := SetYoloMode(true); err != nil {
		t.Fatal(err)
	}
	if _, err := SetYoloMode(false); err != nil {
		t.Fatal(err)
	}
	p, _ := Paths()
	raw, err := os.ReadFile(p.Settings)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip map[string]any
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if _, ok := roundTrip["yolo"]; ok {
		t.Errorf("yolo should be omitted when false, file: %v", roundTrip)
	}
}

func TestSetYoloMode_PreservesUnrelatedFields(t *testing.T) {
	dir := withFakeGeminiDir(t)
	body := `{"theme":"Default","customKey":"keepMe","model":"gemini-2.5-pro"}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := SetYoloMode(true); err != nil {
		t.Fatal(err)
	}
	s, err := ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !s.Yolo || s.Model != "gemini-2.5-pro" {
		t.Errorf("got yolo=%v model=%q", s.Yolo, s.Model)
	}
	if _, ok := s.Extra["customKey"]; !ok {
		t.Error("customKey dropped by SetYoloMode")
	}
}
