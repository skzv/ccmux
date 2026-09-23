//go:build !windows

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The CLI harness runs the real cobra tree in a child process (the test
// binary re-executed into TestCLIHelperProcess) with:
//
//   - PATH holding ONLY a fake `tmux` (plus whatever stubs a test adds),
//     so no test ever reaches the user's real tmux server;
//   - an isolated $HOME / XDG dirs, so config, known_hosts, the daemon
//     socket path etc. are all temp;
//   - the fake tmux logging every invocation, one line per call, args
//     joined by "|" — tests assert on exactly what ccmux asked tmux for.
//
// A child process is what makes this safe: `attach`/`new` end in
// syscall.Exec (which would replace the test binary) and `doctor` ends
// in os.Exit.

// fakeTmux logs its argv and emulates just enough of tmux:
// has-session succeeds only for names in $FAKE_TMUX_SESSIONS, and
// list-sessions exits 1 ("no server running"). Everything else succeeds
// silently.
const fakeTmux = `printf '%s|' "$@" >> "$FAKE_TMUX_LOG"
printf '\n' >> "$FAKE_TMUX_LOG"
case "$1" in
has-session)
  for s in $FAKE_TMUX_SESSIONS; do
    [ "$3" = "=$s" ] && exit 0
  done
  exit 1 ;;
list-sessions) exit 1 ;;
esac
exit 0
`

// TestCLIHelperProcess is the child side of cliEnv.run, not a real test.
func TestCLIHelperProcess(t *testing.T) {
	if os.Getenv("CCMUX_CLI_HELPER") != "1" {
		t.Skip("child process for the CLI harness; runs only via cliEnv.run")
	}
	var args []string
	if err := json.Unmarshal([]byte(os.Getenv("CCMUX_CLI_ARGS")), &args); err != nil {
		fmt.Fprintln(os.Stderr, "harness: bad CCMUX_CLI_ARGS:", err)
		os.Exit(3)
	}
	rootCmd.SetArgs(args)
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	os.Exit(0)
}

type cliEnv struct {
	t       *testing.T
	home    string
	bin     string
	tmuxLog string
	env     map[string]string
}

type cliResult struct {
	stdout, stderr string
	code           int
}

func newCLIEnv(t *testing.T) *cliEnv {
	t.Helper()
	// EvalSymlinks: on macOS t.TempDir() is under /var → /private/var,
	// and the child's os.Getwd reports the resolved form.
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := &cliEnv{
		t:       t,
		home:    home,
		bin:     filepath.Join(home, "fakebin"),
		tmuxLog: filepath.Join(home, "tmux.log"),
	}
	for _, d := range []string{e.bin, filepath.Join(home, "tmp")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	e.writeExe("tmux", fakeTmux)
	e.env = map[string]string{
		"HOME":                 home,
		"XDG_CONFIG_HOME":      filepath.Join(home, ".config"),
		"XDG_STATE_HOME":       filepath.Join(home, ".local", "state"),
		"XDG_DATA_HOME":        filepath.Join(home, ".local", "share"),
		"XDG_CACHE_HOME":       filepath.Join(home, ".cache"),
		"TMPDIR":               filepath.Join(home, "tmp"),
		"TMUX_TMPDIR":          filepath.Join(home, "tmp"),
		"PATH":                 e.bin,
		"CCMUX_NO_SETUP_NUDGE": "1",
		"FAKE_TMUX_LOG":        e.tmuxLog,
		"FAKE_TMUX_SESSIONS":   "",
	}
	return e
}

// writeExe drops an executable /bin/sh script named name on the fake PATH.
func (e *cliEnv) writeExe(name, body string) {
	e.t.Helper()
	if err := os.WriteFile(filepath.Join(e.bin, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		e.t.Fatal(err)
	}
}

// mkdir creates dir (relative to $HOME) and returns its absolute path.
func (e *cliEnv) mkdir(rel string) string {
	e.t.Helper()
	p := filepath.Join(e.home, rel)
	if err := os.MkdirAll(p, 0o755); err != nil {
		e.t.Fatal(err)
	}
	return p
}

// run executes `ccmux <args>` in the child with cwd = dir ("" → $HOME).
func (e *cliEnv) run(dir string, args ...string) cliResult {
	e.t.Helper()
	if dir == "" {
		dir = e.home
	}
	rawArgs, _ := json.Marshal(args)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCLIHelperProcess$")
	c.Dir = dir
	c.Env = []string{"CCMUX_CLI_HELPER=1", "CCMUX_CLI_ARGS=" + string(rawArgs), "PWD=" + dir}
	for k, v := range e.env {
		c.Env = append(c.Env, k+"="+v)
	}
	var stdout, stderr bytes.Buffer
	c.Stdout, c.Stderr = &stdout, &stderr
	err := c.Run()
	res := cliResult{stdout: stdout.String(), stderr: stderr.String()}
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		res.code = ee.ExitCode()
	default:
		e.t.Fatalf("run ccmux %v: %v", args, err)
	}
	return res
}

// tmuxCalls returns every fake-tmux invocation so far, e.g.
// "new-session|-d|-s|c-x|-c|/dir|claude --continue|".
func (e *cliEnv) tmuxCalls() []string {
	e.t.Helper()
	b, err := os.ReadFile(e.tmuxLog)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		e.t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

// tmuxCallsWith returns the invocations whose subcommand is verb.
func (e *cliEnv) tmuxCallsWith(verb string) []string {
	var out []string
	for _, c := range e.tmuxCalls() {
		if strings.HasPrefix(c, verb+"|") {
			out = append(out, c)
		}
	}
	return out
}
