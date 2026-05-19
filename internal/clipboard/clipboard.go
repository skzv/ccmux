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

// EnableTmuxClipboard applies ccmux's tmux-side clipboard config:
// turns on OSC 52 forwarding, replaces the harsh-yellow default
// mode-style with the ccmux mauve, and rebinds MouseDragEnd1Pane so
// dragging-to-select keeps the highlight on release instead of
// blanking it the instant the mouse comes up. All best-effort — a
// failure on any one call doesn't abort the others; the worst case is
// vanilla tmux behavior, which is what the user had before.
//
// Idempotent — rerunning is cheap; there's no "off" mode worth
// supporting (the user can override via their own ~/.tmux.conf).
//
// Three commands, mirrored exactly in TmuxClipboardCommands() so tests
// can pin the contract without standing up a real tmux server.
func EnableTmuxClipboard(ctx context.Context) error {
	var firstErr error
	for _, argv := range TmuxClipboardCommands() {
		if err := exec.CommandContext(ctx, argv[0], argv[1:]...).Run(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// TmuxClipboardCommands returns the argv vectors EnableTmuxClipboard
// invokes, in order. Pure data — no exec, no context, no globals —
// so tests can pin both the contents and the order without booting
// tmux.
//
// Why each command:
//
//   - `set -s set-clipboard on` — server option. Tells tmux to emit
//     OSC 52 escape sequences whenever something lands in its paste
//     buffer. The terminal at the other end of the SSH/local pipe is
//     what actually puts the bytes on the system clipboard; whether
//     that works depends on the terminal (see TerminalSupport). Use
//     `-s` so attaching from a different terminal doesn't lose it.
//
//   - `set -g mode-style ...` — global session option. The default
//     mode-style is bg=yellow,fg=black, which makes selections look
//     like a screaming highlighter. Match the ccmux mauve so the
//     selection reads as part of the chrome instead of a warning.
//
//   - `bind -T copy-mode-vi MouseDragEnd1Pane send -X copy-pipe-no-clear "ccmux clipboard-pipe"` —
//     global keytable binding. Tmux's default fires
//     `copy-pipe-and-cancel` on mouse-release, which immediately exits
//     copy-mode and blanks the visual selection. Users coming from
//     native-terminal selection expect the highlight to stay until
//     they explicitly leave copy-mode (Esc / q). The `-no-clear`
//     variant copies AND keeps the highlight; OSC 52 forwarding still
//     fires because `set-clipboard on` is what wires that, not the
//     binding's flavor.
//
//     The trailing `"ccmux clipboard-pipe"` is the runtime-dispatch
//     hook (see pipe.go). It receives the selection on stdin and
//     decides per-invocation whether to pipe it through pbcopy /
//     wl-copy / xclip — based on whether any *local* tmux client is
//     currently attached. Remote clients (SSH/Mosh) rely on OSC 52
//     which already travels back through their SSH pipe; running
//     pbcopy on the daemon machine for them would silently poison
//     the wrong clipboard. Dispatching at copy time rather than at
//     binding time lets a single daemon serve mixed local + remote
//     attaches correctly.
func TmuxClipboardCommands() [][]string {
	return [][]string{
		{"tmux", "set", "-s", "set-clipboard", "on"},
		// bg/fg are the same accent + base from tmuxchrome.Options so the
		// selection reads as part of the ccmux chrome. Hard-coded here
		// (not imported from tmuxchrome) to keep this package leaf-free
		// and avoid a cycle if tmuxchrome ever wants to call into here.
		{"tmux", "set", "-g", "mode-style", "bg=#cba6f7,fg=#1e1e2e"},
		{"tmux", "bind-key", "-T", "copy-mode-vi", "MouseDragEnd1Pane",
			"send-keys", "-X", "copy-pipe-no-clear", "ccmux clipboard-pipe"},
	}
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
	Program     string // matches $TERM_PROGRAM
	Name        string // pretty name for output
	Supported   bool   // OSC 52 writes work
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
		Program:     "iTerm.app",
		Name:        "iTerm2",
		Supported:   true,
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
		Advice:    "Terminal.app does not implement OSC 52 writes. Local copies still reach the macOS clipboard — ccmux pipes the selection through pbcopy when it sees a local tmux client. Cross-device clipboard over SSH/Mosh still needs an OSC 52 terminal — install iTerm2 (`brew install --cask iterm2`) or Ghostty (`brew install --cask ghostty`).",
	},
	{
		Program:     "vscode",
		Name:        "VS Code's integrated terminal",
		Supported:   true,
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
// wizard's clipboard step. Mirrors what EnableTmuxClipboard /
// TmuxClipboardCommands apply at runtime; tests pin the parity so a
// drift here is loud.
//
// We don't write this to the user's file ourselves at install time —
// touching ~/.tmux.conf is too invasive a default. Setup wizard offers
// it with consent.
func SuggestTmuxConf() string {
	return `# === ccmux clipboard (OSC 52) ===
# Forward selections out via OSC 52 so the local terminal's clipboard
# is updated even when you're attached to a remote tmux over SSH.
set -s set-clipboard on

# Don't blind anyone with the default highlighter-yellow selection.
# Match ccmux's mauve accent so the selection reads as chrome.
set -g mode-style bg=#cba6f7,fg=#1e1e2e

# Mouse-drag to select: keep the highlight visible after release
# (copy-pipe-no-clear) instead of vanishing immediately. The selection
# still lands on the system clipboard via 'set-clipboard on' above —
# that wire is independent of which copy-pipe variant runs.
#
# The trailing 'ccmux clipboard-pipe' is the local-clipboard fallback
# for terminals that don't honor OSC 52 (Terminal.app, most notably).
# It inspects the currently-attached tmux clients at copy time and
# routes the selection to pbcopy / wl-copy / xclip ONLY when at least
# one local (non-SSH) client is attached, so remote attaches don't
# end up with their selection landing on the daemon machine's
# clipboard. Safe to drop the trailing argument if you only ever use
# this tmux from one machine — the OSC 52 path still works on its own.
bind-key -T copy-mode-vi MouseDragEnd1Pane send-keys -X copy-pipe-no-clear "ccmux clipboard-pipe"

# Keyboard yank still cancels copy-mode so you can resume typing
# without an extra Escape.
bind-key -T copy-mode-vi y send-keys -X copy-pipe-and-cancel
# === /ccmux clipboard ===
`
}
