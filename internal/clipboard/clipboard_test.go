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
