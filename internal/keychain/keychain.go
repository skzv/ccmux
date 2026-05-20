// Package keychain probes macOS login-keychain state. ccmux uses it to
// tell a genuine "not configured" apart from a credential check that
// failed only because the login keychain is locked — the usual case
// when you SSH into a headless Mac that has had no console login since
// boot. gh's OAuth token and moshi-hook's pairing secret both live in
// the keychain, so a locked keychain reads to those tools as a missing
// or invalid credential.
package keychain

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// probeTimeout bounds the `security` shell-out. The command is local
// and near-instant; the timeout only guards against a wedged `security`.
const probeTimeout = 3 * time.Second

// Locked reports whether the macOS login keychain is currently locked.
//
// Returns false on non-macOS, and false whenever the state cannot be
// positively determined — callers must read false as "don't blame the
// keychain", never as a confirmed "it is unlocked".
func Locked(ctx context.Context) bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	c, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	// `security show-keychain-info` succeeds (exit 0) on an unlocked
	// keychain. On a locked one, reading the settings triggers an
	// unlock prompt — which can't be shown non-interactively or over
	// SSH — so it fails with errSecInteractionNotAllowed /
	// errSecUserCanceled instead.
	path := filepath.Join(home, "Library", "Keychains", "login.keychain-db")
	out, err := exec.CommandContext(c, "security", "show-keychain-info", path).CombinedOutput()
	if err == nil {
		return false
	}
	return looksLocked(string(out))
}

// looksLocked reports whether `security show-keychain-info` failure
// output specifically indicates a locked keychain — as opposed to an
// unrelated failure such as a missing keychain file. Kept pure so the
// match is unit-testable without a real `security`.
func looksLocked(out string) bool {
	return strings.Contains(out, "User interaction is not allowed") ||
		strings.Contains(out, "User canceled the operation")
}
