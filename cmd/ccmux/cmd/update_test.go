package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRepo lays out a directory that resolveRepo will accept: a .git
// dir and a Makefile. Returns its absolute path.
func fakeRepo(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte("build:\n\techo fake\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func TestLooksLikeCcmuxRepo(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, dir string)
		want  bool
	}{
		{
			"both .git and Makefile",
			func(t *testing.T, d string) { fakeRepo(t, d) },
			true,
		},
		{
			"only .git, no Makefile",
			func(t *testing.T, d string) { _ = os.MkdirAll(filepath.Join(d, ".git"), 0o755) },
			false,
		},
		{
			"only Makefile, no .git",
			func(t *testing.T, d string) {
				_ = os.WriteFile(filepath.Join(d, "Makefile"), []byte("x"), 0o644)
			},
			false,
		},
		{
			"empty dir",
			func(t *testing.T, d string) {},
			false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.setup(t, dir)
			if got := looksLikeCcmuxRepo(dir); got != tc.want {
				t.Fatalf("looksLikeCcmuxRepo(%s) = %v, want %v", dir, got, tc.want)
			}
		})
	}
}

func TestFindGitRoot_WalksUpAncestors(t *testing.T) {
	// Repo at <tmp>/repo, then a deeply nested binary path.
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	_ = os.MkdirAll(repo, 0o755)
	fakeRepo(t, repo)

	deep := filepath.Join(repo, "bin", "subdir", "more")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	if got := findGitRoot(deep); got != repo {
		t.Fatalf("findGitRoot(%s) = %q, want %q", deep, got, repo)
	}
}

func TestFindGitRoot_StopsAtFilesystemRoot(t *testing.T) {
	// Pick a path that almost certainly has no ccmux repo above it.
	if got := findGitRoot("/var/empty"); got != "" {
		t.Fatalf("expected no repo above /var/empty, got %q", got)
	}
}

func TestFindGitRoot_PrefersInnermostRepo(t *testing.T) {
	tmp := t.TempDir()
	outer := filepath.Join(tmp, "outer")
	inner := filepath.Join(outer, "inner")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeRepo(t, outer)
	fakeRepo(t, inner)

	if got := findGitRoot(inner); got != inner {
		t.Fatalf("findGitRoot from inner = %q, want %q", got, inner)
	}
	// Starting from outer still returns outer.
	if got := findGitRoot(outer); got != outer {
		t.Fatalf("findGitRoot from outer = %q, want %q", got, outer)
	}
}

func TestValidateRepo_ExplicitGood(t *testing.T) {
	dir := t.TempDir()
	want := fakeRepo(t, dir)
	got, err := validateRepo(dir)
	if err != nil {
		t.Fatalf("validateRepo(%s): %v", dir, err)
	}
	if got != want {
		t.Fatalf("validateRepo returned %q, want %q", got, want)
	}
}

func TestValidateRepo_RejectsNonRepo(t *testing.T) {
	if _, err := validateRepo(t.TempDir()); err == nil {
		t.Fatal("validateRepo should reject empty dir, got nil")
	}
}

func TestResolveRepo_ExplicitWins(t *testing.T) {
	dir := t.TempDir()
	want := fakeRepo(t, dir)
	got, err := resolveRepo(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("resolveRepo(explicit=%s) = %q, want %q", dir, got, want)
	}
}

func TestResolveRepo_ExplicitRejectsBadPath(t *testing.T) {
	if _, err := resolveRepo(t.TempDir()); err == nil {
		t.Fatal("resolveRepo should reject empty explicit dir")
	}
}

// TestResolveRepo_AutoDetectFromBinaryAncestor is exercised indirectly
// here: we can't easily move os.Executable() inside a test, but we can
// at least confirm the function returns *some* path when invoked from
// the project tree (which has both .git and a Makefile). If this test
// ever fails inside this repo, something is wrong with auto-detection.
func TestResolveRepo_AutoDetectInThisRepo(t *testing.T) {
	got, err := resolveRepo("")
	if err != nil {
		// Acceptable: when running tests outside the project tree (rare),
		// the fallback ~/Projects/ccmux may not exist either.
		t.Skipf("resolveRepo found nothing — likely running outside the ccmux tree: %v", err)
	}
	if !looksLikeCcmuxRepo(got) {
		t.Fatalf("resolveRepo returned %q which doesn't look like a ccmux repo", got)
	}
}

func TestRunStep_DryRunDoesNothing(t *testing.T) {
	// `false` would normally exit 1; under --dry-run we should not call
	// it. If the implementation regresses, this test fails because
	// `false` returns an error.
	if err := runStep(t.TempDir(), true, "false"); err != nil {
		t.Fatalf("dry-run still executed the command: %v", err)
	}
}

func TestRunStep_RunsAndReportsExit(t *testing.T) {
	// `true` exits 0.
	if err := runStep(t.TempDir(), false, "true"); err != nil {
		t.Fatalf("runStep(true): unexpected error %v", err)
	}
	// `false` exits 1 — runStep should surface that.
	if err := runStep(t.TempDir(), false, "false"); err == nil {
		t.Fatal("runStep(false): expected error, got nil")
	}
}

// realGitRepo bootstraps an actual git repo (not a fake .git/ dir) so
// the ensureOnBranch tests can run real `git symbolic-ref` against it.
// Returns the absolute path. Commits one file on `main` so HEAD is
// resolvable.
func realGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustGit(t, dir, "init", "-b", "main")
	mustGit(t, dir, "config", "user.email", "test@example.com")
	mustGit(t, dir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", ".")
	mustGit(t, dir, "commit", "-m", "init")
	return dir
}

func mustGit(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// TestEnsureOnBranch_AlreadyOnBranch — a fresh repo HEAD points at
// `main` and ensureOnBranch should be a no-op (no error, no checkout).
func TestEnsureOnBranch_AlreadyOnBranch(t *testing.T) {
	repo := realGitRepo(t)
	if err := ensureOnBranch(repo, false); err != nil {
		t.Fatalf("on-branch repo errored: %v", err)
	}
	// Branch should still be main after the call.
	out := mustGit(t, repo, "symbolic-ref", "--short", "HEAD")
	if got := strings.TrimSpace(out); got != "main" {
		t.Errorf("HEAD = %q, want main", got)
	}
}

// TestEnsureOnBranch_DetachedHEAD reproduces the user-friend bug:
// `git pull --ff-only` on a detached HEAD prints "You are not
// currently on a branch" and fails. We need to switch back first.
// We simulate detached HEAD via `git checkout <sha>` and assert
// ensureOnBranch puts us back on main automatically — the same
// outcome as the user typing `git checkout main` themselves.
func TestEnsureOnBranch_DetachedHEAD(t *testing.T) {
	repo := realGitRepo(t)
	// Configure refs/remotes/origin/HEAD so resolveDefaultBranch can
	// find a default branch without hitting the network.
	// Set up a fake origin/main first, then point origin/HEAD at it
	// symbolically. update-ref only works on a real branch name; the
	// HEAD symref needs git symbolic-ref.
	mustGit(t, repo, "update-ref", "refs/remotes/origin/main", "HEAD")
	mustGit(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")

	// Detach HEAD by checking out the commit directly.
	sha := strings.TrimSpace(mustGit(t, repo, "rev-parse", "HEAD"))
	mustGit(t, repo, "checkout", sha)

	// Sanity: we should be detached now.
	if out, err := exec.Command("git", "-C", repo, "symbolic-ref", "-q", "HEAD").Output(); err == nil && strings.TrimSpace(string(out)) != "" {
		t.Fatalf("expected detached HEAD, but got branch %q", strings.TrimSpace(string(out)))
	}

	if err := ensureOnBranch(repo, false); err != nil {
		t.Fatalf("ensureOnBranch on detached HEAD: %v", err)
	}
	// After the fix, HEAD should be back on main.
	out := strings.TrimSpace(mustGit(t, repo, "symbolic-ref", "--short", "HEAD"))
	if out != "main" {
		t.Errorf("post-recovery HEAD = %q, want main", out)
	}
}

// TestEnsureOnBranch_DetachedNoOriginHEAD — when origin/HEAD isn't set
// the function can't auto-resolve a branch and must return a clear
// error so the user knows what to do. Better than git's confusing
// multi-line "Please specify which branch to merge with" output.
func TestEnsureOnBranch_DetachedNoOriginHEAD(t *testing.T) {
	repo := realGitRepo(t)
	sha := strings.TrimSpace(mustGit(t, repo, "rev-parse", "HEAD"))
	mustGit(t, repo, "checkout", sha)

	// No origin/HEAD ref. No origin remote at all, in fact — so
	// `git remote show origin` will also fail. Both fallbacks miss.
	err := ensureOnBranch(repo, false)
	if err == nil {
		t.Fatal("expected error when no default branch resolvable, got nil")
	}
	if !strings.Contains(err.Error(), "detached HEAD") {
		t.Errorf("error should mention detached HEAD: %v", err)
	}
}

// TestEnsureOnBranch_DryRun — even in dry-run we still detect the
// detached HEAD state and report the intended `git checkout`; we must
// NOT actually mutate the repo.
func TestEnsureOnBranch_DryRun(t *testing.T) {
	repo := realGitRepo(t)
	// Set up a fake origin/main first, then point origin/HEAD at it
	// symbolically. update-ref only works on a real branch name; the
	// HEAD symref needs git symbolic-ref.
	mustGit(t, repo, "update-ref", "refs/remotes/origin/main", "HEAD")
	mustGit(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	sha := strings.TrimSpace(mustGit(t, repo, "rev-parse", "HEAD"))
	mustGit(t, repo, "checkout", sha)

	if err := ensureOnBranch(repo, true); err != nil {
		t.Fatalf("dry-run errored: %v", err)
	}
	// Should still be detached — dry-run must not execute the checkout.
	out, err := exec.Command("git", "-C", repo, "symbolic-ref", "-q", "HEAD").Output()
	if err == nil && strings.TrimSpace(string(out)) != "" {
		t.Errorf("dry-run executed the checkout; HEAD now %q", strings.TrimSpace(string(out)))
	}
}

// TestResolveDefaultBranch_ReadsOriginHEAD — happy path: when the
// repo has refs/remotes/origin/HEAD pointing at origin/main, the
// helper returns "main".
func TestResolveDefaultBranch_ReadsOriginHEAD(t *testing.T) {
	repo := realGitRepo(t)
	// Set up a fake origin/main first, then point origin/HEAD at it
	// symbolically. update-ref only works on a real branch name; the
	// HEAD symref needs git symbolic-ref.
	mustGit(t, repo, "update-ref", "refs/remotes/origin/main", "HEAD")
	mustGit(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	if got := resolveDefaultBranch(repo); got != "main" {
		t.Errorf("resolveDefaultBranch = %q, want main", got)
	}
}

// TestResolveDefaultBranch_EmptyOnFailure — no origin/HEAD, no
// reachable origin remote, returns "".
func TestResolveDefaultBranch_EmptyOnFailure(t *testing.T) {
	repo := realGitRepo(t)
	if got := resolveDefaultBranch(repo); got != "" {
		t.Errorf("resolveDefaultBranch on bare repo = %q, want empty", got)
	}
}

// realGitRepoWithRemote bootstraps a real git repo whose `main`
// branch tracks a real `origin/main` remote-tracking ref. Returns
// the worktree path. The "remote" is a bare clone in a sibling
// directory — same shape as a real GitHub setup as far as
// rev-parse is concerned, but no network IO.
func realGitRepoWithRemote(t *testing.T) string {
	t.Helper()
	// Bare remote on disk.
	remote := filepath.Join(t.TempDir(), "remote.git")
	mustGit(t, "", "init", "--bare", "-b", "main", remote)

	// Worktree that clones from the bare remote.
	work := t.TempDir()
	mustGit(t, "", "clone", remote, work)
	mustGit(t, work, "config", "user.email", "test@example.com")
	mustGit(t, work, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(work, "README"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, work, "add", ".")
	mustGit(t, work, "commit", "-m", "init")
	mustGit(t, work, "push", "-u", "origin", "main")
	return work
}

// TestEnsureGoodUpstream_HealthyRepoIsNoOp — the common case. main
// tracks origin/main, origin/main exists, nothing to fix. Function
// must not perturb anything.
func TestEnsureGoodUpstream_HealthyRepoIsNoOp(t *testing.T) {
	repo := realGitRepoWithRemote(t)
	beforeUpstream := remoteTrackingFor(repo, "main")
	if err := ensureGoodUpstream(repo, false); err != nil {
		t.Fatalf("healthy repo errored: %v", err)
	}
	afterUpstream := remoteTrackingFor(repo, "main")
	if beforeUpstream != afterUpstream {
		t.Errorf("upstream changed when it shouldn't have:\n  before=%q\n   after=%q",
			beforeUpstream, afterUpstream)
	}
}

// TestEnsureGoodUpstream_RetargetsDeletedUpstream is the user-
// reported bug: local main's upstream pointed at a feature branch
// that got deleted on origin. Function should retarget to
// origin/main (which still exists) so the next `git pull --ff-only`
// works without intervention.
func TestEnsureGoodUpstream_RetargetsDeletedUpstream(t *testing.T) {
	repo := realGitRepoWithRemote(t)
	// Create + push a feature branch, set main to track it, then
	// delete the feature branch on the remote — simulating
	// auto-delete-after-merge.
	mustGit(t, repo, "branch", "feature/foo")
	mustGit(t, repo, "push", "-u", "origin", "feature/foo")
	mustGit(t, repo, "branch", "--set-upstream-to=origin/feature/foo", "main")
	mustGit(t, repo, "push", "origin", "--delete", "feature/foo")
	// Prune the local remote-tracking ref too — otherwise the test
	// sees a stale refs/remotes/origin/feature/foo and concludes
	// the upstream is fine.
	mustGit(t, repo, "remote", "prune", "origin")

	if err := ensureGoodUpstream(repo, false); err != nil {
		t.Fatalf("retarget errored: %v", err)
	}
	if got := remoteTrackingFor(repo, "main"); !strings.HasSuffix(got, "/origin/main") {
		t.Errorf("upstream not retargeted to origin/main: got %q", got)
	}
}

// TestEnsureGoodUpstream_UnfixableErrorsClearly — when the local
// branch has a configured upstream that no longer exists on origin
// AND no same-named remote branch to retarget to, the function
// must surface a clear, actionable error rather than silently
// returning. Otherwise the user lands in `git pull --ff-only`'s
// cryptic "no such ref was fetched" message — which is exactly
// what this layer is supposed to prevent. We must also leave
// branch config untouched (no quiet behavior change).
func TestEnsureGoodUpstream_UnfixableErrorsClearly(t *testing.T) {
	repo := realGitRepoWithRemote(t)
	mustGit(t, repo, "checkout", "-b", "weird")
	mustGit(t, repo, "push", "-u", "origin", "weird")
	mustGit(t, repo, "push", "origin", "--delete", "weird")
	mustGit(t, repo, "remote", "prune", "origin")
	mustGit(t, repo, "branch", "-m", "weird", "absent-on-remote")

	before := mustGit(t, repo, "config", "--get-regexp", "branch\\.absent-on-remote\\.")

	err := ensureGoodUpstream(repo, false)
	if err == nil {
		t.Fatal("unfixable upstream should error with an actionable message, got nil")
	}
	if !strings.Contains(err.Error(), "absent-on-remote") || !strings.Contains(err.Error(), "--skip-pull") {
		t.Errorf("error should name the branch and mention --skip-pull: %v", err)
	}

	after := mustGit(t, repo, "config", "--get-regexp", "branch\\.absent-on-remote\\.")
	if before != after {
		t.Errorf("branch config mutated when it shouldn't have:\nbefore:\n%safter:\n%s", before, after)
	}
}

// TestIsUnderHomebrewPrefix covers the pure prefix-matching logic
// that drives Homebrew detection in `ccmux update`. The function has
// to recognize both macOS prefixes (Apple Silicon /opt/homebrew,
// Intel /usr/local) and Linuxbrew, but NOT misfire on similar-looking
// paths like /opt/homebrew-tap or /usr/local-extras, or on the
// install-script default of ~/.local/bin/.
func TestIsUnderHomebrewPrefix(t *testing.T) {
	prefixes := []string{
		"/opt/homebrew",
		"/usr/local",
		"/home/linuxbrew/.linuxbrew",
	}
	cases := []struct {
		name string
		exe  string
		want bool
	}{
		// Hit
		{"apple silicon cellar", "/opt/homebrew/Cellar/ccmux/0.1.1/bin/ccmux", true},
		{"apple silicon bin symlink", "/opt/homebrew/bin/ccmux", true},
		{"intel cellar", "/usr/local/Cellar/ccmux/0.1.1/bin/ccmux", true},
		{"linuxbrew", "/home/linuxbrew/.linuxbrew/bin/ccmux", true},
		// Miss — close but not under any prefix
		{"install.sh default", "/Users/alice/.local/bin/ccmux", false},
		{"system /usr/bin", "/usr/bin/ccmux", false},
		{"prefix-collision /opt/homebrew-tap", "/opt/homebrew-tap/bin/ccmux", false},
		{"prefix-collision /usr/local-extras", "/usr/local-extras/bin/ccmux", false},
		{"empty exe", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isUnderHomebrewPrefix(tc.exe, prefixes); got != tc.want {
				t.Errorf("isUnderHomebrewPrefix(%q) = %v, want %v", tc.exe, got, tc.want)
			}
		})
	}
}

// TestIsUnderHomebrewPrefix_SkipsEmpty — an empty entry in the
// prefix list mustn't make every absolute path match (an empty
// prefix + "/" would prefix-match "/anything"). Guards a regression
// where homebrewPrefixes() picks up an empty `brew --prefix` output.
func TestIsUnderHomebrewPrefix_SkipsEmpty(t *testing.T) {
	if isUnderHomebrewPrefix("/usr/bin/ccmux", []string{""}) {
		t.Error("empty prefix matched /usr/bin/ccmux — would treat every install as Homebrew")
	}
}

// TestEnsureGoodUpstream_UnpushedBranchErrorsClearly — exact
// reproduction of the bug the user hit on
// `fix/daemon-client-fd-leak`: a fresh local branch created with
// `git checkout -b` whose tracking config points at a remote ref
// that was never pushed. Previously the function returned `""`
// from remoteTrackingFor (because @{upstream} errors when the
// remote-tracking ref is missing), fell into the "no upstream
// set" branch, found no origin/<branch> to retarget to, and
// silently returned nil — so pull then failed with git's
// cryptic "no such ref was fetched" message. The fix reads git
// config directly so we detect this state and error clearly.
func TestEnsureGoodUpstream_UnpushedBranchErrorsClearly(t *testing.T) {
	repo := realGitRepoWithRemote(t)
	// Create a topic branch with `git checkout -b`, which sets up
	// branch.X.remote/merge config but doesn't push anything.
	mustGit(t, repo, "checkout", "-b", "fix/never-pushed")
	// Manually configure tracking the way `git push -u` would
	// have, without actually pushing.
	mustGit(t, repo, "config", "branch.fix/never-pushed.remote", "origin")
	mustGit(t, repo, "config", "branch.fix/never-pushed.merge", "refs/heads/fix/never-pushed")

	// Sanity: configuredUpstream sees the config, remoteTrackingFor
	// returns "" because @{upstream} can't resolve.
	if _, _, ok := configuredUpstream(repo, "fix/never-pushed"); !ok {
		t.Fatal("configuredUpstream should see the branch.X.remote/merge config")
	}
	if got := remoteTrackingFor(repo, "fix/never-pushed"); got != "" {
		t.Fatalf("remoteTrackingFor should return empty for missing remote ref, got %q", got)
	}

	err := ensureGoodUpstream(repo, false)
	if err == nil {
		t.Fatal("unpushed branch with configured-but-missing upstream should error, got nil")
	}
	if !strings.Contains(err.Error(), "git push -u origin fix/never-pushed") {
		t.Errorf("error should suggest pushing the branch: %v", err)
	}
}

// TestEnsureGoodUpstream_NoUpstreamSetsOriginSameName — a branch
// with NO upstream at all + a same-named remote branch existing
// gets its upstream set automatically. Less common than the
// deleted-upstream case but it's the same shape of fix.
func TestEnsureGoodUpstream_NoUpstreamSetsOriginSameName(t *testing.T) {
	repo := realGitRepoWithRemote(t)
	mustGit(t, repo, "checkout", "-b", "experiment")
	mustGit(t, repo, "push", "origin", "experiment") // no -u
	// At this point origin/experiment exists but local experiment
	// has no upstream tracking. Verify the setup before running
	// the function.
	if got := remoteTrackingFor(repo, "experiment"); got != "" {
		t.Fatalf("test setup wrong: experiment already has upstream %q", got)
	}

	if err := ensureGoodUpstream(repo, false); err != nil {
		t.Fatalf("function errored: %v", err)
	}
	if got := remoteTrackingFor(repo, "experiment"); !strings.HasSuffix(got, "/origin/experiment") {
		t.Errorf("upstream not set to origin/experiment: got %q", got)
	}
}

// TestTildify_StandardPath — happy path: $HOME prefix is replaced with ~.
func TestTildify_StandardPath(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	if got := tildify(tmp + "/Projects/ccmux"); got != "~/Projects/ccmux" {
		t.Errorf("tildify = %q, want ~/Projects/ccmux", got)
	}
}

// TestTildify_HomeItself — path equal to $HOME tildifies to bare ~.
func TestTildify_HomeItself(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	if got := tildify(tmp); got != "~" {
		t.Errorf("tildify(HOME) = %q, want ~", got)
	}
}

// TestTildify_DoubleSlashInHome pins the macOS-TMPDIR-trailing-slash
// regression: when `mktemp -d "$TMPDIR/foo.XXX"` runs on macOS,
// TMPDIR's trailing / produces a path with a double slash, which
// propagates into $HOME. A naive HasPrefix-only tildify would fail
// to match. filepath.Clean normalizes both sides so the prefix check
// still hits.
func TestTildify_DoubleSlashInHome(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp+"//extra") // simulate double slash
	if got := tildify(tmp + "/extra/Projects/ccmux"); got != "~/Projects/ccmux" {
		t.Errorf("tildify with double-slash HOME = %q, want ~/Projects/ccmux", got)
	}
}

// TestTildify_PathOutsideHome — paths outside $HOME pass through unchanged.
func TestTildify_PathOutsideHome(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	if got := tildify("/usr/local/bin/ccmux"); got != "/usr/local/bin/ccmux" {
		t.Errorf("tildify outside HOME = %q, want passthrough", got)
	}
}

// TestTildify_PrefixIsNotComponentMatch — /tmp/foobar must NOT match
// when $HOME is /tmp/foo. Without the filepath.Separator suffix on
// the prefix check, "/tmp/foo" prefix-matches "/tmp/foobar" → bug.
func TestTildify_PrefixIsNotComponentMatch(t *testing.T) {
	t.Setenv("HOME", "/tmp/foo")
	if got := tildify("/tmp/foobar/x"); got != "/tmp/foobar/x" {
		t.Errorf("tildify must not match partial path components: got %q", got)
	}
}
