package setupwizard

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPathContains pins the colon-list scanner used to decide whether
// ~/.local/bin is already on PATH. Empty inputs and the trailing-colon
// edge case (which produces an empty entry) need to behave sensibly.
func TestPathContains(t *testing.T) {
	cases := []struct {
		pathEnv, dir string
		want         bool
	}{
		{"/usr/bin:/usr/local/bin:/Users/skz/.local/bin", "/Users/skz/.local/bin", true},
		{"/usr/bin:/usr/local/bin", "/Users/skz/.local/bin", false},
		{"/Users/skz/.local/bin", "/Users/skz/.local/bin", true},
		{"", "/Users/skz/.local/bin", false},
		{"/usr/bin", "", false},
		// substring should not match — exact path components only.
		{"/Users/skz/.local/bin-other", "/Users/skz/.local/bin", false},
		// trailing colon produces an empty component; must not match
		// an empty target.
		{"/usr/bin:", "", false},
	}
	for _, tc := range cases {
		if got := pathContains(tc.pathEnv, tc.dir); got != tc.want {
			t.Errorf("pathContains(%q, %q) = %v, want %v", tc.pathEnv, tc.dir, got, tc.want)
		}
	}
}

// TestDetectShellRC covers the shell→rc mapping. We pin both the file
// path and the export-line syntax — fish in particular uses a different
// command (`set -gx`) than the POSIX `export`, and a regression there
// would silently corrupt fish configs.
func TestDetectShellRC(t *testing.T) {
	home := "/Users/test"
	cases := []struct {
		shell, wantPath, wantSyntax string
	}{
		{"/bin/zsh", "/Users/test/.zshrc", `export PATH="/Users/test/.local/bin:$PATH"`},
		{"/usr/bin/zsh", "/Users/test/.zshrc", `export PATH="/Users/test/.local/bin:$PATH"`},
		{"/usr/local/bin/fish", "/Users/test/.config/fish/config.fish", `set -gx PATH /Users/test/.local/bin $PATH`},
		// Unknown shell falls back to ~/.profile + posix export.
		{"/bin/dash", "/Users/test/.profile", `export PATH="/Users/test/.local/bin:$PATH"`},
		{"", "/Users/test/.profile", `export PATH="/Users/test/.local/bin:$PATH"`},
	}
	for _, tc := range cases {
		t.Run(tc.shell, func(t *testing.T) {
			rc, line := detectShellRC(home, tc.shell)
			if rc != tc.wantPath {
				t.Errorf("rc path = %q, want %q", rc, tc.wantPath)
			}
			if line != tc.wantSyntax {
				t.Errorf("export line = %q, want %q", line, tc.wantSyntax)
			}
		})
	}
}

// TestPathAlreadyManaged — the guard comment is what we key off of for
// "skip the append on re-run". If the comment string ever drifts, this
// test catches it before we ship a wizard that appends two PATH blocks.
func TestPathAlreadyManaged(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"empty", "", false},
		{"unrelated rc", "alias ll='ls -la'\n", false},
		{"manual export but no guard", `export PATH="$HOME/.local/bin:$PATH"`, false},
		{"managed block present", "alias ll='ls -la'\n" + rcGuardOpen + "\nexport PATH=\"…\"\n" + rcGuardClose + "\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pathAlreadyManaged(tc.body); got != tc.want {
				t.Errorf("pathAlreadyManaged = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestAppendCcmuxPathBlock_FreshFile — first-time append should produce
// guard-wrapped block with the export line and a trailing newline.
func TestAppendCcmuxPathBlock_FreshFile(t *testing.T) {
	got := appendCcmuxPathBlock("", `export PATH="/u/.local/bin:$PATH"`)
	if !strings.Contains(got, rcGuardOpen) {
		t.Errorf("missing guard open: %q", got)
	}
	if !strings.Contains(got, rcGuardClose) {
		t.Errorf("missing guard close: %q", got)
	}
	if !strings.Contains(got, `export PATH="/u/.local/bin:$PATH"`) {
		t.Errorf("missing export line: %q", got)
	}
}

// TestAppendCcmuxPathBlock_PreservesExistingContent — never clobber the
// user's rc; always append.
func TestAppendCcmuxPathBlock_PreservesExistingContent(t *testing.T) {
	prior := "alias ll='ls -la'\n# user stuff\nexport EDITOR=nvim\n"
	got := appendCcmuxPathBlock(prior, `export PATH="x"`)
	if !strings.HasPrefix(got, prior) {
		t.Errorf("appendCcmuxPathBlock didn't preserve prior content; got:\n%s", got)
	}
}

// TestAppendCcmuxPathBlock_Idempotent — re-running the wizard must not
// append a second block.
func TestAppendCcmuxPathBlock_Idempotent(t *testing.T) {
	first := appendCcmuxPathBlock("alias x='y'\n", `export PATH="x"`)
	second := appendCcmuxPathBlock(first, `export PATH="x"`)
	if first != second {
		t.Errorf("second call should be a no-op:\n first:\n%s\n second:\n%s", first, second)
	}
	if strings.Count(second, rcGuardOpen) != 1 {
		t.Errorf("expected exactly one guard block, got %d", strings.Count(second, rcGuardOpen))
	}
}

// TestAppendCcmuxPathBlock_AddsNewlineSeparator — if the existing body
// doesn't end in a newline, our block must still appear on its own
// line.
func TestAppendCcmuxPathBlock_AddsNewlineSeparator(t *testing.T) {
	got := appendCcmuxPathBlock("no-newline-at-eof", `export PATH="x"`)
	if !strings.Contains(got, "no-newline-at-eof\n"+rcGuardOpen) {
		t.Errorf("expected newline before our guard block:\n%s", got)
	}
}

// TestEnsureCcmuxOnPath_WritesRC is the end-to-end check: build a fake
// home with an empty .zshrc, point SHELL at zsh, then run the helper.
// Verify the rc file now contains a managed block.
func TestEnsureCcmuxOnPath_WritesRC(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/zsh")
	// PATH that does NOT include the install dir, so the helper takes
	// the "needs fixing" branch.
	t.Setenv("PATH", "/usr/bin:/bin")

	var buf bytes.Buffer
	if err := ensureCcmuxOnPath(&buf); err != nil {
		t.Fatalf("ensureCcmuxOnPath: %v", err)
	}

	rcPath := filepath.Join(home, ".zshrc")
	body, err := os.ReadFile(rcPath)
	if err != nil {
		t.Fatalf("rc not written: %v", err)
	}
	if !strings.Contains(string(body), rcGuardOpen) {
		t.Errorf(".zshrc missing managed block:\n%s", body)
	}
	if !strings.Contains(string(body), filepath.Join(home, ".local", "bin")) {
		t.Errorf(".zshrc missing install dir in export:\n%s", body)
	}
	// Output should tell the user to source the rc.
	if !strings.Contains(buf.String(), "source "+rcPath) {
		t.Errorf("output missing source hint:\n%s", buf.String())
	}
}

// TestEnsureCcmuxOnPath_Idempotent — re-running shouldn't double-write.
// Second invocation should detect the existing managed block and just
// print the source-it hint.
func TestEnsureCcmuxOnPath_Idempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/zsh")
	t.Setenv("PATH", "/usr/bin:/bin")

	var buf1, buf2 bytes.Buffer
	_ = ensureCcmuxOnPath(&buf1)
	body1, _ := os.ReadFile(filepath.Join(home, ".zshrc"))

	_ = ensureCcmuxOnPath(&buf2)
	body2, _ := os.ReadFile(filepath.Join(home, ".zshrc"))

	if string(body1) != string(body2) {
		t.Errorf("rc file changed across two ensure calls:\n first:\n%s\n second:\n%s",
			body1, body2)
	}
	if strings.Count(string(body2), rcGuardOpen) != 1 {
		t.Errorf("expected one managed block, got %d", strings.Count(string(body2), rcGuardOpen))
	}
}
