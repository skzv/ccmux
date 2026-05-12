package setupwizard

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// ccmuxInstallDir is where `make install` drops the binaries. Centralized
// so the PATH-fixer and the printed instructions can't drift.
const ccmuxInstallDir = ".local/bin"

// rcGuardLine is the comment we wrap our managed PATH export with so a
// re-run can detect "I already did this" and skip the append. Distinct
// open/close markers also let a user see exactly what ccmux added if
// they ever open the file.
const (
	rcGuardOpen  = "# >>> ccmux PATH (managed) >>>"
	rcGuardClose = "# <<< ccmux PATH (managed) <<<"
)

// ccmuxOnPath returns true if `ccmux` resolves on the current process's
// PATH. Used as the gate for whether to print the "you need to add
// ~/.local/bin to PATH" callout after install.
func ccmuxOnPath() bool {
	_, err := exec.LookPath("ccmux")
	return err == nil
}

// pathContains reports whether `dir` is one of the colon-separated
// entries in `pathEnv`. Pure for testing — no env reads, no home
// expansion. Empty `dir` always returns false so a busted call doesn't
// silently succeed.
func pathContains(pathEnv, dir string) bool {
	if dir == "" {
		return false
	}
	for _, p := range filepath.SplitList(pathEnv) {
		if p == dir {
			return true
		}
	}
	return false
}

// detectShellRC returns the path to the rc file ccmux should append to
// for the user's login shell, plus the literal export line to write.
// Detection key is $SHELL; we fall back to ~/.profile (POSIX-portable)
// when the shell isn't one we recognize.
//
// Mac-specific note: zsh on macOS reads ~/.zshrc for both login and
// interactive shells (Terminal.app launches zsh as a login shell, so
// ~/.zprofile is also valid, but ~/.zshrc is what most users edit and
// what every guide tells people to use). We commit to ~/.zshrc.
func detectShellRC(home, shellEnv string) (rcPath, exportLine string) {
	shell := filepath.Base(shellEnv)
	binDir := filepath.Join(home, ccmuxInstallDir)
	switch shell {
	case "zsh":
		return filepath.Join(home, ".zshrc"),
			fmt.Sprintf(`export PATH="%s:$PATH"`, binDir)
	case "bash":
		// bash on macOS reads ~/.bash_profile for login shells; bash on
		// Linux usually reads ~/.bashrc. Pick by GOOS.
		if runtime.GOOS == "darwin" {
			return filepath.Join(home, ".bash_profile"),
				fmt.Sprintf(`export PATH="%s:$PATH"`, binDir)
		}
		return filepath.Join(home, ".bashrc"),
			fmt.Sprintf(`export PATH="%s:$PATH"`, binDir)
	case "fish":
		// fish has its own syntax — `set -x PATH …` not `export …` — and
		// reads ~/.config/fish/config.fish.
		return filepath.Join(home, ".config", "fish", "config.fish"),
			fmt.Sprintf(`set -gx PATH %s $PATH`, binDir)
	}
	// Unknown shell — fall back to ~/.profile which every POSIX shell
	// reads on login.
	return filepath.Join(home, ".profile"),
		fmt.Sprintf(`export PATH="%s:$PATH"`, binDir)
}

// pathAlreadyManaged reports whether `rcBody` already contains a ccmux
// managed PATH block. Recognized by the guard comment, not by string-
// matching the export line — that way a user who hand-edited their rc
// to a slightly different formulation doesn't trip us into appending a
// second one.
func pathAlreadyManaged(rcBody string) bool {
	return strings.Contains(rcBody, rcGuardOpen)
}

// appendCcmuxPathBlock returns rcBody with a ccmux-managed PATH block
// appended (or rcBody unchanged if one is already present). Pure
// function — the caller decides whether to write to disk.
//
// The block is wrapped in guard comments and includes a one-line
// explanation so a future reader sees both what was added and why.
func appendCcmuxPathBlock(rcBody, exportLine string) string {
	if pathAlreadyManaged(rcBody) {
		return rcBody
	}
	// Ensure a trailing newline before our block so we don't run into
	// the previous line.
	sep := ""
	if rcBody != "" && !strings.HasSuffix(rcBody, "\n") {
		sep = "\n"
	}
	block := fmt.Sprintf(`%s
# Added by `+"`ccmux setup`"+`. ccmux drops binaries under ~/.local/bin
# (Linux convention; not on macOS's default PATH). Remove this block if
# you'd rather manage PATH yourself.
%s
%s
`, rcGuardOpen, exportLine, rcGuardClose)
	return rcBody + sep + block
}

// ensureCcmuxOnPath is the wizard's last-mile fix for the "command not
// found: ccmux" trap reported on Macs that had never had ~/.local/bin
// on PATH. Walks through:
//
//  1. If `ccmux` already resolves on PATH, do nothing.
//  2. Otherwise, detect the user's shell rc file.
//  3. If the rc is already ccmux-managed, just print a hint to source
//     it / open a new shell.
//  4. Otherwise, append the managed block and tell the user how to
//     activate it for this shell.
//
// Returns nil on success. Best-effort: a permission-denied rc write
// surfaces as a printed warning, not a wizard-aborting error.
func ensureCcmuxOnPath(out io.Writer) error {
	if ccmuxOnPath() {
		fmt.Fprintln(out, stOK.Render("✓ ")+"ccmux is on your PATH.")
		fmt.Fprintln(out, "Next: "+stEmphasis.Render("ccmux")+" to launch the TUI.")
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	binDir := filepath.Join(home, ccmuxInstallDir)
	rcPath, exportLine := detectShellRC(home, os.Getenv("SHELL"))

	fmt.Fprintln(out, stWarn.Render("⚠ ")+binDir+" isn't on your PATH yet, so `ccmux` won't resolve.")
	fmt.Fprintln(out, "  Run "+stEmphasis.Render(filepath.Join(binDir, "ccmux"))+" to launch right now.")
	fmt.Fprintln(out)

	body, err := os.ReadFile(rcPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(out, stWarn.Render("  could not read "+rcPath+": "+err.Error()))
		fmt.Fprintln(out, "  add this line to your shell rc by hand: "+stEmphasis.Render(exportLine))
		return nil
	}
	if pathAlreadyManaged(string(body)) {
		fmt.Fprintln(out, "  Your "+rcPath+" already has a ccmux PATH block — open a new shell or run "+stEmphasis.Render("source "+rcPath))
		return nil
	}
	newBody := appendCcmuxPathBlock(string(body), exportLine)
	if err := os.MkdirAll(filepath.Dir(rcPath), 0o755); err != nil {
		fmt.Fprintln(out, stWarn.Render("  could not create "+filepath.Dir(rcPath)+": "+err.Error()))
		fmt.Fprintln(out, "  add this line to your shell rc by hand: "+stEmphasis.Render(exportLine))
		return nil
	}
	if err := os.WriteFile(rcPath, []byte(newBody), 0o644); err != nil {
		fmt.Fprintln(out, stWarn.Render("  could not write "+rcPath+": "+err.Error()))
		fmt.Fprintln(out, "  add this line to your shell rc by hand: "+stEmphasis.Render(exportLine))
		return nil
	}
	fmt.Fprintln(out, "  Added a managed PATH block to "+stEmphasis.Render(rcPath)+".")
	fmt.Fprintln(out, "  Activate it now with "+stEmphasis.Render("source "+rcPath)+" or open a new terminal.")
	return nil
}
