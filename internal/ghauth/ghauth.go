// Package ghauth detects whether the user has GitHub CLI installed and
// authenticated. ccmux doesn't require gh — but `ccmux new` asks Claude
// to create a private GitHub repo as the last scaffolding step, and that
// step works much better when `gh` is on PATH and signed in. This
// package powers the friendly "recommended, not required" hint in
// `ccmux doctor` and the setup wizard.
package ghauth

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// State enumerates the three things a caller cares about.
type State int

const (
	// StateMissing — gh is not on PATH.
	StateMissing State = iota
	// StateNotAuthed — gh is installed but `gh auth status` exits non-zero.
	StateNotAuthed
	// StateAuthed — gh is installed and `gh auth status` is happy.
	StateAuthed
)

// Status bundles the detection result. User is the GitHub login when
// authed (parsed from `gh auth status`), empty otherwise.
type Status struct {
	State State
	User  string
}

// OK reports whether the user can rely on gh for repo creation.
func (s Status) OK() bool { return s.State == StateAuthed }

// Hint returns the one-line action the user should take next, suitable
// for printing in doctor/wizard output. Empty string when nothing to do.
//
// Framing: `ccmux new` is local-only by default, so gh is purely a
// convenience for pushing later (one `gh repo create` instead of the
// 4-line manual remote dance). We never imply it's required.
func (s Status) Hint() string {
	switch s.State {
	case StateMissing:
		return "gh CLI not installed — `brew install gh` (optional; makes pushing a new project to GitHub one command)"
	case StateNotAuthed:
		return "gh installed but not signed in — run `gh auth login` (optional; lets `gh repo create` push your local project to GitHub)"
	}
	return ""
}

// Detect runs the two relevant checks with a tight timeout. Never errors;
// a missing binary or a failed auth check just produces the matching
// State. Caller passes a context for cancellation.
func Detect(ctx context.Context) Status {
	bin, err := exec.LookPath("gh")
	if err != nil {
		return Status{State: StateMissing}
	}
	c, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(c, bin, "auth", "status").CombinedOutput()
	if err != nil {
		return Status{State: StateNotAuthed}
	}
	return Status{State: StateAuthed, User: parseUser(string(out))}
}

// parseUser pulls the GitHub login out of `gh auth status` output.
// The output is human-formatted and varies slightly across gh versions;
// we look for either "Logged in to github.com as <user>" or
// "account <user> (...)". Returns "" if no match.
func parseUser(out string) string {
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(raw)
		// New form: "✓ Logged in to github.com account <user> (keyring)"
		if i := strings.Index(line, "account "); i >= 0 {
			rest := strings.TrimSpace(line[i+len("account "):])
			if j := strings.IndexAny(rest, " ("); j >= 0 {
				return rest[:j]
			}
			return rest
		}
		// Older form: "Logged in to github.com as <user>".
		if i := strings.Index(line, " as "); i >= 0 {
			rest := strings.TrimSpace(line[i+len(" as "):])
			if j := strings.IndexAny(rest, " ("); j >= 0 {
				return rest[:j]
			}
			return rest
		}
	}
	return ""
}
