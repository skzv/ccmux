// Package clipboard wires up cross-device copy/paste for ccmux.
//
// The mechanism is OSC 52 — a terminal escape sequence ESC ] 52 ; c ;
// <base64-payload> BEL that asks the terminal emulator to put
// <payload> into the local clipboard. tmux can be told to *forward*
// any selection made inside it as an OSC 52 sequence on stdout (via
// `set -s set-clipboard on`), and a compatible terminal at the other
// end of the ssh pipe writes it to the system clipboard. The net
// effect: selecting text inside a tmux pane on the Mac mini lands on
// the laptop's clipboard, no network round-trip beyond the SSH
// channel itself.
//
// The big gotcha: not every terminal honors OSC 52 writes by default.
//
//   - iTerm2 — yes, but Preferences → General → Selection → "Applications
//     in terminal may access clipboard" must be checked.
//   - Ghostty — yes, default on.
//   - WezTerm, Alacritty, kitty — yes.
//   - Terminal.app — no. macOS Terminal.app does not implement OSC 52
//     writes as of macOS Sequoia (15.x). Users on Terminal.app need to
//     switch terminals for cross-device clipboard to work.
//
// This package owns the tmux-side enable, the OSC 52 probe sequence,
// and the terminal-compat hint that doctor + the setup wizard surface.
package clipboard

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// EnableTmuxClipboard turns on `set-clipboard` server-wide so every
// session ccmux interacts with emits OSC 52 on copy. Idempotent —
// rerunning is cheap and there's no "off" mode worth supporting here
// (the user can disable via their own ~/.tmux.conf if needed).
//
// We use `set -s` (server option) rather than per-session so attaching
// from a different terminal doesn't lose the setting.
func EnableTmuxClipboard(ctx context.Context) error {
	return exec.CommandContext(ctx, "tmux", "set", "-s", "set-clipboard", "on").Run()
}

// TmuxClipboardState returns the current value of `set-clipboard` for
// the running tmux server. Returns "off"/"on"/"external", or an error
// if tmux isn't running.
func TmuxClipboardState(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "tmux", "show", "-s", "-v", "set-clipboard").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// WriteOSC52 emits an OSC 52 escape sequence that asks the terminal
// to put `payload` into the system clipboard. Writing through `w` —
// typically os.Stdout when running interactively, or a tmux pane file
// descriptor when running via the daemon. Payload is automatically
// base64-encoded per the OSC 52 spec.
//
// Limit: many terminals cap OSC 52 payloads around 4KB-100KB. Long
// payloads are silently truncated on the terminal side; that's the
// user's problem to discover with their specific terminal.
func WriteOSC52(w io.Writer, payload string) error {
	encoded := base64.StdEncoding.EncodeToString([]byte(payload))
	// ESC ] 52 ; c ; <base64> BEL
	//   ] = OSC opening
	//   52 = clipboard
	//   c = primary system clipboard ("c" = clipboard, "p" = primary on
	//       Linux X11; "c" is the only one macOS terminals care about)
	_, err := fmt.Fprintf(w, "\x1b]52;c;%s\x07", encoded)
	return err
}

// Probe writes a recognizable test string to the local clipboard via
// OSC 52 and returns it so the caller can ask the user "paste somewhere
// — does it match?" Useful as the final step of `ccmux doctor` and
// `ccmux setup`'s clipboard wizard.
//
// Payload format is "ccmux-clipboard-test-<random>" so the user can
// tell it apart from whatever they had on the clipboard before.
func Probe(w io.Writer) (string, error) {
	payload := fmt.Sprintf("ccmux-clipboard-test-%d", os.Getpid())
	if err := WriteOSC52(w, payload); err != nil {
		return "", err
	}
	return payload, nil
}

// TerminalSupport describes one terminal program's OSC 52 capabilities
// as far as ccmux knows. Doctor uses this to print actionable advice
// instead of a generic "OSC 52 may not work" warning.
type TerminalSupport struct {
	Program    string // matches $TERM_PROGRAM
	Name       string // pretty name for output
	Supported  bool   // OSC 52 writes work
	NeedsToggle string // empty if no user action needed; otherwise the setting to flip
	Advice      string // free-form additional hint (e.g. "use iTerm2 or Ghostty instead")
}

// terminals enumerates the known terminal emulators ccmux has tested
// OSC 52 against. New entries should go here, not as ad-hoc strings
// inside doctor.
//
// The "Apple_Terminal" entry is the load-bearing one — most macOS
// first-time users hit it and need to be told gently that this is a
// terminal limitation, not a ccmux bug.
var terminals = []TerminalSupport{
	{
		Program:   "iTerm.app",
		Name:      "iTerm2",
		Supported: true,
		NeedsToggle: `Preferences → General → Selection → "Applications in terminal may access clipboard"`,
	},
	{
		Program:   "ghostty",
		Name:      "Ghostty",
		Supported: true,
	},
	{
		Program:   "WezTerm",
		Name:      "WezTerm",
		Supported: true,
	},
	{
		Program:   "alacritty",
		Name:      "Alacritty",
		Supported: true,
	},
	{
		Program:   "kitty",
		Name:      "Kitty",
		Supported: true,
	},
	{
		Program:   "Apple_Terminal",
		Name:      "Terminal.app",
		Supported: false,
		Advice:    "Terminal.app does not implement OSC 52 writes — install iTerm2 (`brew install --cask iterm2`) or Ghostty (`brew install --cask ghostty`) and ccmux's cross-device clipboard will just work.",
	},
	{
		Program:   "vscode",
		Name:      "VS Code's integrated terminal",
		Supported: true,
		NeedsToggle: `settings.json: "terminal.integrated.enableMultiLinePasteWarning": false (and the default xterm.js OSC 52 setting is already on)`,
	},
	{
		Program:   "tmux",
		Name:      "tmux (no outer terminal detected)",
		Supported: false,
		Advice:    "TERM_PROGRAM=tmux — ccmux can't tell which terminal is outside tmux. Whatever it is, make sure it allows OSC 52 writes.",
	},
}

// DetectTerminal returns the TerminalSupport row for the user's
// current terminal, derived from $TERM_PROGRAM. Returns an "unknown"
// row when the program is not in our list — better than guessing.
func DetectTerminal() TerminalSupport {
	prog := os.Getenv("TERM_PROGRAM")
	if prog == "" {
		return TerminalSupport{
			Name:   "unknown terminal ($TERM_PROGRAM unset)",
			Advice: "Set $TERM_PROGRAM in your terminal or check its docs for OSC 52 support.",
		}
	}
	for _, t := range terminals {
		if t.Program == prog {
			return t
		}
	}
	return TerminalSupport{
		Program: prog,
		Name:    prog + " (untested by ccmux)",
		Advice:  "Check your terminal's docs for OSC 52 / clipboard-write support.",
	}
}

// SuggestTmuxConf returns the tmux.conf snippet ccmux suggests
// appending to the user's ~/.tmux.conf when they go through the setup
// wizard's clipboard step. Includes the server option plus copy-mode
// keybindings that pipe to OSC 52.
//
// We don't write this to the user's file ourselves at install time —
// touching ~/.tmux.conf is too invasive a default. Setup wizard offers
// it with consent.
func SuggestTmuxConf() string {
	return `# === ccmux clipboard (OSC 52) ===
# Forward selections out via OSC 52 so the local terminal's clipboard
# is updated even when you're attached to a remote tmux over SSH.
set -s set-clipboard on

# Copy-mode bindings: pressing 'y' (vi mode) or Enter (emacs) yanks the
# selection AND emits OSC 52. Without these, the default copy-pipe
# uses pbcopy/xclip which doesn't traverse SSH.
bind-key -T copy-mode-vi y send-keys -X copy-pipe-and-cancel
bind-key -T copy-mode    MouseDragEnd1Pane send-keys -X copy-pipe-and-cancel
# === /ccmux clipboard ===
`
}
