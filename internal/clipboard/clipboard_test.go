package clipboard

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

// TestWriteOSC52_FrameAndPayload pins the exact wire format ccmux
// emits. If the framing drifts (e.g. someone "cleans up" the BEL
// terminator into a CSI), every clipboard write silently breaks —
// terminals will just drop the sequence. This test makes that loud.
func TestWriteOSC52_FrameAndPayload(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteOSC52(&buf, "hello world"); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if !strings.HasPrefix(got, "\x1b]52;c;") {
		t.Errorf("missing ESC ]52;c; prefix: %q", got)
	}
	if !strings.HasSuffix(got, "\x07") {
		t.Errorf("missing BEL terminator: %q", got)
	}
	// Decode the middle bit and check the payload matches.
	mid := strings.TrimPrefix(got, "\x1b]52;c;")
	mid = strings.TrimSuffix(mid, "\x07")
	decoded, err := base64.StdEncoding.DecodeString(mid)
	if err != nil {
		t.Fatalf("payload not valid base64: %v (raw=%q)", err, mid)
	}
	if string(decoded) != "hello world" {
		t.Errorf("payload = %q, want %q", decoded, "hello world")
	}
}

// TestWriteOSC52_BinarySafe — base64 is binary-safe so we shouldn't
// barf on bytes that would otherwise be meaningful in shell or escape
// contexts (null, ESC, BEL, newlines).
func TestWriteOSC52_BinarySafe(t *testing.T) {
	var buf bytes.Buffer
	tricky := "a\x00b\x1bc\x07d\ne"
	if err := WriteOSC52(&buf, tricky); err != nil {
		t.Fatal(err)
	}
	// Decode and compare.
	mid := strings.TrimSuffix(strings.TrimPrefix(buf.String(), "\x1b]52;c;"), "\x07")
	dec, _ := base64.StdEncoding.DecodeString(mid)
	if string(dec) != tricky {
		t.Errorf("round-trip mismatch:\n got  %q\n want %q", dec, tricky)
	}
}

// TestProbe_PayloadShapeIsRecognizable — the probe's payload format
// has to be something the user can tell apart from arbitrary text. We
// pin the prefix so a future "cleanup" doesn't accidentally make the
// probe paste as just a number.
func TestProbe_PayloadShapeIsRecognizable(t *testing.T) {
	var buf bytes.Buffer
	payload, err := Probe(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(payload, "ccmux-clipboard-test-") {
		t.Errorf("probe payload prefix changed: %q", payload)
	}
	// Probe should also have written an OSC 52 sequence to the buf.
	if !strings.HasPrefix(buf.String(), "\x1b]52;c;") {
		t.Errorf("probe didn't emit OSC 52: %q", buf.String())
	}
}

// TestDetectTerminal_KnownPrograms covers the curated TERM_PROGRAM
// rows. The Apple_Terminal case is the one users will hit most often
// and is the load-bearing assertion — if doctor stops warning about
// Terminal.app, it'll silently mislead people.
func TestDetectTerminal_KnownPrograms(t *testing.T) {
	cases := []struct {
		env       string
		wantName  string
		supported bool
	}{
		{"iTerm.app", "iTerm2", true},
		{"ghostty", "Ghostty", true},
		{"WezTerm", "WezTerm", true},
		{"Apple_Terminal", "Terminal.app", false},
		{"vscode", "VS Code's integrated terminal", true},
	}
	for _, tc := range cases {
		t.Run(tc.env, func(t *testing.T) {
			t.Setenv("TERM_PROGRAM", tc.env)
			got := DetectTerminal()
			if got.Name != tc.wantName {
				t.Errorf("Name = %q, want %q", got.Name, tc.wantName)
			}
			if got.Supported != tc.supported {
				t.Errorf("Supported = %v, want %v", got.Supported, tc.supported)
			}
		})
	}
}

// TestDetectTerminal_UnknownProgram — when ccmux hasn't tested the
// terminal, we should report unknown rather than guess "supported".
func TestDetectTerminal_UnknownProgram(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "imaginary-term-9000")
	got := DetectTerminal()
	if got.Supported {
		t.Errorf("untested terminal should default to Supported=false: %+v", got)
	}
	if !strings.Contains(got.Name, "imaginary-term-9000") {
		t.Errorf("unknown program name should include the env value: %q", got.Name)
	}
}

// TestDetectTerminal_Unset — empty $TERM_PROGRAM (e.g. ssh into a
// machine without one set) is its own diagnostic.
func TestDetectTerminal_Unset(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "")
	got := DetectTerminal()
	if got.Supported {
		t.Errorf("unset TERM_PROGRAM should not be Supported")
	}
	if !strings.Contains(got.Name, "unknown") {
		t.Errorf("unset terminal should report unknown: %q", got.Name)
	}
}

// TestSuggestTmuxConf_HasKeyDirectives — pins the contents users will
// paste into ~/.tmux.conf. Drift here means surveys of new ccmux users
// would land different snippets in their dotfiles.
func TestSuggestTmuxConf_HasKeyDirectives(t *testing.T) {
	out := SuggestTmuxConf()
	musts := []string{
		"set -s set-clipboard on",
		// Don't blind the user with default highlighter-yellow.
		"mode-style",
		// Mouse-drag: keep highlight on release (the user-reported
		// "selection vanishes" bug). copy-pipe-no-clear, not -and-cancel.
		"MouseDragEnd1Pane",
		"copy-pipe-no-clear",
		// Runtime-dispatch hook for local-clipboard fallback (Terminal.app).
		"ccmux clipboard-pipe",
		// Keyboard yank still cancels (so user can resume typing).
		"copy-pipe-and-cancel",
		"# === ccmux clipboard",
		"# === /ccmux clipboard ===",
	}
	for _, m := range musts {
		if !strings.Contains(out, m) {
			t.Errorf("tmux conf snippet missing %q:\n%s", m, out)
		}
	}
}

// TestTmuxClipboardCommands_AppliesAllThreeFixes pins the argv vectors
// EnableTmuxClipboard runs against tmux. Three directives, in order:
//
//  1. set -s set-clipboard on   — OSC 52 forwarding (the existing fix)
//  2. set -g mode-style ...     — replaces ugly yellow selection
//  3. bind MouseDragEnd1Pane    — selection persists after mouse release
//
// Each one corresponds to a symptom the user hit: yellow color, vanish-
// on-release, and copy-not-reaching-system-clipboard. Locking these in
// means a future "clean up the clipboard package" refactor that drops
// any of the three regresses the bug we just shipped a fix for.
func TestTmuxClipboardCommands_AppliesAllThreeFixes(t *testing.T) {
	cmds := TmuxClipboardCommands()
	if len(cmds) != 3 {
		t.Fatalf("len = %d, want 3 (set-clipboard, mode-style, MouseDragEnd1Pane)", len(cmds))
	}

	// Each row is an argv. First element is always "tmux".
	for i, argv := range cmds {
		if len(argv) == 0 {
			t.Fatalf("cmds[%d] is empty", i)
		}
		if argv[0] != "tmux" {
			t.Errorf("cmds[%d][0] = %q, want tmux", i, argv[0])
		}
	}

	// Flatten for substring-based assertions on each row.
	join := func(argv []string) string { return strings.Join(argv, " ") }

	// 1. set-clipboard on (server option).
	if got := join(cmds[0]); !strings.Contains(got, "set -s set-clipboard on") {
		t.Errorf("cmds[0] should enable set-clipboard server-wide, got: %q", got)
	}

	// 2. mode-style overrides the harsh default. Must be a global (-g)
	// set so it applies to every session, and must NOT contain "yellow"
	// (which would be regression to default).
	if got := join(cmds[1]); !strings.Contains(got, "set -g mode-style") {
		t.Errorf("cmds[1] should set mode-style globally, got: %q", got)
	}
	if got := join(cmds[1]); strings.Contains(strings.ToLower(got), "yellow") {
		t.Errorf("cmds[1] should NOT use yellow (the broken default), got: %q", got)
	}

	// 3. MouseDragEnd1Pane binding under copy-mode-vi must use
	// copy-pipe-no-clear so the highlight persists after release.
	// copy-pipe-and-cancel here would be the bug we're fixing.
	mouseBinding := join(cmds[2])
	musts := []string{
		"bind-key",
		"copy-mode-vi",
		"MouseDragEnd1Pane",
		"copy-pipe-no-clear",
		// The runtime-dispatch hook: tmux pipes the selection through
		// `ccmux clipboard-pipe` which decides per-invocation whether
		// to route to pbcopy / wl-copy / xclip. Without this trailing
		// arg, Terminal.app users get nothing from the local clipboard
		// because Terminal.app silently drops OSC 52 writes.
		"ccmux clipboard-pipe",
	}
	for _, want := range musts {
		if !strings.Contains(mouseBinding, want) {
			t.Errorf("cmds[2] should contain %q, got: %q", want, mouseBinding)
		}
	}
	if strings.Contains(mouseBinding, "copy-pipe-and-cancel") {
		t.Errorf("cmds[2] uses copy-pipe-and-cancel — that's the bug. Want copy-pipe-no-clear.\nGot: %q", mouseBinding)
	}
	// The pipe hook must be the LAST argv element — tmux's
	// copy-pipe-no-clear treats its trailing optional arg as the
	// shell command. Putting it anywhere else makes tmux read it as
	// a flag and the dispatch silently breaks.
	if got := cmds[2][len(cmds[2])-1]; got != "ccmux clipboard-pipe" {
		t.Errorf("last arg = %q, want %q", got, "ccmux clipboard-pipe")
	}
}

// TestSuggestTmuxConf_MatchesAppliedConfig — the snippet ccmux pastes
// into the user's ~/.tmux.conf and the live tmux commands ccmuxd
// invokes on startup must agree on the load-bearing directives, or a
// user who follows the snippet gets a different experience than ccmux
// applies. Pin parity on the keywords that drive the fix.
func TestSuggestTmuxConf_MatchesAppliedConfig(t *testing.T) {
	conf := SuggestTmuxConf()
	for _, argv := range TmuxClipboardCommands() {
		joined := strings.Join(argv, " ")
		// Strip the leading "tmux " — the conf file syntax omits it.
		joined = strings.TrimPrefix(joined, "tmux ")
		// Pick a stable signature substring per command rather than the
		// whole argv, since conf-file syntax is slightly different (e.g.
		// `bind-key -T copy-mode-vi MouseDragEnd1Pane send-keys -X X`
		// matches both representations on the keyword "copy-pipe-no-clear").
		// We assert each command has a recognizable substring in the conf.
		var key string
		switch {
		case strings.Contains(joined, "set-clipboard"):
			key = "set-clipboard on"
		case strings.Contains(joined, "mode-style"):
			key = "mode-style"
		case strings.Contains(joined, "MouseDragEnd1Pane"):
			key = "copy-pipe-no-clear"
		default:
			t.Errorf("unrecognized command in TmuxClipboardCommands: %q — add a parity check", joined)
			continue
		}
		if !strings.Contains(conf, key) {
			t.Errorf("SuggestTmuxConf missing %q (drift from TmuxClipboardCommands)", key)
		}
	}
}
