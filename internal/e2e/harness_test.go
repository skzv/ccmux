//go:build integration

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
)

// Built binaries, populated once by buildBinaries (called from TestMain).
var (
	binDir      string
	builtCcmux  string
	builtCcmuxd string
)

// stubBinDir holds fake `claude`/`codex` agent executables — see
// installStubAgents. Populated once by TestMain.
var stubBinDir string

// repoRoot returns the absolute path of the repository root, derived
// from this source file's location (internal/e2e/harness_test.go).
func repoRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("e2e: cannot resolve caller")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

// buildBinaries compiles ccmux and ccmuxd into a temp dir. The build
// is the plain shipped artifact — no integration tag — so the tests
// exercise exactly what users run.
func buildBinaries() error {
	dir, err := os.MkdirTemp("", "ccmux-e2e-bin")
	if err != nil {
		return err
	}
	binDir = dir
	builtCcmux = filepath.Join(dir, "ccmux")
	builtCcmuxd = filepath.Join(dir, "ccmuxd")
	root := repoRoot()
	for _, b := range []struct{ out, pkg string }{
		{builtCcmux, "./cmd/ccmux"},
		{builtCcmuxd, "./cmd/ccmuxd"},
	} {
		cmd := exec.Command("go", "build", "-o", b.out, b.pkg)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("build %s: %v\n%s", b.pkg, err, out)
		}
	}
	return nil
}

// installStubAgents writes stub `claude`, `codex`, and `agy`
// (antigravity / gemini) executables into a temp dir. ccmux launches
// the configured agent by bare name ("claude" / "codex" / "agy"), which
// tmux resolves through PATH; on a CI runner no such binary exists, so
// the agent command exits instantly and tmux tears the session down
// before a test can observe it. The stub stands in for
// a real agent: it echoes `ccmux-stub-agent=<name>` (so a test can
// assert which agent a session launched) then sleeps, keeping the pane
// — and therefore the session — alive exactly like an agent waiting for
// input. newEnv prepends stubBinDir to PATH so every spawned session
// resolves these, making the suite hermetic instead of silently
// depending on the dev's PATH.
func installStubAgents() error {
	dir, err := os.MkdirTemp("", "ccmux-e2e-agents")
	if err != nil {
		return err
	}
	stubBinDir = dir
	const stub = `#!/bin/sh
# ccmux e2e stub agent: announce which agent ran (so a test can assert
# which agent a session launched), then stay alive like an agent
# awaiting input so the tmux session persists.
echo "ccmux-stub-agent=$(basename "$0")"
exec sleep 86400
`
	for _, name := range []string{"claude", "codex", "agy"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(stub), 0o755); err != nil {
			return err
		}
	}
	return nil
}

// requireTmux skips the calling test when tmux is not installed.
func requireTmux(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed — skipping e2e test")
	}
}

// safeBuffer is a goroutine-safe bytes.Buffer for capturing a child
// process's output while the parent may read it concurrently.
type safeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// Env is one hermetic e2e fixture: an isolated $HOME, projects root,
// tmux server (via TMUX_TMPDIR), and ccmux config. Construct it with
// newEnv; teardown is registered automatically.
type Env struct {
	t      *testing.T
	Home   string // temp $HOME — config + daemon socket live under here
	Root   string // temp projects root
	daemon *daemonProc
}

// shortTempDir creates a temp directory under a short base path. The
// platform TempDir on macOS (/var/folders/…) is long enough that a
// nested Unix socket path (TMUX_TMPDIR's tmux socket, ccmuxd's socket)
// blows past the ~104-char sockaddr_un limit. /tmp keeps paths short.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ccme")
	if err != nil {
		t.Fatalf("create sandbox dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// newEnv builds a fresh hermetic fixture. It sets HOME and TMUX_TMPDIR
// for the current test (so config, the daemon socket, and the tmux
// server are all sandboxed) and writes a default ccmux config.
func newEnv(t *testing.T) *Env {
	t.Helper()
	requireTmux(t)
	home := shortTempDir(t)
	// HOME isolates config (~/.config/ccmux) and the daemon socket
	// (~/.local/state/ccmux). TMUX_TMPDIR isolates the tmux server so
	// every test gets its own — no collisions with the user's real
	// sessions or with other tests.
	t.Setenv("HOME", home)
	t.Setenv("TMUX_TMPDIR", home)
	// Resolve `claude`/`codex` to the sleeping stub (installStubAgents).
	// Prepend so the stub wins over any real agent on the dev's PATH;
	// the rest of PATH (tmux, sh, the go toolchain) still resolves.
	t.Setenv("PATH", stubBinDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	root := filepath.Join(home, "Projects")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir projects root: %v", err)
	}
	e := &Env{t: t, Home: home, Root: root}
	e.writeConfig(e.defaultConfig())
	t.Cleanup(e.cleanup)
	return e
}

// defaultConfig returns the baseline config for a fixture: defaults,
// but with the projects root pinned to the sandbox and a fast poll
// interval so daemon tests don't wait on a 2s wall-clock tick.
func (e *Env) defaultConfig() config.Config {
	cfg := config.Defaults()
	cfg.Projects.Root = e.Root
	cfg.Daemon.PollIntervalSeconds = 1
	cfg.Daemon.IdleSecondsForNeedsInput = 1
	return cfg
}

// writeConfig persists cfg to the sandbox's ~/.config/ccmux/config.toml.
func (e *Env) writeConfig(cfg config.Config) {
	e.t.Helper()
	if err := config.Save(cfg); err != nil {
		e.t.Fatalf("save config: %v", err)
	}
}

// cleanup tears the fixture down: stop the daemon, kill the isolated
// tmux server. Registered via t.Cleanup so it runs before t.Setenv
// restores the environment.
func (e *Env) cleanup() {
	if e.daemon != nil {
		e.daemon.stop()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "tmux", "kill-server").Run()
}

// ccmux runs the built ccmux binary with the sandbox environment and
// $HOME as the working directory. Returns stdout, stderr, and the run
// error (an *exec.ExitError for a non-zero exit).
func (e *Env) ccmux(args ...string) (stdout, stderr string, err error) {
	return e.ccmuxIn(e.Home, args...)
}

// ccmuxIn is ccmux with an explicit working directory — needed for
// cwd-sensitive commands like `ccmux upgrade`.
func (e *Env) ccmuxIn(dir string, args ...string) (stdout, stderr string, err error) {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, builtCcmux, args...)
	cmd.Dir = dir
	var out, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errBuf
	err = cmd.Run()
	return out.String(), errBuf.String(), err
}

// tmux runs the raw tmux CLI against the isolated server. Used for
// test assertions and fixture setup, never as the code under test.
func (e *Env) tmux(args ...string) (string, error) {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", args...).CombinedOutput()
	return string(out), err
}

// newTmuxSession starts a detached session named `name` in `dir` on
// the isolated server, running a bare shell. Fixture setup helper.
func (e *Env) newTmuxSession(name, dir string) {
	e.t.Helper()
	if dir == "" {
		dir = e.Home
	}
	if out, err := e.tmux("new-session", "-d", "-s", name, "-c", dir); err != nil {
		e.t.Fatalf("new tmux session %q: %v\n%s", name, err, out)
	}
}

// sessionNames returns every session name on the isolated tmux server,
// sorted. An empty server yields an empty slice (not an error).
func (e *Env) sessionNames() []string {
	e.t.Helper()
	out, err := e.tmux("list-sessions", "-F", "#{session_name}")
	if err != nil {
		// tmux exits 1 when there is no running server or no sessions
		// (the server shuts down once its last session is gone) — both
		// mean "empty list", not a harness failure.
		return nil
	}
	var names []string
	for _, ln := range strings.Split(strings.TrimSpace(out), "\n") {
		if ln != "" {
			names = append(names, ln)
		}
	}
	sort.Strings(names)
	return names
}

// hasSession reports whether a session by that name exists.
func (e *Env) hasSession(name string) bool {
	for _, n := range e.sessionNames() {
		if n == name {
			return true
		}
	}
	return false
}

// capturePane returns the visible content of a session's active pane.
func (e *Env) capturePane(name string) string {
	e.t.Helper()
	out, err := e.tmux("capture-pane", "-p", "-t", name)
	if err != nil {
		e.t.Fatalf("capture-pane %q: %v\n%s", name, err, out)
	}
	return out
}

// daemonProc is a running ccmuxd child process under an Env.
type daemonProc struct {
	cmd *exec.Cmd
	log *safeBuffer
}

// startDaemon spawns ccmuxd in the sandbox and blocks until it answers
// a health probe (or fails the test on timeout).
func (e *Env) startDaemon() *daemonProc {
	e.t.Helper()
	cmd := exec.Command(builtCcmuxd)
	cmd.Dir = e.Home
	logBuf := &safeBuffer{}
	cmd.Stdout, cmd.Stderr = logBuf, logBuf
	if err := cmd.Start(); err != nil {
		e.t.Fatalf("start ccmuxd: %v", err)
	}
	d := &daemonProc{cmd: cmd, log: logBuf}
	e.daemon = d

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		cli, err := daemon.LocalClient()
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			_, herr := cli.Health(ctx)
			cancel()
			if herr == nil {
				return d
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	e.t.Fatalf("ccmuxd did not become ready within timeout; log:\n%s", logBuf.String())
	return nil
}

// stop terminates the daemon: SIGTERM, then SIGKILL if it lingers.
func (d *daemonProc) stop() {
	if d == nil || d.cmd == nil || d.cmd.Process == nil {
		return
	}
	_ = d.cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() { _, _ = d.cmd.Process.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = d.cmd.Process.Kill()
		<-done
	}
}

// localClient returns a daemon client for the sandbox's Unix socket.
func (e *Env) localClient() *daemon.Client {
	e.t.Helper()
	cli, err := daemon.LocalClient()
	if err != nil {
		e.t.Fatalf("local daemon client: %v", err)
	}
	return cli
}

// waitFor polls `cond` every 50ms until it returns true or `timeout`
// elapses. Returns false on timeout. Used instead of a fixed sleep so
// daemon-observation tests are deterministic and fast.
func waitFor(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return cond()
}
