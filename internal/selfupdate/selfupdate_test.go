package selfupdate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGit builds a gitRunner stub from a map of "subcommand prefix" →
// (output, error). The key is matched as a prefix of the joined args
// so a test can say {"fetch": ...} without spelling the whole vector.
// Restores the real runner via t.Cleanup.
func fakeGit(t *testing.T, responses map[string]struct {
	out string
	err error
}) {
	t.Helper()
	orig := gitRunner
	t.Cleanup(func() { gitRunner = orig })
	gitRunner = func(_ context.Context, _ string, args ...string) (string, error) {
		joined := strings.Join(args, " ")
		for prefix, resp := range responses {
			if strings.HasPrefix(joined, prefix) {
				return resp.out, resp.err
			}
		}
		return "", fmt.Errorf("fakeGit: no response configured for %q", joined)
	}
}

type gitResp = struct {
	out string
	err error
}

// TestResult_Available — the predicate the dashboard keys off. Behind
// > 0 means show the banner; 0 means don't.
func TestResult_Available(t *testing.T) {
	if (Result{Behind: 0}).Available() {
		t.Error("Behind=0 should not be Available")
	}
	if !(Result{Behind: 1}).Available() {
		t.Error("Behind=1 should be Available")
	}
	if !(Result{Behind: 42}).Available() {
		t.Error("Behind=42 should be Available")
	}
}

// TestCheck_BehindCount — happy path: branch resolves, upstream
// resolves, fetch succeeds, rev-list reports 3. Check must surface
// Behind=3 with the branch name.
//
// Note: Check calls RepoRoot() which hits the real filesystem. The
// test binary runs from a temp build dir, not the ccmux checkout, so
// RepoRoot may or may not resolve. We skip when it can't — the git
// logic above RepoRoot is what these fakeGit tests exercise; RepoRoot
// itself is covered separately.
func TestCheck_BehindCount(t *testing.T) {
	if _, err := RepoRoot(); err != nil {
		t.Skipf("no ccmux checkout resolvable in this environment: %v", err)
	}
	fakeGit(t, map[string]gitResp{
		"rev-parse --abbrev-ref HEAD":                      {out: "main"},
		"rev-parse --abbrev-ref --symbolic-full-name @{u}": {out: "origin/main"},
		"fetch --quiet origin":                             {out: ""},
		"rev-list --count HEAD..@{u}":                      {out: "3"},
	})
	res, err := Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.Behind != 3 {
		t.Errorf("Behind = %d, want 3", res.Behind)
	}
	if res.Branch != "main" {
		t.Errorf("Branch = %q, want main", res.Branch)
	}
	if !res.Available() {
		t.Error("3 commits behind should be Available")
	}
}

// TestCheck_UpToDate — fetch succeeds, rev-list reports 0. Check
// returns a non-error Result with Behind=0 — "definitely up to date,"
// distinct from the error case ("couldn't tell").
func TestCheck_UpToDate(t *testing.T) {
	if _, err := RepoRoot(); err != nil {
		t.Skipf("no ccmux checkout: %v", err)
	}
	fakeGit(t, map[string]gitResp{
		"rev-parse --abbrev-ref HEAD":                      {out: "main"},
		"rev-parse --abbrev-ref --symbolic-full-name @{u}": {out: "origin/main"},
		"fetch --quiet origin":                             {out: ""},
		"rev-list --count HEAD..@{u}":                      {out: "0"},
	})
	res, err := Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.Available() {
		t.Errorf("Behind=0 should not be Available, got %+v", res)
	}
}

// TestCheck_NoUpstream — a local feature branch with no upstream set
// can't be compared. Check must return an error (caller treats it as
// "no banner"), not a misleading Behind=0.
func TestCheck_NoUpstream(t *testing.T) {
	if _, err := RepoRoot(); err != nil {
		t.Skipf("no ccmux checkout: %v", err)
	}
	fakeGit(t, map[string]gitResp{
		"rev-parse --abbrev-ref HEAD":                      {out: "feature/x"},
		"rev-parse --abbrev-ref --symbolic-full-name @{u}": {err: errors.New("no upstream")},
	})
	if _, err := Check(context.Background()); err == nil {
		t.Error("Check should error when the branch has no upstream")
	}
}

// TestCheck_DetachedHead — a detached HEAD has no branch; Check must
// error rather than try to resolve a nonexistent upstream.
func TestCheck_DetachedHead(t *testing.T) {
	if _, err := RepoRoot(); err != nil {
		t.Skipf("no ccmux checkout: %v", err)
	}
	fakeGit(t, map[string]gitResp{
		"rev-parse --abbrev-ref HEAD": {out: "HEAD"},
	})
	if _, err := Check(context.Background()); err == nil {
		t.Error("Check should error on a detached HEAD")
	}
}

// TestCheck_FetchFails — offline / network error during fetch. Check
// must surface the error so the caller shows no (possibly stale)
// banner rather than comparing against an outdated upstream ref.
func TestCheck_FetchFails(t *testing.T) {
	if _, err := RepoRoot(); err != nil {
		t.Skipf("no ccmux checkout: %v", err)
	}
	fakeGit(t, map[string]gitResp{
		"rev-parse --abbrev-ref HEAD":                      {out: "main"},
		"rev-parse --abbrev-ref --symbolic-full-name @{u}": {out: "origin/main"},
		"fetch --quiet origin":                             {err: errors.New("could not resolve host")},
	})
	if _, err := Check(context.Background()); err == nil {
		t.Error("Check should error when git fetch fails")
	}
}

// TestCheck_GarbageRevListOutput — defensive: if rev-list returns
// something non-numeric, the atoi must fail loudly rather than
// silently treating it as 0 ("up to date").
func TestCheck_GarbageRevListOutput(t *testing.T) {
	if _, err := RepoRoot(); err != nil {
		t.Skipf("no ccmux checkout: %v", err)
	}
	fakeGit(t, map[string]gitResp{
		"rev-parse --abbrev-ref HEAD":                      {out: "main"},
		"rev-parse --abbrev-ref --symbolic-full-name @{u}": {out: "origin/main"},
		"fetch --quiet origin":                             {out: ""},
		"rev-list --count HEAD..@{u}":                      {out: "not-a-number"},
	})
	if _, err := Check(context.Background()); err == nil {
		t.Error("Check should error on non-numeric rev-list output")
	}
}

// TestLooksLikeCcmuxRepo — the .git + Makefile signature. A directory
// with neither, or only one, is not the ccmux repo.
func TestLooksLikeCcmuxRepo(t *testing.T) {
	// The package's own test runs inside the checkout; the repo root
	// is two levels up from internal/selfupdate. But rather than guess
	// paths, build synthetic dirs.
	bothPresent := t.TempDir()
	mkDir(t, bothPresent, ".git")
	mkFile(t, bothPresent, "Makefile")
	if !looksLikeCcmuxRepo(bothPresent) {
		t.Error(".git + Makefile dir should look like the repo")
	}

	onlyGit := t.TempDir()
	mkDir(t, onlyGit, ".git")
	if looksLikeCcmuxRepo(onlyGit) {
		t.Error(".git without Makefile should NOT look like the repo")
	}

	empty := t.TempDir()
	if looksLikeCcmuxRepo(empty) {
		t.Error("empty dir should not look like the repo")
	}
}

// mkDir / mkFile are t.Helper conveniences for the synthetic-repo
// tests above.
func mkDir(t *testing.T, parent, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(parent, name), 0o755); err != nil {
		t.Fatal(err)
	}
}

func mkFile(t *testing.T, parent, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(parent, name), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}
