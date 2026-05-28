//go:build integration

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// TestTUIAgents_CodexReasoningEffortKeyPersists exercises the keyboard
// workflow that writes Codex's reasoning effort from the TUI:
//
//  1. The TUI boots into Sessions (the only display assertion the test
//     makes — tab names are public app vocabulary, fluid section
//     labels are not).
//  2. `5` switches to Agents, `l` advances to the Codex sub-tab,
//     `r` cycles the effort.
//  3. The Codex `config.toml` on disk reflects the new value.
//
// The test deliberately avoids waiting on per-sub-tab display strings
// ("Default model", "Codex configuration", etc.). Those are
// design-system content that the TUI redesign work churns through
// regularly — every time a label tweaks, a content-coupled e2e wait
// breaks even though the underlying workflow is fine. The only
// outcome that matters here is the file write, so the test sends the
// key sequence with a short inter-key delay and polls the file as
// the sole behavioural assertion.
func TestTUIAgents_CodexReasoningEffortKeyPersists(t *testing.T) {
	e := newEnv(t)
	cfg := e.defaultConfig()
	cfg.Tour.Shown = true
	cfg.Update.AutoCheck = false
	e.writeConfig(cfg)

	codexHome := filepath.Join(e.Home, ".codex")
	writeFile(t, filepath.Join(codexHome, "config.toml"), "model_reasoning_effort = \"medium\"\n")

	cmd := exec.Command(builtCcmux)
	cmd.Dir = e.Home
	cmd.Env = append(os.Environ(),
		"CODEX_HOME="+codexHome,
		"TERM=xterm-256color",
	)
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 40, Cols: 120})
	if err != nil {
		t.Fatalf("start ccmux TUI: %v", err)
	}
	defer func() {
		_ = f.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_, _ = cmd.Process.Wait()
	}()

	var output safeBuffer
	copyDone := make(chan struct{})
	go copyTerminalOutput(f, &output, copyDone)

	// Single sync point: wait for the TUI to fully render the
	// initial Sessions screen. After that we drive the workflow with
	// timed key sends and assert on the on-disk outcome.
	waitForTUI(t, &output, "Sessions")

	// `5` → Agents · default sub-tab is Claude.
	// `l` → cycle to Codex sub-tab (h/l cycle agents).
	// `r` → advance reasoning effort (medium → low).
	for _, k := range []string{"5", "l", "r"} {
		writeTTY(t, f, k)
		time.Sleep(150 * time.Millisecond)
	}

	configPath := filepath.Join(codexHome, "config.toml")
	if !waitFor(5*time.Second, func() bool {
		return strings.Contains(readFile(t, configPath), `model_reasoning_effort = "low"`)
	}) {
		t.Fatalf("Codex reasoning effort did not advance through TUI r key; config:\n%s\n\nTUI output:\n%s", readFile(t, configPath), output.String())
	}

	writeTTY(t, f, "\x03")
	select {
	case <-copyDone:
	case <-time.After(3 * time.Second):
		t.Fatal("ccmux TUI did not exit after ctrl+c")
	}
}

func waitForTUI(t *testing.T, output *safeBuffer, want string) {
	t.Helper()
	if !waitFor(5*time.Second, func() bool {
		return strings.Contains(output.String(), want)
	}) {
		t.Fatalf("TUI output never contained %q; output:\n%s", want, output.String())
	}
}

func waitForTUIWithInput(t *testing.T, f *os.File, output *safeBuffer, want, input string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(output.String(), want) {
			return
		}
		writeTTY(t, f, input)
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("TUI output never contained %q after input %q; output:\n%s", want, input, output.String())
}

func writeTTY(t *testing.T, f *os.File, s string) {
	t.Helper()
	if _, err := f.Write([]byte(s)); err != nil {
		t.Fatalf("write TTY %q: %v", s, err)
	}
}

func copyTerminalOutput(f *os.File, output *safeBuffer, done chan<- struct{}) {
	defer close(done)
	buf := make([]byte, 4096)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			_, _ = output.Write(chunk)
			text := string(chunk)
			if strings.Contains(text, "\x1b]11;?\x1b\\") {
				_, _ = f.Write([]byte("\x1b]11;rgb:0000/0000/0000\x1b\\"))
			}
			if strings.Contains(text, "\x1b[6n") {
				_, _ = f.Write([]byte("\x1b[1;1R"))
			}
		}
		if err != nil {
			return
		}
	}
}
