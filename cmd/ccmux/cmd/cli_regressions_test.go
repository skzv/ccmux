//go:build !windows

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hasCall reports whether any recorded tmux invocation contains all of
// the given "|"-delimited fragments.
func hasCall(calls []string, fragments ...string) bool {
	for _, c := range calls {
		ok := true
		for _, f := range fragments {
			if !strings.Contains("|"+c, "|"+f+"|") {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// --- ccmux attach <project> -------------------------------------------

// TestAttach_BareNameResolvesUnderProjectsRoot — the README flow is
// `ccmux new auth-redesign` then `ccmux attach auth-redesign`. From ~,
// attach resolved the name against the CWD (~/auth-redesign); tmux
// silently falls back to $HOME for a missing -c dir, so `claude
// --continue` resumed the wrong conversation.
func TestAttach_BareNameResolvesUnderProjectsRoot(t *testing.T) {
	e := newCLIEnv(t)
	proj := e.mkdir("Projects/auth-redesign")

	res := e.run("", "attach", "auth-redesign")
	if res.code != 0 {
		t.Fatalf("attach exit %d\nstderr: %s", res.code, res.stderr)
	}
	news := e.tmuxCallsWith("new-session")
	if !hasCall(news, "-s", "c-auth-redesign", "-c", proj) {
		t.Errorf("session must start in %s; tmux calls:\n%s", proj, strings.Join(e.tmuxCalls(), "\n"))
	}
	if !hasCall(e.tmuxCallsWith("attach-session"), "=c-auth-redesign") {
		t.Errorf("expected an attach to c-auth-redesign; tmux calls:\n%s", strings.Join(e.tmuxCalls(), "\n"))
	}
}

// TestAttach_MissingDirectoryErrorsInsteadOfLaunching — a name that is
// neither a project nor a directory must fail, not start an agent in
// whatever directory tmux falls back to.
func TestAttach_MissingDirectoryErrorsInsteadOfLaunching(t *testing.T) {
	e := newCLIEnv(t)
	e.mkdir("Projects")

	res := e.run("", "attach", "nope")
	if res.code == 0 {
		t.Fatalf("attach of a missing project should fail; stdout: %s", res.stdout)
	}
	if !strings.Contains(res.stderr, "nope") {
		t.Errorf("error should name the missing project: %s", res.stderr)
	}
	if news := e.tmuxCallsWith("new-session"); len(news) != 0 {
		t.Errorf("no session may be launched for a missing directory, got: %v", news)
	}
}

// TestAttach_ExplicitPathStillRelativeToCWD — `./dir` (anything with a
// separator) keeps the path semantics.
func TestAttach_ExplicitPathStillRelativeToCWD(t *testing.T) {
	e := newCLIEnv(t)
	local := e.mkdir("work/scratch")
	e.mkdir("Projects/scratch") // same basename under the root must NOT win

	res := e.run(filepath.Join(e.home, "work"), "attach", "./scratch")
	if res.code != 0 {
		t.Fatalf("attach exit %d\nstderr: %s", res.code, res.stderr)
	}
	if !hasCall(e.tmuxCallsWith("new-session"), "-c", local) {
		t.Errorf("./scratch must resolve against the CWD (%s); tmux calls:\n%s", local, strings.Join(e.tmuxCalls(), "\n"))
	}
}

// TestAttach_ProjectsFlagOverridesRoot — the global --projects flag
// scopes the bare-name lookup like it scopes the TUI.
func TestAttach_ProjectsFlagOverridesRoot(t *testing.T) {
	e := newCLIEnv(t)
	e.mkdir("Projects/api")
	other := e.mkdir("code/api")

	res := e.run("", "--projects", filepath.Join(e.home, "code"), "attach", "api")
	if res.code != 0 {
		t.Fatalf("attach exit %d\nstderr: %s", res.code, res.stderr)
	}
	if !hasCall(e.tmuxCallsWith("new-session"), "-c", other) {
		t.Errorf("--projects root must win (%s); tmux calls:\n%s", other, strings.Join(e.tmuxCalls(), "\n"))
	}
}

// --- attach from inside tmux ----------------------------------------------

// TestAttach_InsideTmuxSwitchesClient — from inside a tmux pane,
// `tmux attach-session` fails ("sessions should be nested with care")
// after the session was already created. The CLI must switch the
// current client instead, as the TUI does.
func TestAttach_InsideTmuxSwitchesClient(t *testing.T) {
	e := newCLIEnv(t)
	e.mkdir("Projects/web")
	e.env["TMUX"] = filepath.Join(e.home, "tmp", "tmux-fake", "default") + ",4242,0"
	e.env["FAKE_TMUX_SESSIONS"] = "c-web"

	res := e.run("", "attach", "web")
	if res.code != 0 {
		t.Fatalf("attach exit %d\nstderr: %s", res.code, res.stderr)
	}
	if !hasCall(e.tmuxCallsWith("switch-client"), "-t", "=c-web") {
		t.Errorf("inside tmux, attach must switch-client to =c-web; tmux calls:\n%s", strings.Join(e.tmuxCalls(), "\n"))
	}
	if got := e.tmuxCallsWith("attach-session"); len(got) != 0 {
		t.Errorf("attach-session must not run inside tmux (it's refused there): %v", got)
	}
}

// TestAttach_OutsideTmuxAttaches — the standalone path is unchanged.
func TestAttach_OutsideTmuxAttaches(t *testing.T) {
	e := newCLIEnv(t)
	e.mkdir("Projects/web")
	e.env["FAKE_TMUX_SESSIONS"] = "c-web"

	res := e.run("", "attach", "web")
	if res.code != 0 {
		t.Fatalf("attach exit %d\nstderr: %s", res.code, res.stderr)
	}
	if !hasCall(e.tmuxCallsWith("attach-session"), "-t", "=c-web") {
		t.Errorf("outside tmux, attach must attach-session; tmux calls:\n%s", strings.Join(e.tmuxCalls(), "\n"))
	}
	if got := e.tmuxCallsWith("switch-client"); len(got) != 0 {
		t.Errorf("switch-client must not run outside tmux: %v", got)
	}
}

// --- ccmux new / project honor --projects ----------------------------------

// TestNew_HonorsProjectsFlag — `ccmux --projects X new foo` used to
// create ~/Projects/foo, ignoring the flag.
func TestNew_HonorsProjectsFlag(t *testing.T) {
	e := newCLIEnv(t)
	root := e.mkdir("code")

	res := e.run("", "--projects", root, "new", "beta")
	if res.code != 0 {
		t.Fatalf("new exit %d\nstderr: %s", res.code, res.stderr)
	}
	want := filepath.Join(root, "beta")
	if fi, err := os.Stat(want); err != nil || !fi.IsDir() {
		t.Errorf("project dir %s not created (stat err %v)", want, err)
	}
	if _, err := os.Stat(filepath.Join(e.home, "Projects", "beta")); err == nil {
		t.Errorf("--projects ignored: created ~/Projects/beta")
	}
	if !hasCall(e.tmuxCallsWith("new-session"), "-c", want) {
		t.Errorf("session must start in %s; tmux calls:\n%s", want, strings.Join(e.tmuxCalls(), "\n"))
	}
}

// TestProject_HonorsProjectsFlag — same for `ccmux project`.
func TestProject_HonorsProjectsFlag(t *testing.T) {
	e := newCLIEnv(t)
	e.mkdir("Projects")
	root := e.mkdir("code")
	e.mkdir("code/alpha/.git")

	res := e.run("", "--projects", root, "project", "alpha")
	if res.code != 0 {
		t.Fatalf("project exit %d\nstderr: %s", res.code, res.stderr)
	}
	if !strings.Contains(res.stdout, filepath.Join(root, "alpha")) {
		t.Errorf("project output should show %s:\n%s", filepath.Join(root, "alpha"), res.stdout)
	}
}

// --- ccmux list --json ------------------------------------------------------

// TestListJSON_EmptyIsArray — with no daemon and no sessions the output
// was `null`, which breaks `ccmux list --json | jq '.[]'` and any
// consumer expecting an array.
func TestListJSON_EmptyIsArray(t *testing.T) {
	e := newCLIEnv(t)
	res := e.run("", "list", "--json")
	if res.code != 0 {
		t.Fatalf("list --json exit %d\nstderr: %s", res.code, res.stderr)
	}
	if got := strings.TrimSpace(res.stdout); got != "[]" {
		t.Errorf("list --json with no sessions = %q, want []", got)
	}
}

// --- ccmux kill --------------------------------------------------------------

// TestKill_ResolvesExistingSessionBeforeProject covers every naming
// case. `kill c-foo` used to assume any c- argument was a session name,
// so for a project literally named c-foo it killed project foo's
// session; a bare session like `work` (from `ccmux shell --name work`)
// was mapped to c-work and could never be killed by name.
func TestKill_ResolvesExistingSessionBeforeProject(t *testing.T) {
	cases := []struct {
		name     string
		sessions string
		arg      string
		want     string
	}{
		{"project named c-foo, only its session exists", "c-c-foo", "c-foo", "=c-c-foo"},
		{"project named c-foo, other sessions exist", "c-c-foo c-bar", "c-foo", "=c-c-foo"},
		{"existing session name wins", "c-foo c-c-foo", "c-foo", "=c-foo"},
		{"bare session name", "work", "work", "=work"},
		{"plain project name", "c-web", "web", "=c-web"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newCLIEnv(t)
			e.env["FAKE_TMUX_SESSIONS"] = tc.sessions
			res := e.run("", "kill", tc.arg)
			if res.code != 0 {
				t.Fatalf("kill exit %d\nstderr: %s", res.code, res.stderr)
			}
			kills := e.tmuxCallsWith("kill-session")
			if len(kills) != 1 || !hasCall(kills, "-t", tc.want) {
				t.Errorf("kill %s (sessions %q): kill-session calls = %v, want exactly -t %s", tc.arg, tc.sessions, kills, tc.want)
			}
		})
	}
}

// TestKill_NothingToKillNamesBothCandidates — with neither the literal
// session nor the project's session running, kill must fail without
// sending tmux a kill for a guessed name.
func TestKill_NothingToKillNamesBothCandidates(t *testing.T) {
	e := newCLIEnv(t)
	e.env["FAKE_TMUX_SESSIONS"] = "c-other"
	res := e.run("", "kill", "c-foo")
	if res.code == 0 {
		t.Fatal("kill of a session that doesn't exist should fail")
	}
	if !strings.Contains(res.stderr, `"c-foo"`) || !strings.Contains(res.stderr, `"c-c-foo"`) {
		t.Errorf("error should name both candidates: %s", res.stderr)
	}
	if kills := e.tmuxCallsWith("kill-session"); len(kills) != 0 {
		t.Errorf("no kill-session may be sent for a guessed name: %v", kills)
	}
}

// --- ccmux doctor -------------------------------------------------------------

// doctorEnv stubs every binary doctor requires, so only the test's
// deliberate omissions can fail it.
func doctorEnv(t *testing.T, omit ...string) *cliEnv {
	t.Helper()
	e := newCLIEnv(t)
	skip := map[string]bool{}
	for _, o := range omit {
		skip[o] = true
	}
	for _, b := range []string{"mosh", "tailscale", "claude", "ccmux"} {
		if !skip[b] {
			e.writeExe(b, "exit 0\n")
		}
	}
	return e
}

// TestDoctor_OptionalRipgrepDoesNotFail — rg is labelled optional, but
// a missing rg still counted toward doctor's failure exit code, so a
// healthy machine exited 1.
func TestDoctor_OptionalRipgrepDoesNotFail(t *testing.T) {
	e := doctorEnv(t) // no rg on PATH
	res := e.run("", "doctor")
	if res.code != 0 {
		t.Errorf("doctor exit = %d with only optional rg missing, want 0\n%s", res.code, res.stdout)
	}
	if !strings.Contains(res.stdout, "rg") {
		t.Errorf("doctor should still report rg's absence:\n%s", res.stdout)
	}
}

// TestDoctor_MissingRequiredBinaryFails — the guard: a required binary
// still fails doctor.
func TestDoctor_MissingRequiredBinaryFails(t *testing.T) {
	e := doctorEnv(t, "mosh")
	res := e.run("", "doctor")
	if res.code == 0 {
		t.Errorf("doctor exit = 0 with mosh missing, want non-zero\n%s", res.stdout)
	}
}
