package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/claudeconfig"
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
	return dir
}

// writeClaudeSettings drops a settings.json into the fake claude dir.
func writeClaudeSettings(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestNormalizeModelAlias — the helper that maps EffectiveModel output
// (alias, full vendor ID, or "(default)" sentinel) onto the picker's
// known-aliases so the cursor can pre-position correctly.
func TestNormalizeModelAlias(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"(default)", ""},
		{"  (default)  ", ""},
		{"opus", "opus"},
		{"  opus  ", "opus"},
		{"OPUS", "opus"},
		{"sonnet", "sonnet"},
		{"haiku", "haiku"},
		{"claude-opus-4-7", "opus"},
		{"claude-opus-4-1", "opus"},
		{"claude-opus-4", "opus"},
		{"claude-sonnet-4-6", "sonnet"},
		{"claude-sonnet-4-5", "sonnet"},
		{"claude-sonnet-4", "sonnet"},
		{"claude-haiku-4-5", "haiku"},
		{"claude-haiku-4", "haiku"},
		// Unknown stays as-is (lowercased + trimmed) so the cursor
		// lookup still works for aliases the user added by hand.
		{"unknown-model", "unknown-model"},
		{"  custom-id  ", "custom-id"},
		{"opusplan", "opusplan"},
	}
	for _, tc := range cases {
		if got := normalizeModelAlias(tc.in); got != tc.want {
			t.Errorf("normalizeModelAlias(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestClaudeModel_PickerPreselectsEffectiveModel_FromSettings — when
// settings.json has model="sonnet" and no env var, opening the picker
// (m key) lands the cursor on the sonnet row (index 1 in KnownModels).
func TestClaudeModel_PickerPreselectsEffectiveModel_FromSettings(t *testing.T) {
	dir := fakeClaudeDir(t)
	writeClaudeSettings(t, dir, `{"model":"sonnet"}`)

	m := newClaude(styles.Default(), DefaultKeymap())
	m, _ = m.Update(keyMsg("m"))

	if m.picker != pickerModel {
		t.Fatalf("picker = %v, want pickerModel", m.picker)
	}
	want := indexOfAlias("sonnet")
	if m.pickerCursor != want {
		t.Fatalf("pickerCursor = %d (alias %q), want %d (sonnet)",
			m.pickerCursor, aliasAt(m.pickerCursor), want)
	}
}

// TestClaudeModel_PickerPreselectsEffectiveModel_FromEnvVar — when
// settings.json is empty but $ANTHROPIC_MODEL is set to a short alias,
// the picker pre-positions on THAT alias (not "Inherit"). This is the
// regression test for the bug the dev just fixed.
func TestClaudeModel_PickerPreselectsEffectiveModel_FromEnvVar(t *testing.T) {
	fakeClaudeDir(t)
	t.Setenv("ANTHROPIC_MODEL", "opus") // overrides the empty fake dir

	m := newClaude(styles.Default(), DefaultKeymap())
	m, _ = m.Update(keyMsg("m"))

	want := indexOfAlias("opus")
	if m.pickerCursor != want {
		t.Fatalf("pickerCursor = %d (alias %q), want %d (opus). The cursor must NOT default to Inherit when an env var is overriding.",
			m.pickerCursor, aliasAt(m.pickerCursor), want)
	}
}

// TestClaudeModel_PickerPreselectsEffectiveModel_FromEnvVarFullID —
// same as above but the env var holds the full vendor ID. normalizeModelAlias
// must collapse "claude-opus-4-7" → "opus" so the cursor still finds it.
func TestClaudeModel_PickerPreselectsEffectiveModel_FromEnvVarFullID(t *testing.T) {
	fakeClaudeDir(t)
	t.Setenv("ANTHROPIC_MODEL", "claude-opus-4-7")

	m := newClaude(styles.Default(), DefaultKeymap())
	m, _ = m.Update(keyMsg("m"))

	want := indexOfAlias("opus")
	if m.pickerCursor != want {
		t.Fatalf("pickerCursor = %d (alias %q), want %d (opus) — full vendor ID must normalize to alias",
			m.pickerCursor, aliasAt(m.pickerCursor), want)
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

// TestClaudeModel_PickerSubmitWritesSettings — open the picker,
// navigate to a known row, submit (enter). The settings.json must
// contain the chosen alias, and the in-memory model state must reflect
// it after the change-msg cycle has completed.
func TestClaudeModel_PickerSubmitWritesSettings(t *testing.T) {
	fakeClaudeDir(t)
	m := newClaude(styles.Default(), DefaultKeymap())

	// Open the picker. With no settings + no env var, the cursor lands
	// on "Inherit / no override". Navigate up to the haiku row at
	// index 2 in KnownModels (opus, sonnet, haiku, opusplan, "").
	m, _ = m.Update(keyMsg("m"))
	// Drive the cursor to a known row deterministically.
	m.pickerCursor = indexOfAlias("haiku")

	// Submit. The handler returns a cmd that performs SetModel and
	// emits claudeModelChangedMsg. Capture the post-enter model state
	// (which already has picker=pickerNone), then run the cmd and feed
	// the resulting message back so reload() fires.
	var cmd tea.Cmd
	m, cmd = m.Update(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("submit should return a cmd")
	}
	m, _ = m.Update(cmd())

	// Disk-side check.
	s, err := claudeconfig.ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.Model != "haiku" {
		t.Errorf("settings.json model = %q, want haiku", s.Model)
	}
	// In-memory check: reload() ran inside the changed-msg handler.
	if m.settings == nil || m.settings.Model != "haiku" {
		got := ""
		if m.settings != nil {
			got = m.settings.Model
		}
		t.Errorf("in-memory settings.Model = %q, want haiku", got)
	}
	if m.picker != pickerNone {
		t.Errorf("picker should close after submit, got %v", m.picker)
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

// indexOfAlias returns the position of the given alias in
// claudeconfig.KnownModels, or -1 if not found. Test-only helper that
// keeps the index assertions readable when KnownModels order changes.
func indexOfAlias(alias string) int {
	for i, o := range claudeconfig.KnownModels() {
		if o.Alias == alias {
			return i
		}
	}
	return -1
}

func aliasAt(i int) string {
	opts := claudeconfig.KnownModels()
	if i < 0 || i >= len(opts) {
		return "<oob>"
	}
	return opts[i].Alias
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
