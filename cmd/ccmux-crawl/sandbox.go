package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// useRealHome is --use-real-home: crawl against the invoking user's
// real $HOME instead of a throwaway one.
var useRealHome bool

// Random keys drive the real TUI, and the TUI writes what its screens
// edit: a crawl left config.toml with shown_version="crawl" and
// tier="api", created ~/.codex/config.toml (plus four backups), and a
// stray Enter on the Agents screen can set Claude Code's
// permissions.defaultMode to bypassPermissions. So by default every
// run gets a throwaway home: HOME, the XDG dirs, each agent's own
// config-dir override, and TMUX_TMPDIR all point inside a fresh temp
// dir, and TMUX is unset so nothing reaches the user's tmux server.
// Nothing ccmux reads resolves its paths before this runs (it happens
// in the root command's PersistentPreRunE, ahead of every mode).

// sandboxEnv is the environment enterSandbox points at dir, relative
// paths joined onto it. Every variable ccmux or an agent config
// package uses to find a directory it may write to belongs here.
var sandboxEnv = []struct{ key, rel string }{
	{"HOME", "."},
	{"XDG_CONFIG_HOME", ".config"},
	{"XDG_STATE_HOME", ".local/state"},
	{"XDG_DATA_HOME", ".local/share"},
	{"XDG_CACHE_HOME", ".cache"},
	{"CLAUDE_CONFIG_DIR", ".claude"},
	{"CODEX_HOME", ".codex"},
	{"CURSOR_HOME", ".cursor"},
	{"ANTIGRAVITY_HOME", ".gemini/antigravity"},
	{"TMUX_TMPDIR", "tmux"},
}

// sandboxUnset are variables that would let a crawl reach real state
// despite the redirected dirs: $TMUX names the user's tmux server
// directly, bypassing TMUX_TMPDIR.
var sandboxUnset = []string{"TMUX", "TMUX_PANE"}

// enterSandbox creates a throwaway home and points this process's
// environment at it. The returned cleanup stops any tmux server a
// crawl started there and removes the directory.
func enterSandbox() (dir string, cleanup func(), err error) {
	dir, err = os.MkdirTemp("", "ccmux-crawl-")
	if err != nil {
		return "", nil, fmt.Errorf("create crawl sandbox: %w", err)
	}
	if dir, err = filepath.EvalSymlinks(dir); err != nil {
		return "", nil, err
	}
	for _, e := range sandboxEnv {
		p := filepath.Join(dir, e.rel)
		if err := os.MkdirAll(p, 0o700); err != nil {
			return "", nil, fmt.Errorf("create crawl sandbox: %w", err)
		}
		if err := os.Setenv(e.key, p); err != nil {
			return "", nil, err
		}
	}
	for _, k := range sandboxUnset {
		_ = os.Unsetenv(k)
	}
	cleanup = func() {
		// A drained scenario command may have started a tmux server in
		// the sandbox; don't leave it running once its socket dir is
		// gone. TMUX_TMPDIR still points into the sandbox and TMUX is
		// unset, so this can only reach that server.
		if matches, _ := filepath.Glob(filepath.Join(dir, "tmux", "tmux-*", "default")); len(matches) > 0 {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = exec.CommandContext(ctx, "tmux", "kill-server").Run()
			cancel()
		}
		_ = os.RemoveAll(dir)
	}
	return dir, cleanup, nil
}

// sandboxCleanup is set by setupSandbox and run by main once the mode
// finishes. A crash exits early and leaves the directory for
// inspection — its path was printed at the start.
var sandboxCleanup = func() {}

// setupSandbox is the root command's PersistentPreRunE.
func setupSandbox(w io.Writer) error {
	if useRealHome {
		fmt.Fprintln(w, "ccmux-crawl: --use-real-home — random keys may rewrite your ccmux, Claude Code and Codex config")
		return nil
	}
	dir, cleanup, err := enterSandbox()
	if err != nil {
		return err
	}
	sandboxCleanup = cleanup
	fmt.Fprintf(w, "ccmux-crawl: sandbox HOME=%s (removed on exit; --use-real-home to crawl your real config)\n", dir)
	return nil
}
