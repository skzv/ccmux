package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/claudeconfig"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// fakeClaudeDir sets $CLAUDE_CONFIG_DIR to a fresh tempdir AND clears
// $ANTHROPIC_MODEL so a developer's real env doesn't leak in. Mirrors
// the claudeconfig package's withFakeClaudeDir helper but inlined here
// so the tui tests don't need to import the test helper from another
// package.
func fakeClaudeDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	t.Setenv("ANTHROPIC_MODEL", "")
	// Isolate HOME too: the unified model picker writes ccmux's own
	// config ([claude] default_model pin) under $HOME/.config/ccmux,
	// and loads the model catalog from $HOME/.local/state — pointing
	// HOME at a tempdir keeps a test from touching the real config and
	// makes the catalog deterministic (no daemon cache → curated
	// fallback, which carries the current model IDs).
	t.Setenv("HOME", t.TempDir())
	return dir
}

// writeClaudeSettings drops a settings.json into the fake claude dir.
func writeClaudeSettings(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// selectedChoice returns the unified-picker row the cursor is on.
func selectedChoice(t *testing.T, m claudeModel) modelChoice {
	t.Helper()
	choices := m.unifiedModelChoices()
	if m.pickerCursor < 0 || m.pickerCursor >= len(choices) {
		t.Fatalf("pickerCursor %d out of range (%d choices)", m.pickerCursor, len(choices))
	}
	return choices[m.pickerCursor]
}

// TestClaudeModel_PickerPreselectsEffectiveModel_FromSettings — with
// settings.json model="sonnet" and no env var, opening the picker lands
// the cursor on the row that sets "sonnet" (the alias row).
func TestClaudeModel_PickerPreselectsEffectiveModel_FromSettings(t *testing.T) {
	dir := fakeClaudeDir(t)
	writeClaudeSettings(t, dir, `{"model":"sonnet"}`)

	m := newClaude(styles.Default(), DefaultKeymap())
	m, _ = m.Update(keyMsg("m"))

	if m.picker != pickerModel {
		t.Fatalf("picker = %v, want pickerModel", m.picker)
	}
	if got := selectedChoice(t, m).Settings; got != "sonnet" {
		t.Fatalf("cursor landed on a row that sets %q, want sonnet", got)
	}
}

// TestClaudeModel_PickerPreselectsEffectiveModel_FromEnvVar — when
// $ANTHROPIC_MODEL is a short alias, the picker pre-positions on THAT
// alias, not "Inherit". Regression test for the env-override case.
func TestClaudeModel_PickerPreselectsEffectiveModel_FromEnvVar(t *testing.T) {
	fakeClaudeDir(t)
	t.Setenv("ANTHROPIC_MODEL", "opus")

	m := newClaude(styles.Default(), DefaultKeymap())
	m, _ = m.Update(keyMsg("m"))

	if got := selectedChoice(t, m).Settings; got != "opus" {
		t.Fatalf("cursor landed on a row that sets %q, want opus (must not default to Inherit when env var overrides)", got)
	}
}

// TestClaudeModel_PickerPreselectsEffectiveModel_FromEnvVarFullID —
// when the env var holds a full vendor ID present in the catalog, the
// cursor lands on that SPECIFIC catalog row (not just the family alias).
// The unified picker carries real IDs, so it pre-positions exactly.
func TestClaudeModel_PickerPreselectsEffectiveModel_FromEnvVarFullID(t *testing.T) {
	fakeClaudeDir(t)
	t.Setenv("ANTHROPIC_MODEL", "claude-opus-4-8")

	m := newClaude(styles.Default(), DefaultKeymap())
	m, _ = m.Update(keyMsg("m"))

	if got := selectedChoice(t, m).Settings; got != "claude-opus-4-8" {
		t.Fatalf("cursor landed on a row that sets %q, want the exact catalog ID claude-opus-4-8", got)
	}
}

// TestClaudeModel_PickerListsCurrentModelIDs — the headline of the
// merge: the picker shows real, current model IDs pulled from the
// catalog (incl. claude-opus-4-8), not a hardcoded alias-only list.
func TestClaudeModel_PickerListsCurrentModelIDs(t *testing.T) {
	fakeClaudeDir(t)
	m := newClaude(styles.Default(), DefaultKeymap())
	m, _ = m.Update(keyMsg("m"))
	out := m.View(120, 50)
	if !strings.Contains(out, "claude-opus-4-8") {
		t.Errorf("model picker should list the current catalog IDs (claude-opus-4-8); got:\n%s", out)
	}
}

// TestClaudeModel_BothMandMOpenTheSamePicker — the old uppercase-M
// "ccmux pin" picker is gone; M now opens the unified picker, same as m.
func TestClaudeModel_BothMandMOpenTheSamePicker(t *testing.T) {
	fakeClaudeDir(t)
	for _, key := range []string{"m", "M"} {
		m := newClaude(styles.Default(), DefaultKeymap())
		m, _ = m.Update(keyMsg(key))
		if m.picker != pickerModel {
			t.Errorf("key %q: picker = %v, want pickerModel", key, m.picker)
		}
	}
}

// TestClaudeModel_PickerShowsEnvVarWarning — when $ANTHROPIC_MODEL is
// set, the picker view renders a warning explaining that the env var
// is shadowing settings.json so the picker doesn't appear broken when
// nothing visibly changes after the user picks a row.
func TestClaudeModel_PickerShowsEnvVarWarning(t *testing.T) {
	fakeClaudeDir(t)
	t.Setenv("ANTHROPIC_MODEL", "opus")

	m := newClaude(styles.Default(), DefaultKeymap())
	m, _ = m.Update(keyMsg("m"))
	out := m.View(120, 40)

	if !strings.Contains(out, "ANTHROPIC_MODEL") {
		t.Errorf("picker view should mention ANTHROPIC_MODEL when env var is set; got:\n%s", out)
	}
	if !strings.Contains(out, "settings.json") {
		t.Errorf("picker view should mention settings.json in the warning; got:\n%s", out)
	}
}

// TestClaudeModel_PickerNoWarningWithoutEnvVar — with no env override,
// the picker must NOT spook the user with a false-positive warning.
func TestClaudeModel_PickerNoWarningWithoutEnvVar(t *testing.T) {
	fakeClaudeDir(t) // also clears ANTHROPIC_MODEL

	m := newClaude(styles.Default(), DefaultKeymap())
	m, _ = m.Update(keyMsg("m"))
	out := m.View(120, 40)

	if strings.Contains(out, "ANTHROPIC_MODEL") {
		t.Errorf("picker view should NOT mention ANTHROPIC_MODEL when env var is unset; got:\n%s", out)
	}
}

// TestClaudeModel_PickerOpensWithM — sending "m" sets the picker state.
func TestClaudeModel_PickerOpensWithM(t *testing.T) {
	fakeClaudeDir(t)
	m := newClaude(styles.Default(), DefaultKeymap())
	if m.picker != pickerNone {
		t.Fatalf("picker = %v, want pickerNone at start", m.picker)
	}
	m, _ = m.Update(keyMsg("m"))
	if m.picker != pickerModel {
		t.Fatalf("picker = %v, want pickerModel after m", m.picker)
	}
}

// TestClaudeModel_PickerClosesWithEsc — esc returns the picker to none
// without writing anything.
func TestClaudeModel_PickerClosesWithEsc(t *testing.T) {
	fakeClaudeDir(t)
	m := newClaude(styles.Default(), DefaultKeymap())
	m, _ = m.Update(keyMsg("m"))
	if m.picker != pickerModel {
		t.Fatal("precondition: picker should be open")
	}
	m, _ = m.Update(keyMsg("esc"))
	if m.picker != pickerNone {
		t.Fatalf("picker = %v, want pickerNone after esc", m.picker)
	}
	// Settings should be untouched.
	s, err := claudeconfig.ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.Model != "" {
		t.Errorf("esc must not write settings; Model = %q", s.Model)
	}
}

// choiceIndexBySettings finds the unified-picker row whose Settings
// value equals `settings`, or fails the test.
func choiceIndexBySettings(t *testing.T, m claudeModel, settings string) int {
	t.Helper()
	for i, c := range m.unifiedModelChoices() {
		if c.Settings == settings {
			return i
		}
	}
	t.Fatalf("no picker row sets %q (choices: %d)", settings, len(m.unifiedModelChoices()))
	return -1
}

// submitPicker runs the enter→cmd→message cycle and returns the model
// after reload(). Shared by the model-pick tests.
func submitPicker(t *testing.T, m claudeModel) claudeModel {
	t.Helper()
	var cmd tea.Cmd
	m, cmd = m.Update(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("submit should return a cmd")
	}
	m, _ = m.Update(cmd())
	if m.picker != pickerNone {
		t.Errorf("picker should close after submit, got %v", m.picker)
	}
	return m
}

// TestClaudeModel_PickFullIDWritesBothTargets — the core of the merge:
// picking a specific catalog model writes BOTH settings.json `model`
// AND ccmux's pin, so the choice takes effect for ccmux-launched
// sessions even when the shell exports a different ANTHROPIC_MODEL.
func TestClaudeModel_PickFullIDWritesBothTargets(t *testing.T) {
	fakeClaudeDir(t)
	m := newClaude(styles.Default(), DefaultKeymap())
	m, _ = m.Update(keyMsg("m"))
	m.pickerCursor = choiceIndexBySettings(t, m, "claude-opus-4-8")
	m = submitPicker(t, m)

	// settings.json (Claude Code global default).
	s, err := claudeconfig.ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.Model != "claude-opus-4-8" {
		t.Errorf("settings.json model = %q, want claude-opus-4-8", s.Model)
	}
	// ccmux pin (re-exported as ANTHROPIC_MODEL at launch).
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Claude.DefaultModel != "claude-opus-4-8" {
		t.Errorf("ccmux pin = %q, want claude-opus-4-8 (so the pick wins over a shell ANTHROPIC_MODEL)", cfg.Claude.DefaultModel)
	}
	// In-memory state reloaded.
	if m.ccmuxDefaultModel != "claude-opus-4-8" {
		t.Errorf("in-memory pin = %q, want claude-opus-4-8", m.ccmuxDefaultModel)
	}
}

// TestClaudeModel_PickAliasWritesSettingsClearsPin — an alias row
// ("always latest opus") writes the alias to settings.json and CLEARS
// the pin, because aliases track the latest version and a frozen pin
// would defeat that.
func TestClaudeModel_PickAliasWritesSettingsClearsPin(t *testing.T) {
	fakeClaudeDir(t)
	// Pre-seed a pin so we can prove it gets cleared.
	if err := setCcmuxClaudeDefault("claude-opus-4-8"); err != nil {
		t.Fatal(err)
	}
	m := newClaude(styles.Default(), DefaultKeymap())
	m, _ = m.Update(keyMsg("m"))
	m.pickerCursor = choiceIndexBySettings(t, m, "opus") // the alias row
	m = submitPicker(t, m)

	s, _ := claudeconfig.ReadSettings()
	if s.Model != "opus" {
		t.Errorf("settings.json model = %q, want opus", s.Model)
	}
	cfg, _ := config.Load()
	if cfg.Claude.DefaultModel != "" {
		t.Errorf("alias pick should clear the pin; ccmux pin = %q, want empty", cfg.Claude.DefaultModel)
	}
}

// TestClaudeModel_PickInheritClearsBoth — the inherit/clear row wipes
// both the settings.json model and the ccmux pin.
func TestClaudeModel_PickInheritClearsBoth(t *testing.T) {
	dir := fakeClaudeDir(t)
	writeClaudeSettings(t, dir, `{"model":"sonnet"}`)
	if err := setCcmuxClaudeDefault("claude-opus-4-8"); err != nil {
		t.Fatal(err)
	}
	m := newClaude(styles.Default(), DefaultKeymap())
	m, _ = m.Update(keyMsg("m"))
	m.pickerCursor = 0 // the "Inherit / clear override" sentinel row
	m = submitPicker(t, m)

	s, _ := claudeconfig.ReadSettings()
	if s.Model != "" {
		t.Errorf("inherit should clear settings.json model; got %q", s.Model)
	}
	cfg, _ := config.Load()
	if cfg.Claude.DefaultModel != "" {
		t.Errorf("inherit should clear the pin; got %q", cfg.Claude.DefaultModel)
	}
}

// TestClaudeModel_EffortPickerOpensWithE — sending "e" opens the
// effort picker.
func TestClaudeModel_EffortPickerOpensWithE(t *testing.T) {
	fakeClaudeDir(t)
	m := newClaude(styles.Default(), DefaultKeymap())
	m, _ = m.Update(keyMsg("e"))
	if m.picker != pickerEffort {
		t.Fatalf("picker = %v, want pickerEffort", m.picker)
	}
}

// TestClaudeModel_EffortPickerSubmitWritesSettings — choosing the
// "high" row writes effortLevel=high to settings.json.
func TestClaudeModel_EffortPickerSubmitWritesSettings(t *testing.T) {
	fakeClaudeDir(t)
	m := newClaude(styles.Default(), DefaultKeymap())
	m, _ = m.Update(keyMsg("e"))
	m.pickerCursor = indexOfEffort("high")

	var cmd tea.Cmd
	m, cmd = m.Update(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("submit should return a cmd")
	}
	m, _ = m.Update(cmd())

	s, err := claudeconfig.ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.EffortLevel != "high" {
		t.Errorf("settings.json effortLevel = %q, want high", s.EffortLevel)
	}
	if m.settings == nil || m.settings.EffortLevel != "high" {
		got := ""
		if m.settings != nil {
			got = m.settings.EffortLevel
		}
		t.Errorf("in-memory effortLevel = %q, want high", got)
	}
}

// TestClaudeModel_EffortPickerPreselectsCurrent — when settings.json
// has effortLevel="medium", opening the picker lands the cursor on
// medium (index 3 in KnownEffortLevels: max, xhigh, high, medium, low, "").
func TestClaudeModel_EffortPickerPreselectsCurrent(t *testing.T) {
	dir := fakeClaudeDir(t)
	writeClaudeSettings(t, dir, `{"effortLevel":"medium"}`)

	m := newClaude(styles.Default(), DefaultKeymap())
	m, _ = m.Update(keyMsg("e"))

	want := indexOfEffort("medium")
	if m.pickerCursor != want {
		t.Fatalf("pickerCursor = %d (%q), want %d (medium)",
			m.pickerCursor, effortAt(m.pickerCursor), want)
	}
}

// TestClaudeModel_AlwaysThinkingToggleWritesSettings — "a" flips
// the AlwaysThinkingEnabled bool; second press flips it back.
func TestClaudeModel_AlwaysThinkingToggleWritesSettings(t *testing.T) {
	fakeClaudeDir(t)
	m := newClaude(styles.Default(), DefaultKeymap())

	// First toggle: off → on.
	var cmd tea.Cmd
	m, cmd = m.Update(keyMsg("a"))
	if cmd == nil {
		t.Fatal("a key should return a cmd")
	}
	m, _ = m.Update(cmd())

	s, err := claudeconfig.ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !s.AlwaysThinkingEnabled {
		t.Errorf("after first toggle, AlwaysThinkingEnabled = false, want true")
	}
	if !m.alwaysThinking {
		t.Errorf("in-memory alwaysThinking should be true after reload")
	}

	// Second toggle: on → off.
	m, cmd = m.Update(keyMsg("a"))
	if cmd == nil {
		t.Fatal("a key should return a cmd on second press")
	}
	m, _ = m.Update(cmd())

	s, err = claudeconfig.ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.AlwaysThinkingEnabled {
		t.Errorf("after second toggle, AlwaysThinkingEnabled = true, want false")
	}
	if m.alwaysThinking {
		t.Errorf("in-memory alwaysThinking should be false after second toggle")
	}
}

// TestClaudeModel_YoloToggleWritesSettings — "y" sets
// permissions.defaultMode = "bypassPermissions"; second press unsets it.
func TestClaudeModel_YoloToggleWritesSettings(t *testing.T) {
	fakeClaudeDir(t)
	m := newClaude(styles.Default(), DefaultKeymap())

	var cmd tea.Cmd
	m, cmd = m.Update(keyMsg("y"))
	if cmd == nil {
		t.Fatal("y key should return a cmd")
	}
	m, _ = m.Update(cmd())

	s, err := claudeconfig.ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.Permissions.DefaultMode != claudeconfig.YoloModeValue {
		t.Errorf("after first toggle, DefaultMode = %q, want %q",
			s.Permissions.DefaultMode, claudeconfig.YoloModeValue)
	}
	if !m.yolo {
		t.Errorf("in-memory yolo should be true after reload")
	}

	// Toggle back off.
	m, cmd = m.Update(keyMsg("y"))
	if cmd == nil {
		t.Fatal("y key should return a cmd on second press")
	}
	m, _ = m.Update(cmd())

	s, err = claudeconfig.ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.Permissions.DefaultMode == claudeconfig.YoloModeValue {
		t.Errorf("after second toggle, DefaultMode still %q, want cleared",
			s.Permissions.DefaultMode)
	}
	if m.yolo {
		t.Errorf("in-memory yolo should be false after second toggle")
	}
}

func indexOfEffort(value string) int {
	for i, o := range claudeconfig.KnownEffortLevels() {
		if o.Value == value {
			return i
		}
	}
	return -1
}

func effortAt(i int) string {
	opts := claudeconfig.KnownEffortLevels()
	if i < 0 || i >= len(opts) {
		return "<oob>"
	}
	return opts[i].Value
}

// TestSummarizePath_StandardPath — happy path: $HOME prefix becomes ~.
func TestSummarizePath_StandardPath(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	if got := summarizePath(tmp + "/Projects/ccmux"); got != "~/Projects/ccmux" {
		t.Errorf("summarizePath = %q, want ~/Projects/ccmux", got)
	}
}

// TestSummarizePath_HomeItself — bare $HOME becomes bare ~.
func TestSummarizePath_HomeItself(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	if got := summarizePath(tmp); got != "~" {
		t.Errorf("summarizePath(HOME) = %q, want ~", got)
	}
}

// TestSummarizePath_DoubleSlashInHome pins the macOS-TMPDIR-trailing-
// slash regression that broke the cuj11 demo's tildified path: the
// raw HasPrefix check failed against /var/folders/.../T//foo derived
// from $TMPDIR. filepath.Clean normalizes both sides.
func TestSummarizePath_DoubleSlashInHome(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp+"//extra")
	if got := summarizePath(tmp + "/extra/Projects/ccmux"); got != "~/Projects/ccmux" {
		t.Errorf("summarizePath with double-slash HOME = %q, want ~/Projects/ccmux", got)
	}
}

// TestSummarizePath_PathOutsideHome — passes through unchanged.
func TestSummarizePath_PathOutsideHome(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	if got := summarizePath("/usr/local/bin/ccmux"); got != "/usr/local/bin/ccmux" {
		t.Errorf("summarizePath outside HOME = %q, want passthrough", got)
	}
}

// TestSummarizePath_PrefixIsNotComponentMatch — /tmp/foo prefix must
// not falsely tildify /tmp/foobar. Without the trailing-separator
// guard, /tmp/foobar would render as ~bar.
func TestSummarizePath_PrefixIsNotComponentMatch(t *testing.T) {
	t.Setenv("HOME", "/tmp/foo")
	if got := summarizePath("/tmp/foobar/x"); got != "/tmp/foobar/x" {
		t.Errorf("summarizePath must not match partial components: got %q", got)
	}
}

// TestClaudeScreen_EffortPickerNoPanicOnMalformedSettings — regression
// for a nil-pointer crash. When settings.json is malformed (or
// permission-denied), claudeconfig.ReadSettings errors and reload()
// leaves m.settings nil. Pressing `e` (effort picker) used to deref
// m.settings.EffortLevel unguarded and crash the whole TUI — exactly
// when the user opened the Claude screen to fix the broken config.
func TestClaudeScreen_EffortPickerNoPanicOnMalformedSettings(t *testing.T) {
	dir := fakeClaudeDir(t)
	writeClaudeSettings(t, dir, "{ this is not valid json")

	m := newClaude(styles.Default(), DefaultKeymap())
	if m.settings != nil {
		t.Fatal("precondition: malformed settings.json should leave m.settings nil")
	}

	// Press `e`. Must not panic; must open the effort picker.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("pressing `e` with nil settings panicked: %v", r)
		}
	}()
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	if m2.picker != pickerEffort {
		t.Errorf("`e` should open the effort picker, got picker=%v", m2.picker)
	}
}
