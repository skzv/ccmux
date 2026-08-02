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

func TestSetEffortLevel_RoundTrip(t *testing.T) {
	withFakeClaudeDir(t)
	if _, err := SetEffortLevel("xhigh"); err != nil {
		t.Fatal(err)
	}
	s, err := ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.EffortLevel != "xhigh" {
		t.Errorf("effortLevel = %q, want xhigh", s.EffortLevel)
	}
}

func TestSetEffortLevel_PreservesUnrelatedFields(t *testing.T) {
	dir := withFakeClaudeDir(t)
	body := `{"theme":"dark","customKey":"keepMe","model":"opus"}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := SetEffortLevel("high"); err != nil {
		t.Fatal(err)
	}
	s, err := ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.EffortLevel != "high" || s.Model != "opus" || s.Theme != "dark" {
		t.Errorf("got effort=%q model=%q theme=%q", s.EffortLevel, s.Model, s.Theme)
	}
	if _, ok := s.Extra["customKey"]; !ok {
		t.Error("customKey dropped by SetEffortLevel")
	}
}

func TestSetEffortLevel_ClearOverride(t *testing.T) {
	withFakeClaudeDir(t)
	if _, err := SetEffortLevel("xhigh"); err != nil {
		t.Fatal(err)
	}
	if _, err := SetEffortLevel(""); err != nil {
		t.Fatal(err)
	}
	// "" should drop the key entirely (omitempty), so the raw file
	// shouldn't even mention effortLevel.
	p, _ := Paths()
	raw, err := os.ReadFile(p.Settings)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip map[string]any
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if _, ok := roundTrip["effortLevel"]; ok {
		t.Errorf("effortLevel should have been omitted after clear, file has: %v", roundTrip)
	}
}

func TestSetAlwaysThinking_RoundTrip(t *testing.T) {
	withFakeClaudeDir(t)
	if _, err := SetAlwaysThinking(true); err != nil {
		t.Fatal(err)
	}
	s, err := ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !s.AlwaysThinkingEnabled {
		t.Error("alwaysThinkingEnabled = false, want true")
	}

	// Toggling off should drop the key (omitempty), not write `false`.
	if _, err := SetAlwaysThinking(false); err != nil {
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
	if _, ok := roundTrip["alwaysThinkingEnabled"]; ok {
		t.Errorf("alwaysThinkingEnabled should have been omitted when false, file: %v", roundTrip)
	}
}

func TestEffectiveEffortLevel_Precedence(t *testing.T) {
	withFakeClaudeDir(t)
	// Nothing set → built-in default.
	if v, src := EffectiveEffortLevel(); v != "(default)" || src != "Claude Code default" {
		t.Errorf("default: got %q from %q", v, src)
	}
	// settings.json only.
	if _, err := SetEffortLevel("high"); err != nil {
		t.Fatal(err)
	}
	if v, src := EffectiveEffortLevel(); v != "high" || src != "settings.json" {
		t.Errorf("settings: got %q from %q", v, src)
	}
}

func TestKnownEffortLevels_IncludesEachLevel(t *testing.T) {
	want := map[string]bool{"low": false, "medium": false, "high": false, "xhigh": false, "max": false}
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

// TestSetEffortLevel_TrimsWhitespace ensures a copy-pasted value with
// stray spaces ("  high\n") is normalized before being written.
// Whitespace-prefixed values were a real source of "I picked high but
// settings.json says ' high'" confusion before SetEffortLevel learned
// to TrimSpace.
func TestSetEffortLevel_TrimsWhitespace(t *testing.T) {
	withFakeClaudeDir(t)
	if _, err := SetEffortLevel("  high\n"); err != nil {
		t.Fatal(err)
	}
	s, err := ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.EffortLevel != "high" {
		t.Errorf("effortLevel = %q, want exactly \"high\" (no surrounding whitespace)", s.EffortLevel)
	}
}

func TestSetYoloMode_RoundTrip(t *testing.T) {
	withFakeClaudeDir(t)
	if _, err := SetYoloMode(true); err != nil {
		t.Fatal(err)
	}
	s, err := ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.Permissions.DefaultMode != YoloModeValue {
		t.Errorf("defaultMode = %q, want %q", s.Permissions.DefaultMode, YoloModeValue)
	}
	if enabled, src := EffectiveYoloMode(); !enabled || src != "settings.json" {
		t.Errorf("EffectiveYoloMode = (%v, %q)", enabled, src)
	}
}

// TestSetYoloMode_DisablePreservesOtherDefaultMode covers the
// hand-edited-config case: a user with `permissions.defaultMode =
// "acceptEdits"` flipping YOLO off through ccmux must NOT lose their
// acceptEdits setting.
func TestSetYoloMode_DisablePreservesOtherDefaultMode(t *testing.T) {
	dir := withFakeClaudeDir(t)
	body := `{"permissions":{"defaultMode":"acceptEdits"}}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := SetYoloMode(false); err != nil {
		t.Fatal(err)
	}
	s, err := ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.Permissions.DefaultMode != "acceptEdits" {
		t.Errorf("user's acceptEdits defaultMode was clobbered: %q", s.Permissions.DefaultMode)
	}
}

func TestSetYoloMode_DisableClearsOurSentinel(t *testing.T) {
	withFakeClaudeDir(t)
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
	if perms, ok := roundTrip["permissions"]; ok {
		// permissions key may still exist (allow/deny preserved) but
		// defaultMode must be gone.
		p := perms.(map[string]any)
		if _, has := p["defaultMode"]; has {
			t.Errorf("defaultMode should be cleared, file has: %v", p)
		}
	}
}

func TestSetYoloMode_PreservesAllowDeny(t *testing.T) {
	dir := withFakeClaudeDir(t)
	body := `{"permissions":{"allow":["Read"],"deny":["Bash(rm -rf:*)"]}}`
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
	if len(s.Permissions.Allow) != 1 || s.Permissions.Allow[0] != "Read" {
		t.Errorf("Allow patterns lost: %v", s.Permissions.Allow)
	}
	if len(s.Permissions.Deny) != 1 || s.Permissions.Deny[0] != "Bash(rm -rf:*)" {
		t.Errorf("Deny patterns lost: %v", s.Permissions.Deny)
	}
	if s.Permissions.DefaultMode != YoloModeValue {
		t.Errorf("defaultMode = %q, want %q", s.Permissions.DefaultMode, YoloModeValue)
	}
}

func TestEffectiveYoloMode_Precedence(t *testing.T) {
	withFakeClaudeDir(t)
	if enabled, src := EffectiveYoloMode(); enabled || src != "Claude Code default" {
		t.Errorf("default: got (%v, %q)", enabled, src)
	}
	if _, err := SetYoloMode(true); err != nil {
		t.Fatal(err)
	}
	if enabled, src := EffectiveYoloMode(); !enabled || src != "settings.json" {
		t.Errorf("settings: got (%v, %q)", enabled, src)
	}
}

// TestWriteSettings_Atomic — a partial write must never leave settings.json
// truncated or empty. The atomic write-then-rename means either the
// previous version is still on disk, or the new one is — never a half.
func TestWriteSettings_Atomic(t *testing.T) {
	dir := withFakeClaudeDir(t)
	// Pre-seed with content that round-trip-preserves Extra so we can
	// confirm a failed write doesn't blow it away.
	initial := `{"model":"sonnet","customField":42}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	// Successful write replaces the file atomically — the temp file must
	// not linger.
	s, err := ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	s.Model = "opus"
	if _, err := WriteSettings(s); err != nil {
		t.Fatalf("WriteSettings: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".settings-") && strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temp file %q leaked after successful write", e.Name())
		}
	}
	got, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"customField": 42`) {
		t.Errorf("Extra field lost after atomic write:\n%s", got)
	}
	if !strings.Contains(string(got), `"model": "opus"`) {
		t.Errorf("model not updated:\n%s", got)
	}
}

// TestBackupFile_RotatesBeyondCap — every WriteSettings call creates a
// backup; without rotation a power user accumulates thousands. After
// the cap the oldest entries get pruned.
func TestBackupFile_RotatesBeyondCap(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "settings.json")
	backups := filepath.Join(dir, "backups")
	if err := os.WriteFile(src, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Seed maxBackupsPerFile + 5 pre-existing backup files with lexically
	// ordered names (matches the timestamp suffix the real code uses).
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxBackupsPerFile+5; i++ {
		name := "settings.json." + sprintN(i)
		if err := os.WriteFile(filepath.Join(backups, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := backupFile(src, backups); err != nil {
		t.Fatalf("backupFile: %v", err)
	}
	entries, err := os.ReadDir(backups)
	if err != nil {
		t.Fatal(err)
	}
	matches := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "settings.json.") {
			matches++
		}
	}
	if matches != maxBackupsPerFile {
		t.Errorf("after rotation: %d backup files, want %d", matches, maxBackupsPerFile)
	}
}

// sprintN renders n as a fixed-width 6-digit decimal so lexical sort
// matches numerical order (rotation prunes the lexically-smallest
// entries, which on real backups corresponds to the oldest by
// timestamp).
func sprintN(n int) string {
	const digits = "0123456789"
	out := []byte{'0', '0', '0', '0', '0', '0'}
	i := len(out) - 1
	for n > 0 && i >= 0 {
		out[i] = digits[n%10]
		n /= 10
		i--
	}
	return string(out)
}

// TestRoundTrip_PreservesHookMatcher — regression for the silent
// data-loss bug where a ReadSettings→WriteSettings cycle (triggered by
// any ccmux setting toggle) stripped a hook's `matcher` field, turning
// a tool-scoped hook into an all-tools hook. Writes a settings.json
// with a Bash-scoped PreToolUse hook, round-trips through the typed
// Settings, and asserts the matcher survives on disk.
func TestRoundTrip_PreservesHookMatcher(t *testing.T) {
	dir := withFakeClaudeDir(t)
	settingsPath := filepath.Join(dir, "settings.json")
	const input = `{
  "model": "opus",
  "hooks": {
    "PreToolUse": [
      { "matcher": "Bash", "hooks": [ { "type": "command", "command": "echo hi" } ] }
    ]
  }
}`
	if err := os.WriteFile(settingsPath, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}
	// The matcher must be captured in the HookGroup's Extra, not dropped.
	grp := s.Hooks["PreToolUse"][0]
	if _, ok := grp.Extra["matcher"]; !ok {
		t.Errorf("hook matcher not captured in Extra; got Extra=%v", grp.Extra)
	}

	// Simulate a ccmux write (e.g. SetModel).
	if _, err := WriteSettings(s); err != nil {
		t.Fatalf("WriteSettings: %v", err)
	}
	out, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(out), `"matcher"`) || !contains(string(out), `"Bash"`) {
		t.Errorf("matcher dropped on round-trip; settings.json now:\n%s", out)
	}
}

// TestRoundTrip_PreservesMCPServerHeaders — regression for the bug
// where an MCP server's unmodeled fields (notably `headers`, which
// commonly carry an Authorization bearer token) were stripped on any
// ccmux settings write, silently breaking the server's auth.
func TestRoundTrip_PreservesMCPServerHeaders(t *testing.T) {
	dir := withFakeClaudeDir(t)
	settingsPath := filepath.Join(dir, "settings.json")
	const input = `{
  "model": "opus",
  "mcpServers": {
    "remote": {
      "type": "http",
      "url": "https://mcp.example.com",
      "headers": { "Authorization": "Bearer SECRET_TOKEN" },
      "disabled": false
    }
  }
}`
	if err := os.WriteFile(settingsPath, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}
	srv := s.MCPServers["remote"]
	if srv.Type != "http" || srv.URL != "https://mcp.example.com" {
		t.Errorf("modelled fields wrong: %+v", srv)
	}
	if _, ok := srv.Extra["headers"]; !ok {
		t.Errorf("MCP headers not captured in Extra; got Extra=%v", srv.Extra)
	}

	if _, err := WriteSettings(s); err != nil {
		t.Fatalf("WriteSettings: %v", err)
	}
	out, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !contains(got, "Bearer SECRET_TOKEN") {
		t.Errorf("MCP Authorization header dropped on round-trip; settings.json now:\n%s", got)
	}
	if !contains(got, `"disabled"`) {
		t.Errorf("MCP `disabled` field dropped on round-trip; settings.json now:\n%s", got)
	}
}

// TestRoundTrip_HookAndMCPStructuralIntegrity — the modelled fields
// must still survive too (we didn't trade unknown-field preservation
// for known-field loss). Asserts hooks[].command and an MCP server's
// type/url are intact through a round-trip.
func TestRoundTrip_HookAndMCPStructuralIntegrity(t *testing.T) {
	dir := withFakeClaudeDir(t)
	settingsPath := filepath.Join(dir, "settings.json")
	const input = `{
  "hooks": { "Stop": [ { "hooks": [ { "type": "command", "command": "notify" } ] } ] },
  "mcpServers": { "local": { "type": "stdio", "command": "my-server", "args": ["--flag"] } }
}`
	if err := os.WriteFile(settingsPath, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}
	if got := s.Hooks["Stop"][0].Hooks[0].Command; got != "notify" {
		t.Errorf("hook command lost: %q", got)
	}
	if got := s.MCPServers["local"]; got.Command != "my-server" || len(got.Args) != 1 {
		t.Errorf("MCP modelled fields lost: %+v", got)
	}
	if _, err := WriteSettings(s); err != nil {
		t.Fatalf("WriteSettings: %v", err)
	}
	out, _ := os.ReadFile(settingsPath)
	for _, want := range []string{`"command": "notify"`, `"my-server"`, `"--flag"`} {
		if !contains(string(out), want) {
			t.Errorf("expected %s in round-tripped settings:\n%s", want, out)
		}
	}
}
