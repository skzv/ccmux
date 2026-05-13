package codexconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// withFakeCodexDir points $CODEX_HOME at a tempdir so all
// Paths()/Read/Write traffic stays local.
func withFakeCodexDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	return dir
}

func TestPaths_HonorsCodexHomeEnv(t *testing.T) {
	dir := withFakeCodexDir(t)
	got, err := Paths()
	if err != nil {
		t.Fatal(err)
	}
	if got.Root != dir {
		t.Errorf("Root = %q, want %q", got.Root, dir)
	}
	if got.Config != filepath.Join(dir, "config.toml") {
		t.Errorf("Config path wrong: %q", got.Config)
	}
	if got.BackupsDir != filepath.Join(dir, "backups") {
		t.Errorf("BackupsDir wrong: %q", got.BackupsDir)
	}
}

func TestReadSettings_MissingFile(t *testing.T) {
	withFakeCodexDir(t)
	s, err := ReadSettings()
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if s == nil || s.Extra == nil {
		t.Fatal("expected non-nil Settings with non-nil Extra")
	}
	if s.Model != "" || s.ModelReasoningEffort != "" {
		t.Errorf("missing file should give empty Settings, got %+v", s)
	}
}

func TestReadSettings_UnknownKeysPreservedInExtra(t *testing.T) {
	dir := withFakeCodexDir(t)
	body := `model = "gpt-5"
model_reasoning_effort = "high"
some_future_setting = "keep me"

[profiles.work]
model = "gpt-5-pro"
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.Model != "gpt-5" {
		t.Errorf("Model = %q, want gpt-5", s.Model)
	}
	if s.ModelReasoningEffort != "high" {
		t.Errorf("ModelReasoningEffort = %q, want high", s.ModelReasoningEffort)
	}
	if _, ok := s.Extra["some_future_setting"]; !ok {
		t.Error("some_future_setting was dropped")
	}
	if _, ok := s.Extra["profiles"]; !ok {
		t.Error("profiles table was dropped from Extra")
	}
}

func TestWriteSettings_PreservesExtras(t *testing.T) {
	dir := withFakeCodexDir(t)
	body := `model = "gpt-5"
custom_key = "keepme"

[experiment]
flag = true
count = 7
`
	configPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(configPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	s.Model = "gpt-5-codex"
	if _, err := WriteSettings(s); err != nil {
		t.Fatal(err)
	}

	// Re-decode and confirm survivors.
	var roundTrip map[string]any
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := toml.Decode(string(raw), &roundTrip); err != nil {
		t.Fatal(err)
	}
	if roundTrip["model"] != "gpt-5-codex" {
		t.Errorf("model not updated: %v", roundTrip["model"])
	}
	if roundTrip["custom_key"] != "keepme" {
		t.Errorf("custom_key dropped: %v", roundTrip["custom_key"])
	}
	exp, ok := roundTrip["experiment"].(map[string]any)
	if !ok {
		t.Fatalf("experiment table dropped or mangled: %v", roundTrip["experiment"])
	}
	if exp["flag"] != true || exp["count"].(int64) != 7 {
		t.Errorf("experiment contents changed: %v", exp)
	}
}

func TestWriteSettings_CreatesBackup(t *testing.T) {
	dir := withFakeCodexDir(t)
	configPath := filepath.Join(dir, "config.toml")
	original := "model = \"gpt-5\"\n"
	if err := os.WriteFile(configPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	s.Model = "gpt-5-codex"
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
	if string(body) != original {
		t.Errorf("backup content = %q, want %q", string(body), original)
	}
}

func TestWriteSettings_NoBackupWhenSourceMissing(t *testing.T) {
	withFakeCodexDir(t)
	s := &Settings{Model: "gpt-5", Extra: map[string]any{}}
	backup, err := WriteSettings(s)
	if err != nil {
		t.Fatal(err)
	}
	if backup != "" {
		t.Errorf("expected empty backup when source didn't exist, got %q", backup)
	}
}

func TestSetEffortLevel_RoundTrip(t *testing.T) {
	withFakeCodexDir(t)
	if _, err := SetEffortLevel("high"); err != nil {
		t.Fatal(err)
	}
	s, err := ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.ModelReasoningEffort != "high" {
		t.Errorf("model_reasoning_effort = %q, want high", s.ModelReasoningEffort)
	}
}

func TestSetEffortLevel_PreservesUnrelatedFields(t *testing.T) {
	dir := withFakeCodexDir(t)
	body := `model = "gpt-5"
custom_key = "keepme"
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := SetEffortLevel("medium"); err != nil {
		t.Fatal(err)
	}
	s, err := ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.ModelReasoningEffort != "medium" || s.Model != "gpt-5" {
		t.Errorf("got effort=%q model=%q", s.ModelReasoningEffort, s.Model)
	}
	if _, ok := s.Extra["custom_key"]; !ok {
		t.Error("custom_key dropped by SetEffortLevel")
	}
}

func TestSetEffortLevel_ClearOverride(t *testing.T) {
	dir := withFakeCodexDir(t)
	if _, err := SetEffortLevel("high"); err != nil {
		t.Fatal(err)
	}
	if _, err := SetEffortLevel(""); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "model_reasoning_effort") {
		t.Errorf("model_reasoning_effort should have been omitted after clear, file:\n%s", raw)
	}
}

func TestEffectiveEffortLevel_Precedence(t *testing.T) {
	withFakeCodexDir(t)
	if v, src := EffectiveEffortLevel(); v != "(default)" || src != "Codex default" {
		t.Errorf("default: got %q from %q", v, src)
	}
	if _, err := SetEffortLevel("medium"); err != nil {
		t.Fatal(err)
	}
	if v, src := EffectiveEffortLevel(); v != "medium" || src != "config.toml" {
		t.Errorf("settings: got %q from %q", v, src)
	}
}

func TestKnownEffortLevels_IncludesEachLevel(t *testing.T) {
	want := map[string]bool{"minimal": false, "low": false, "medium": false, "high": false}
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
	withFakeCodexDir(t)
	if _, err := SetYoloMode(true); err != nil {
		t.Fatal(err)
	}
	s, err := ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.ApprovalPolicy != YoloApprovalPolicy {
		t.Errorf("approval_policy = %q, want %q", s.ApprovalPolicy, YoloApprovalPolicy)
	}
	if s.SandboxMode != YoloSandboxMode {
		t.Errorf("sandbox_mode = %q, want %q", s.SandboxMode, YoloSandboxMode)
	}
	if enabled, src := EffectiveYoloMode(); !enabled || src != "config.toml" {
		t.Errorf("EffectiveYoloMode = (%v, %q)", enabled, src)
	}
}

func TestSetYoloMode_DisableLeavesNonYoloValues(t *testing.T) {
	dir := withFakeCodexDir(t)
	// User-set values that are NOT our YOLO sentinels — disable must
	// not touch them.
	body := `approval_policy = "on-failure"
sandbox_mode = "workspace-write"
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := SetYoloMode(false); err != nil {
		t.Fatal(err)
	}
	s, err := ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.ApprovalPolicy != "on-failure" {
		t.Errorf("approval_policy was clobbered: %q", s.ApprovalPolicy)
	}
	if s.SandboxMode != "workspace-write" {
		t.Errorf("sandbox_mode was clobbered: %q", s.SandboxMode)
	}
}

func TestSetYoloMode_DisableClearsOurSentinels(t *testing.T) {
	withFakeCodexDir(t)
	if _, err := SetYoloMode(true); err != nil {
		t.Fatal(err)
	}
	if _, err := SetYoloMode(false); err != nil {
		t.Fatal(err)
	}
	s, err := ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.ApprovalPolicy != "" || s.SandboxMode != "" {
		t.Errorf("YOLO sentinels not cleared: approval=%q sandbox=%q",
			s.ApprovalPolicy, s.SandboxMode)
	}
}

func TestEffectiveYoloMode_RequiresBothFields(t *testing.T) {
	dir := withFakeCodexDir(t)
	// Only approval_policy set — not the full combo, should read off.
	body := `approval_policy = "never"
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if enabled, _ := EffectiveYoloMode(); enabled {
		t.Error("partial YOLO config should not register as enabled")
	}
}
