package agent

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestNoHardcodedAgentLaunchCommands is the regression net for the
// multi-agent bug: at one point five separate call sites all called
// `tmux.New(..., "claude")` or `tmux.New(..., "claude --continue ||
// claude || zsh")` regardless of the project's agent sidecar or the
// Sessions-tab picker selection. That meant Codex / Antigravity were
// effectively impossible to actually run from ccmux.
//
// All those sites now route through agent.ByID(...).LaunchCmd(...).
// To keep them honest this test walks the repo's production .go
// files and asserts that no source line contains a hardcoded Claude
// launch string in a `tmux.New(` argument position. Adding a new
// session-spawn site that hardcodes claude trips this test on the
// PR that introduces it.
//
// The audit lives in `internal/agent` because that's the package
// the fix routes through — colocating the test with the abstraction
// makes the contract obvious to anyone reading the package.
func TestNoHardcodedAgentLaunchCommands(t *testing.T) {
	root := repoRoot(t)
	// Patterns that would have re-introduced the bug. We keep them
	// loose enough to catch `"claude"`, `"claude --continue ..."`, and
	// `'claude ... || zsh'` raw-string forms. Capture group not used;
	// the match itself is what we report.
	patterns := []*regexp.Regexp{
		// tmux.New(...) with a quoted literal starting with claude.
		regexp.MustCompile(`tmux\.New\([^)]*"claude(\s|"|--)`),
		regexp.MustCompile("tmux\\.New\\([^)]*`claude(\\s|`|--)"),
		// Explicit "claude --continue || claude || zsh" anywhere as a
		// string literal — there is no legitimate non-test use of
		// this exact phrase outside the agent.Claude.LaunchCmd
		// implementation.
		regexp.MustCompile(`"claude --continue \|\| claude \|\| zsh"`),
		regexp.MustCompile("`claude --continue \\|\\| claude \\|\\| zsh`"),
	}
	// File-level allowlist: places where these strings are
	// legitimately the agent's own definition or this very audit
	// test. Anything else needs the agent abstraction.
	allowlist := map[string]struct{}{
		filepath.Join("internal", "agent", "claude.go"):                   {},
		filepath.Join("internal", "agent", "no_hardcode_audit_test.go"):   {},
		filepath.Join("internal", "agent", "no_hardcoded_claude_test.go"): {},
	}

	violations := []string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Skip vendor, hidden dirs, bin output, etc.
			base := d.Name()
			if base == "vendor" || base == ".git" || base == "bin" ||
				base == "node_modules" || strings.HasPrefix(base, ".") {
				if path != root {
					return fs.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		// Only audit production code under cmd/ and internal/, not
		// _test.go files (the agent has tests that *do* use the raw
		// strings to pin LaunchCmd's shape).
		rel, _ := filepath.Rel(root, path)
		if !(strings.HasPrefix(rel, "cmd"+string(filepath.Separator)) ||
			strings.HasPrefix(rel, "internal"+string(filepath.Separator))) {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if _, skip := allowlist[rel]; skip {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// Line-by-line so the failure message points at the actual
		// regression rather than dumping the whole file. Comment
		// lines are skipped because regression-history comments
		// (e.g. "this used to call `claude --continue || ... || zsh`")
		// are exactly the kind of context we want to keep in the
		// codebase — they explain *why* the abstraction exists.
		// `/* … */` block comments are rare enough in this codebase
		// to ignore; a future violator hiding the string in one
		// would still need to actually call tmux.New elsewhere.
		lines := strings.Split(string(body), "\n")
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			for _, re := range patterns {
				if re.MatchString(line) {
					violations = append(violations,
						fmt.Sprintf("%s:%d: %s", rel, i+1, strings.TrimSpace(line)))
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(violations) > 0 {
		t.Errorf("hardcoded claude launch command detected — every session-spawn site must go through agent.ByID(id).LaunchCmd(...) so the user's agent choice is honored. Offending lines:\n  %s",
			strings.Join(violations, "\n  "))
	}
}

// repoRoot returns the absolute path to the ccmux repo root. The
// agent package lives two directories below it (internal/agent), so
// we walk upward from the test binary's working directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// Walk up until we find go.mod. Belt-and-suspenders for callers
	// that might run tests from an unusual cwd.
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("repoRoot: could not find go.mod above %s", wd)
		}
		dir = parent
	}
}
