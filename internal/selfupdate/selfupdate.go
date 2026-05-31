// Package selfupdate detects whether a source/`make install` ccmux
// checkout is behind its upstream. For that install path "is there an
// update?" is answered by a `git fetch` plus a commit-count comparison
// — not a release-feed poll. Packaged installs (Homebrew, release
// tarballs) have no checkout to compare against, so RepoRoot returns an
// error and no banner is shown; those update through their package
// manager (`ccmux update` hands brew installs to `brew upgrade`).
//
// This package only CHECKS. It never pulls, rebuilds, or restarts
// anything; that's `ccmux update`'s job, run by the user. The TUI
// calls Check on launch (when update.auto_check is on) to surface a
// dashboard banner.
package selfupdate

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Result is the outcome of a Check.
type Result struct {
	// Behind is how many commits the local HEAD is behind its
	// upstream tracking branch. 0 means up to date.
	Behind int

	// Branch is the local branch name that was checked (for the
	// banner wording — "3 commits behind on main").
	Branch string

	// RepoRoot is the resolved ccmux checkout the check ran against.
	RepoRoot string
}

// Available reports whether an update is worth surfacing to the user.
func (r Result) Available() bool { return r.Behind > 0 }

// gitRunner runs a git subcommand inside `dir` and returns trimmed
// stdout. A package var so tests can swap it for a fake without a
// real repo or network. The production implementation is realGit.
var gitRunner = realGit

func realGit(ctx context.Context, dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir}, args...)
	out, err := exec.CommandContext(ctx, "git", full...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Check resolves the ccmux checkout, fetches its upstream, and counts
// how many commits HEAD is behind. Every failure mode — no checkout
// (binary install), no upstream branch, no network — returns an error
// the caller treats as "can't tell, show no banner." A non-error
// Result with Behind=0 means "definitely up to date."
//
// The whole operation is time-bounded: the fetch needs network, and a
// hung fetch must not wedge the caller. Pass a context with a
// deadline (the TUI uses ~20s); Check also clamps its own internal
// fetch to be safe.
func Check(ctx context.Context) (Result, error) {
	root, err := RepoRoot()
	if err != nil {
		return Result{}, err
	}

	branch, err := gitRunner(ctx, root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return Result{}, fmt.Errorf("resolve branch: %w", err)
	}
	if branch == "" || branch == "HEAD" {
		// Detached HEAD — no branch to compare an upstream against.
		return Result{}, fmt.Errorf("detached HEAD; no upstream to check")
	}

	// The upstream tracking ref. Errors when the branch has no
	// upstream configured — common on local feature branches; treat
	// as "can't check."
	upstream, err := gitRunner(ctx, root, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	if err != nil || upstream == "" {
		return Result{}, fmt.Errorf("branch %q has no upstream", branch)
	}

	// Fetch just the tracked remote so the upstream ref is current.
	// upstream looks like "origin/main" — the remote is the part
	// before the first slash.
	remote := upstream
	if i := strings.IndexByte(upstream, '/'); i > 0 {
		remote = upstream[:i]
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if _, err := gitRunner(fetchCtx, root, "fetch", "--quiet", remote); err != nil {
		return Result{}, fmt.Errorf("git fetch %s: %w", remote, err)
	}

	// Count commits HEAD is behind upstream: `rev-list --count HEAD..@{u}`.
	countStr, err := gitRunner(ctx, root, "rev-list", "--count", "HEAD..@{u}")
	if err != nil {
		return Result{}, fmt.Errorf("count commits behind: %w", err)
	}
	behind, err := strconv.Atoi(strings.TrimSpace(countStr))
	if err != nil {
		return Result{}, fmt.Errorf("parse commit count %q: %w", countStr, err)
	}

	return Result{Behind: behind, Branch: branch, RepoRoot: root}, nil
}

// RepoRoot resolves the ccmux git checkout this binary updates from:
// first by walking up from the running binary's directory, then — only
// for a `make install` binary — falling back to ~/Projects/ccmux.
// Returns an error when neither applies, which the caller treats as
// "no update check possible" (the expected outcome for a Homebrew or
// other packaged install, whose updates come from a package manager,
// not a git pull).
func RepoRoot() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		exe = ""
	}
	home, _ := os.UserHomeDir()
	return repoRootFrom(exe, home)
}

// repoRootFrom is the testable core of RepoRoot. `exe` is the running
// ccmux binary (may be ""); `home` is the user's home dir (may be "").
func repoRootFrom(exe, home string) (string, error) {
	if exe != "" {
		real := exe
		if r, err := filepath.EvalSymlinks(exe); err == nil {
			real = r
		}
		if root := findCcmuxRoot(filepath.Dir(real)); root != "" {
			return root, nil
		}
	}
	// The ~/Projects/ccmux fallback is valid ONLY for a `make install`
	// binary: make install copies ccmux to ~/.local/bin and leaves the
	// checkout at ~/Projects/ccmux, so the running binary sits outside
	// its checkout. A Homebrew binary, a release tarball, or any other
	// packaged install must NOT borrow an unrelated ~/Projects/ccmux
	// clone — without this guard a brew install reported "N commits
	// behind main" against whatever stale clone happened to live there.
	if exe != "" && home != "" && isMakeInstallBinary(exe, home) {
		guess := filepath.Join(home, "Projects", "ccmux")
		if looksLikeCcmuxRepo(guess) {
			return guess, nil
		}
	}
	return "", fmt.Errorf("no ccmux git checkout for this binary — update check skipped (packaged install?)")
}

// isMakeInstallBinary reports whether `exe` is the binary `make
// install` produces: a plain copy at ~/.local/bin/ccmux. Checks the
// path as invoked and after resolving symlinks.
func isMakeInstallBinary(exe, home string) bool {
	localBin := filepath.Join(home, ".local", "bin")
	if filepath.Dir(exe) == localBin {
		return true
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		if filepath.Dir(real) == localBin {
			return true
		}
	}
	return false
}

// findCcmuxRoot walks up from `start` looking for the ccmux repo
// root. Returns "" when it reaches the filesystem root without a hit.
func findCcmuxRoot(start string) string {
	dir := start
	for {
		if looksLikeCcmuxRepo(dir) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// looksLikeCcmuxRepo is true when `dir` has both a .git and a Makefile
// — the cheap, good-enough signature of the ccmux checkout. (Mirrors
// the same predicate in cmd/ccmux/cmd/update.go; kept independent so
// internal/ doesn't depend on cmd/.)
func looksLikeCcmuxRepo(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, "Makefile")); err != nil {
		return false
	}
	return true
}
