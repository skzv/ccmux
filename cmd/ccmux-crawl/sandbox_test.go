package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/claudeconfig"
	"github.com/skzv/ccmux/internal/codexconfig"
	"github.com/skzv/ccmux/internal/config"
)

// fakeRealHome points every directory variable at a temp "real" home
// (t.Setenv restores the true values afterwards, including whatever
// the sandbox sets) and returns it.
func fakeRealHome(t *testing.T) string {
	t.Helper()
	real := t.TempDir()
	t.Setenv("HOME", real)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(real, ".config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(real, ".local", "state"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(real, ".local", "share"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(real, ".cache"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(real, ".claude"))
	t.Setenv("CODEX_HOME", filepath.Join(real, ".codex"))
	t.Setenv("CURSOR_HOME", filepath.Join(real, ".cursor"))
	t.Setenv("ANTIGRAVITY_HOME", filepath.Join(real, ".gemini", "antigravity"))
	t.Setenv("TMUX_TMPDIR", filepath.Join(real, "tmux"))
	t.Setenv("TMUX", "/tmp/tmux-501/default,123,0")
	t.Setenv("TMUX_PANE", "%1")
	prev := useRealHome
	useRealHome = false
	t.Cleanup(func() { useRealHome = prev })
	return real
}

// filesUnder lists every regular file below dir.
func filesUnder(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	_ = filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err == nil && fi.Mode().IsRegular() {
			out = append(out, p)
		}
		return nil
	})
	return out
}

// TestCrawl_WritesStayOutOfRealHome — ccmux-crawl drove the TUI against
// the real config: after a run config.toml had shown_version="crawl"
// (among worse). Dismissing the tour — which the random walk does
// constantly — persists the tour version; it must land in the sandbox.
func TestCrawl_WritesStayOutOfRealHome(t *testing.T) {
	real := fakeRealHome(t)
	var buf bytes.Buffer
	if err := setupSandbox(&buf); err != nil {
		t.Fatalf("setupSandbox: %v", err)
	}
	defer sandboxCleanup()

	res := runCrawlWithPreamble(0, []Input{keyRune('T'), keyType(tea.KeyEsc, "esc")}, nil, 120, 40)
	if res.Panic != nil {
		t.Fatalf("crawl panicked: %v\n%s", res.Panic, res.Stack)
	}
	if files := filesUnder(t, real); len(files) != 0 {
		t.Errorf("the crawl wrote into the real home: %v", files)
	}
	p, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(p)
	if err != nil || !strings.Contains(string(raw), "crawl") {
		t.Errorf("the tour write should have landed in the sandbox config %s: %v %q", p, err, raw)
	}
	if !strings.Contains(buf.String(), "sandbox HOME=") {
		t.Errorf("the sandbox location should be printed: %q", buf.String())
	}
}

// TestEnterSandbox_RedirectsEveryWritablePath — every config location a
// TUI screen writes resolves inside the sandbox, and $TMUX (which names
// the user's server directly) is gone.
func TestEnterSandbox_RedirectsEveryWritablePath(t *testing.T) {
	real := fakeRealHome(t)
	dir, cleanup, err := enterSandbox()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	inSandbox := func(what, p string) {
		t.Helper()
		if !strings.HasPrefix(p, dir+string(os.PathSeparator)) {
			t.Errorf("%s = %s, want it under the sandbox %s", what, p, dir)
		}
		if strings.HasPrefix(p, real) {
			t.Errorf("%s = %s is still in the real home", what, p)
		}
	}
	home, _ := os.UserHomeDir()
	if home != dir {
		t.Errorf("HOME = %s, want %s", home, dir)
	}
	cfgPath, _ := config.Path()
	inSandbox("ccmux config", cfgPath)
	cl, err := claudeconfig.Paths()
	if err != nil {
		t.Fatal(err)
	}
	inSandbox("Claude settings", cl.Settings)
	cx, err := codexconfig.Paths()
	if err != nil {
		t.Fatal(err)
	}
	inSandbox("Codex config", cx.Config)
	inSandbox("TMUX_TMPDIR", os.Getenv("TMUX_TMPDIR"))
	for _, k := range []string{"TMUX", "TMUX_PANE"} {
		if v, ok := os.LookupEnv(k); ok {
			t.Errorf("%s still set (%q): tmux would reach the user's server", k, v)
		}
	}

	cleanup()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("cleanup left the sandbox behind: %v", err)
	}
}

// TestUseRealHome_OptsOut — the explicit flag leaves the environment
// alone.
func TestUseRealHome_OptsOut(t *testing.T) {
	real := fakeRealHome(t)
	useRealHome = true
	var buf bytes.Buffer
	if err := setupSandbox(&buf); err != nil {
		t.Fatal(err)
	}
	if home, _ := os.UserHomeDir(); home != real {
		t.Errorf("--use-real-home moved HOME to %s", home)
	}
	if !strings.Contains(buf.String(), "--use-real-home") {
		t.Errorf("opting out should be announced: %q", buf.String())
	}
}
