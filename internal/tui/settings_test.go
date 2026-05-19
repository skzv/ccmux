package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/tui/styles"
)

func TestEditableFields_ProjectsRoot(t *testing.T) {
	fields := byLabel(editableFields(), "projects.root")
	if fields == nil {
		t.Fatal("projects.root field missing")
	}

	cfg := config.Defaults()

	// Reject empty / blank.
	if err := fields.set(&cfg, ""); err == nil {
		t.Error("empty path should error")
	}
	if err := fields.set(&cfg, "   "); err == nil {
		t.Error("blank path should error")
	}

	// Reject non-existent paths.
	missing := filepath.Join(t.TempDir(), "nope")
	if err := fields.set(&cfg, missing); err == nil {
		t.Error("missing path should error")
	}

	// Reject a regular file.
	tmp := t.TempDir()
	f := filepath.Join(tmp, "file.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := fields.set(&cfg, f); err == nil {
		t.Error("regular file should error (must be a directory)")
	}

	// Accept an existing directory.
	if err := fields.set(&cfg, tmp); err != nil {
		t.Fatalf("valid dir rejected: %v", err)
	}
	if cfg.Projects.Root != tmp {
		t.Errorf("Projects.Root = %q, want %q", cfg.Projects.Root, tmp)
	}
}

func TestEditableFields_ScaffoldDirs(t *testing.T) {
	fields := byLabel(editableFields(), "scaffold.dirs")
	cfg := config.Defaults()
	cfg.Scaffold.Dirs = nil // start blank

	// Empty input clears any override.
	if err := fields.set(&cfg, ""); err != nil {
		t.Fatal(err)
	}
	if cfg.Scaffold.Dirs != nil {
		t.Errorf("empty input should leave Dirs nil, got %v", cfg.Scaffold.Dirs)
	}

	// Valid comma-separated list.
	if err := fields.set(&cfg, " src , tests, docs/01_Specs "); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Scaffold.Dirs) != 3 || cfg.Scaffold.Dirs[0] != "src" {
		t.Errorf("Dirs not parsed: %v", cfg.Scaffold.Dirs)
	}

	// Reject absolute paths — those would land scaffolded files outside
	// the project dir.
	if err := fields.set(&cfg, "/tmp/foo"); err == nil {
		t.Error("absolute path should error")
	}

	// Empty entries get squeezed out.
	if err := fields.set(&cfg, "src,,,tests,,"); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Scaffold.Dirs) != 2 {
		t.Errorf("empty entries should be squeezed: %v", cfg.Scaffold.Dirs)
	}

	// All-empty after squeeze → error.
	if err := fields.set(&cfg, ",, ,,"); err == nil {
		t.Error("all-empty input should error")
	}
}

func TestEditableFields_SubscriptionTier(t *testing.T) {
	fields := byLabel(editableFields(), "subscription.tier")
	cfg := config.Defaults()
	for _, ok := range []string{"", "api", "pro", "max5x", "max20x", "MAX20X", "  pro  "} {
		if err := fields.set(&cfg, ok); err != nil {
			t.Errorf("tier %q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"max", "max-5x", "free", "team"} {
		if err := fields.set(&cfg, bad); err == nil {
			t.Errorf("tier %q should be rejected", bad)
		}
	}
}

func TestEditableFields_ThemeIsReadOnly(t *testing.T) {
	f := byLabel(editableFields(), "theme")
	if f == nil {
		t.Fatal("theme field missing")
	}
	if !f.readOnly {
		t.Error("theme should be marked read-only until the picker lands in v0.2")
	}
}

// TestSettings_CursorMovesBetweenFields exercises the j/k bindings.
func TestSettings_CursorMovesBetweenFields(t *testing.T) {
	m := newSettings(styles.Default(), DefaultKeymap(), config.Defaults(), "test")
	max := len(editableFields()) - 1

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.cursor != 1 {
		t.Fatalf("after one down, cursor = %d, want 1", m.cursor)
	}
	// Walk to the end; should clamp.
	for i := 0; i < 10; i++ {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	if m.cursor != max {
		t.Fatalf("cursor should clamp at %d, got %d", max, m.cursor)
	}
	// Walk back to top; should clamp.
	for i := 0; i < 10; i++ {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	}
	if m.cursor != 0 {
		t.Fatalf("cursor should clamp at 0, got %d", m.cursor)
	}
}

func TestSettings_EditEnterCommitRoundTrip(t *testing.T) {
	// Hermetic home so config.Save writes to a tempdir.
	home := t.TempDir()
	t.Setenv("HOME", home)
	tmp := filepath.Join(home, "newroot")
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		t.Fatal(err)
	}

	m := newSettings(styles.Default(), DefaultKeymap(), config.Defaults(), "test")
	// Cursor on projects.root (index 0).
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.editing {
		t.Fatal("Enter on cursor should activate editing")
	}
	// Type a new path.
	m.editor.SetValue(tmp)
	m, _ = m.commit()
	if m.editing {
		t.Fatal("commit should close the editor")
	}
	if m.errMsg != "" {
		t.Fatalf("unexpected commit error: %s", m.errMsg)
	}
	if m.cfg.Projects.Root != tmp {
		t.Errorf("Projects.Root not applied: %q", m.cfg.Projects.Root)
	}
	// The change was persisted to disk.
	reloaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Projects.Root != tmp {
		t.Errorf("config.Save didn't persist Projects.Root: got %q", reloaded.Projects.Root)
	}
}

func TestSettings_EditEscCancels(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := newSettings(styles.Default(), DefaultKeymap(), config.Defaults(), "test")
	original := m.cfg.Projects.Root

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.editor.SetValue("/some/bogus/path/that/will/fail/validation")
	// Esc instead of commit — value should be discarded.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.editing {
		t.Fatal("Esc should close the editor")
	}
	if m.cfg.Projects.Root != original {
		t.Errorf("Esc should discard the edit: got %q, want %q", m.cfg.Projects.Root, original)
	}
}

func TestSettings_CommitWithInvalidValueKeepsEditingOpen(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := newSettings(styles.Default(), DefaultKeymap(), config.Defaults(), "test")

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.editor.SetValue("/nonexistent/path/that/cannot/exist")
	m, _ = m.commit()
	if !m.editing {
		t.Fatal("commit with invalid value should keep editor open")
	}
	if m.errMsg == "" {
		t.Error("expected inline error message on bad commit")
	}
}

func TestSettings_ReadOnlyFieldRefusesEnter(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := newSettings(styles.Default(), DefaultKeymap(), config.Defaults(), "test")
	// Move cursor to the theme row (the read-only one).
	fields := editableFields()
	for i, f := range fields {
		if f.label == "theme" {
			m.cursor = i
			break
		}
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.editing {
		t.Fatal("Enter on a read-only field should NOT start editing")
	}
	if !strings.Contains(m.errMsg, "read-only") {
		t.Errorf("expected read-only hint, got %q", m.errMsg)
	}
}

// byLabel is a tiny test helper that returns the named field or nil.
func byLabel(fields []editableField, name string) *editableField {
	for i := range fields {
		if fields[i].label == name {
			return &fields[i]
		}
	}
	return nil
}

// TestSettings_AgentsDefault_AcceptsValidIDs — the agents.default
// field validates against agent.ParseID, so all three canonical IDs
// plus the legacy "gemini" alias and the "shell" opt-out must round-
// trip. A regression here would block the user from changing their
// default from the Settings screen.
func TestSettings_AgentsDefault_AcceptsValidIDs(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"claude", "claude"},
		{"codex", "codex"},
		{"antigravity", "antigravity"},
		// Back-compat alias from the rebrand: must accept the input
		// but normalize to the canonical name when storing, otherwise
		// "gemini" would persist forever in the user's config.
		{"gemini", "antigravity"},
		{"shell", "shell"},
		{"  CODEX  ", "codex"},
		// Empty resets to claude (the default-of-default) — see field
		// doc in settings.go.
		{"", "claude"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			cfg := config.Defaults()
			f := byLabel(editableFields(), "agents.default")
			if f == nil {
				t.Fatal("agents.default field not registered in editableFields")
			}
			if err := f.set(&cfg, tc.input); err != nil {
				t.Fatalf("set(%q) returned error: %v", tc.input, err)
			}
			if got := cfg.Agents.Default; got != tc.want {
				t.Errorf("Agents.Default = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSettings_AgentsDefault_RejectsUnknown — typo'd or imaginary
// agent names must produce an error rather than silently persisting
// garbage. Without this, "ccode" would end up in config and the
// next session would fall back to claude with the user thinking
// their setting "took".
func TestSettings_AgentsDefault_RejectsUnknown(t *testing.T) {
	cfg := config.Defaults()
	f := byLabel(editableFields(), "agents.default")
	if f == nil {
		t.Fatal("agents.default field missing")
	}
	for _, bad := range []string{"ccode", "gpt-4", "imaginary", "claude-3-sonnet"} {
		t.Run(bad, func(t *testing.T) {
			err := f.set(&cfg, bad)
			if err == nil {
				t.Errorf("set(%q) should error, got nil", bad)
			}
		})
	}
}

// TestSettings_AttachMode_AcceptsValidModes — the sessions.attach_mode
// Settings field accepts "mirror" / "exclusive" (and empty → mirror),
// case-insensitively, and rejects anything else. This is the surface
// the user flips to opt out of mirror mode.
func TestSettings_AttachMode_AcceptsValidModes(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"mirror", "mirror"},
		{"exclusive", "exclusive"},
		{"EXCLUSIVE", "exclusive"},
		{"  Mirror  ", "mirror"},
		{"", "mirror"}, // empty resets to the default
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			cfg := config.Defaults()
			f := byLabel(editableFields(), "sessions.attach_mode")
			if f == nil {
				t.Fatal("sessions.attach_mode field not registered")
			}
			if err := f.set(&cfg, tc.input); err != nil {
				t.Fatalf("set(%q) errored: %v", tc.input, err)
			}
			if cfg.Sessions.AttachMode != tc.want {
				t.Errorf("AttachMode = %q, want %q", cfg.Sessions.AttachMode, tc.want)
			}
		})
	}
}

// TestSettings_AttachMode_RejectsUnknown — a typo must error rather
// than persist. "miror" silently falling through would leave the user
// in mirror mode while they think they typed something meaningful.
func TestSettings_AttachMode_RejectsUnknown(t *testing.T) {
	cfg := config.Defaults()
	f := byLabel(editableFields(), "sessions.attach_mode")
	if f == nil {
		t.Fatal("sessions.attach_mode field missing")
	}
	for _, bad := range []string{"miror", "shared", "solo", "detached"} {
		t.Run(bad, func(t *testing.T) {
			if err := f.set(&cfg, bad); err == nil {
				t.Errorf("set(%q) should error, got nil", bad)
			}
		})
	}
}

// TestSettings_AttachMode_GetShowsEffectiveValue — an empty stored
// value must display as "mirror" in the Settings row, not blank, so
// the user sees what's actually in effect.
func TestSettings_AttachMode_GetShowsEffectiveValue(t *testing.T) {
	f := byLabel(editableFields(), "sessions.attach_mode")
	if f == nil {
		t.Fatal("sessions.attach_mode field missing")
	}
	cfg := config.Defaults()
	cfg.Sessions.AttachMode = "" // simulate a pre-field config
	if got := f.get(&cfg); got != "mirror" {
		t.Errorf("get() on empty AttachMode = %q, want mirror (effective value)", got)
	}
}

// TestSettings_AutoCheck_TogglesBothWays — the update.auto_check
// Settings field accepts the common on/off vocabularies and flips
// the bool. Empty resets to on (the default).
func TestSettings_AutoCheck_TogglesBothWays(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"on", true}, {"off", false},
		{"true", true}, {"false", false},
		{"yes", true}, {"no", false},
		{"ON", true}, {"OFF", false},
		{"", true}, // empty → default on
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			cfg := config.Defaults()
			f := byLabel(editableFields(), "update.auto_check")
			if f == nil {
				t.Fatal("update.auto_check field not registered")
			}
			if err := f.set(&cfg, tc.input); err != nil {
				t.Fatalf("set(%q) errored: %v", tc.input, err)
			}
			if cfg.Update.AutoCheck != tc.want {
				t.Errorf("AutoCheck = %v, want %v", cfg.Update.AutoCheck, tc.want)
			}
		})
	}
}

// TestSettings_AutoCheck_RejectsGarbage — a value that's neither
// on-ish nor off-ish must error rather than silently picking one.
func TestSettings_AutoCheck_RejectsGarbage(t *testing.T) {
	cfg := config.Defaults()
	f := byLabel(editableFields(), "update.auto_check")
	if f == nil {
		t.Fatal("update.auto_check field missing")
	}
	for _, bad := range []string{"maybe", "sometimes", "2"} {
		if err := f.set(&cfg, bad); err == nil {
			t.Errorf("set(%q) should error", bad)
		}
	}
}

// TestSettings_AutoCheck_GetShowsOnOff — the field renders as a
// human "on"/"off", not Go's "true"/"false".
func TestSettings_AutoCheck_GetShowsOnOff(t *testing.T) {
	f := byLabel(editableFields(), "update.auto_check")
	if f == nil {
		t.Fatal("update.auto_check field missing")
	}
	cfg := config.Defaults()
	cfg.Update.AutoCheck = true
	if got := f.get(&cfg); got != "on" {
		t.Errorf("get() with AutoCheck=true = %q, want on", got)
	}
	cfg.Update.AutoCheck = false
	if got := f.get(&cfg); got != "off" {
		t.Errorf("get() with AutoCheck=false = %q, want off", got)
	}
}
