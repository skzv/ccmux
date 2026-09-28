package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/i18n"
)

// TestMain pins the package's global language to English for every test.
// Under en, tr() returns the key verbatim, so the existing golden/width
// snapshots (which assert English text) stay byte-identical. Tests that
// need Chinese opt in via withLang(t, "zh").
func TestMain(m *testing.M) {
	// New(Config{}) resolves the environment again, so pin the locale as
	// well as the initial global state. Locale-specific tests use t.Setenv.
	if err := os.Setenv("LC_ALL", "en_US.UTF-8"); err != nil {
		panic(err)
	}
	if err := os.Setenv("LANG", "en_US.UTF-8"); err != nil {
		panic(err)
	}
	i18n.SetLanguage("en")
	// Never ask a real tmux server which session the test binary runs
	// in (the kill dialog does); tests that need it stub a name.
	currentTmuxSession = func() string { return "" }
	cleanup := sandboxTestEnv("ccmux-tui-test")
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// sandboxTestEnv points HOME, the XDG dirs and tmux's socket dir at a
// throwaway directory for the whole test binary, and clears variables
// that would lead back to the developer's real setup. Without it a test
// that forgot its own t.Setenv("HOME", …) read the real ~/.claude and
// config.toml (goldens picked up live hooks), and reached the live
// ccmuxd, because daemon.LocalClient derives its socket from $HOME and
// is a process-wide singleton. Tests that need a particular HOME still
// set their own with t.Setenv.
func sandboxTestEnv(prefix string) (cleanup func()) {
	home, err := os.MkdirTemp("", prefix)
	if err != nil {
		panic(err)
	}
	set := map[string]string{
		"HOME":            home,
		"XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		"XDG_STATE_HOME":  filepath.Join(home, ".local", "state"),
		"XDG_CACHE_HOME":  filepath.Join(home, ".cache"),
		"TMUX_TMPDIR":     home,
	}
	for k, v := range set {
		if err := os.Setenv(k, v); err != nil {
			panic(err)
		}
	}
	for _, k := range []string{"TMUX", "TMUX_PANE", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "CURSOR_HOME", "ANTIGRAVITY_HOME"} {
		_ = os.Unsetenv(k)
	}
	return func() { _ = os.RemoveAll(home) }
}

// withLang sets the package language for the duration of one test and
// restores English afterwards, so a zh test can't leak into the next.
func withLang(t *testing.T, lang string) {
	t.Helper()
	i18n.SetLanguage(lang)
	t.Cleanup(func() { i18n.SetLanguage("en") })
}

// realHome is captured at package init, before TestMain sandboxes HOME.
var realHome, _ = os.UserHomeDir()

// TestSandbox_TestsCannotReachTheRealDaemon — a TUI test once ran real
// refreshes against the developer's live ccmuxd (and broke a later test
// through the shared client's reused connection). The daemon socket a
// test would dial must never be the real one.
func TestSandbox_TestsCannotReachTheRealDaemon(t *testing.T) {
	sock, err := daemon.SocketPath()
	if err != nil {
		t.Fatal(err)
	}
	if realHome != "" && strings.HasPrefix(sock, realHome+string(os.PathSeparator)) {
		t.Fatalf("tests dial the real daemon socket %s; TestMain must sandbox HOME", sock)
	}
}
