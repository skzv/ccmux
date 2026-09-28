package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/daemon"
)

// realHome is captured at package init, before TestMain sandboxes HOME.
var realHome, _ = os.UserHomeDir()

// TestMain points HOME, the XDG dirs and tmux's socket dir at a
// throwaway directory for the whole test binary. The subprocess tests
// already build their own environment (cliEnv), but in-process tests
// that forgot a t.Setenv("HOME", …) read the developer's real config
// and could reach the live ccmuxd, whose socket daemon.LocalClient
// derives from $HOME.
func TestMain(m *testing.M) {
	// The harness re-runs this binary as the CLI (TestCLIHelperProcess)
	// with an environment it built itself; sandboxing again there would
	// replace the HOME that test set up.
	if os.Getenv("CCMUX_CLI_HELPER") == "1" {
		os.Exit(m.Run())
	}
	home, err := os.MkdirTemp("", "ccmux-cli-test")
	if err != nil {
		panic(err)
	}
	for k, v := range map[string]string{
		"HOME":            home,
		"XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		"XDG_STATE_HOME":  filepath.Join(home, ".local", "state"),
		"XDG_CACHE_HOME":  filepath.Join(home, ".cache"),
		"TMUX_TMPDIR":     home,
	} {
		if err := os.Setenv(k, v); err != nil {
			panic(err)
		}
	}
	for _, k := range []string{"TMUX", "TMUX_PANE", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "CURSOR_HOME", "ANTIGRAVITY_HOME"} {
		_ = os.Unsetenv(k)
	}
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}

// TestSandbox_TestsCannotReachTheRealDaemon — see TestMain.
func TestSandbox_TestsCannotReachTheRealDaemon(t *testing.T) {
	sock, err := daemon.SocketPath()
	if err != nil {
		t.Fatal(err)
	}
	if realHome != "" && strings.HasPrefix(sock, realHome+string(os.PathSeparator)) {
		t.Fatalf("tests dial the real daemon socket %s; TestMain must sandbox HOME", sock)
	}
}
