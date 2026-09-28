//go:build !windows

package cmd

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/conversations"
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
	if !hasCall(e.tmuxCallsWith("attach-session"), exactTarget("c-auth-redesign")) {
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

// TestAttach_LiveSessionNameAttachesAsIs — `ccmux attach c-shell-abc`
// (a name `ccmux list` prints for a non-project session) was mapped as
// a project to c-c-shell-abc and failed with "no project … create it
// with `ccmux new`". A live session's name must attach as-is, the way
// `ccmux kill` resolves it.
func TestAttach_LiveSessionNameAttachesAsIs(t *testing.T) {
	for _, name := range []string{"c-shell-abc", "work"} {
		t.Run(name, func(t *testing.T) {
			e := newCLIEnv(t)
			e.mkdir("Projects")
			e.env["FAKE_TMUX_SESSIONS"] = name

			res := e.run("", "attach", name)
			if res.code != 0 {
				t.Fatalf("attach %s exit %d\nstderr: %s", name, res.code, res.stderr)
			}
			if !hasCall(e.tmuxCallsWith("attach-session"), "-t", exactTarget(name)) {
				t.Errorf("attach must target the live session %s; tmux calls:\n%s", name, strings.Join(e.tmuxCalls(), "\n"))
			}
			if news := e.tmuxCallsWith("new-session"); len(news) != 0 {
				t.Errorf("no session may be created for a live session name, got: %v", news)
			}
		})
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
	if !hasCall(e.tmuxCallsWith("switch-client"), "-t", exactTarget("c-web")) {
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
	if !hasCall(e.tmuxCallsWith("attach-session"), "-t", exactTarget("c-web")) {
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

// TestList_HungDaemonFallsBackToTmux — a daemon that accepts the
// connection but never answers used up list's whole 3s context, and the
// tmux fallback then ran on that expired context and failed too, so
// `ccmux list` errored instead of listing the local sessions.
func TestList_HungDaemonFallsBackToTmux(t *testing.T) {
	e := newCLIEnv(t)
	// The socket lives under $HOME; keep the path under the 104-byte
	// unix-socket limit, which t.TempDir() on macOS exceeds.
	home, err := os.MkdirTemp("/tmp", "cxl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	e.env["HOME"] = home
	sockDir := filepath.Join(home, ".local", "state", "ccmux")
	if err := os.MkdirAll(sockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", filepath.Join(sockDir, "ccmuxd.sock"))
	if err != nil {
		t.Fatal(err)
	}
	var (
		mu    sync.Mutex
		conns []net.Conn
	)
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	})
	go func() { // a wedged daemon: accept, never respond
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	e.writeExe("tmux", `case "$1" in
list-sessions)
  case "$3" in
  *session_created*) printf 'c-alive\t1700000000\t1700000000\t0\t1\t/work/alive\n' ;;
  *) printf 'c-alive\t\n' ;;
  esac ;;
esac
exit 0
`)

	res := e.run("", "list", "--json")
	if res.code != 0 {
		t.Fatalf("list --json with a hung daemon exit %d\nstderr: %s", res.code, res.stderr)
	}
	if !strings.Contains(res.stdout, `"c-alive"`) {
		t.Errorf("list should fall back to tmux's sessions, got:\n%s", res.stdout)
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
		{"project named c-foo, only its session exists", "c-c-foo", "c-foo", "c-c-foo"},
		{"project named c-foo, other sessions exist", "c-c-foo c-bar", "c-foo", "c-c-foo"},
		{"existing session name wins", "c-foo c-c-foo", "c-foo", "c-foo"},
		{"bare session name", "work", "work", "work"},
		{"plain project name", "c-web", "web", "c-web"},
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
			if len(kills) != 1 || !hasCall(kills, "-t", exactTarget(tc.want)) {
				t.Errorf("kill %s (sessions %q): kill-session calls = %v, want exactly -t %s", tc.arg, tc.sessions, kills, exactTarget(tc.want))
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

// --- session-ID targets --------------------------------------------------------

// fakeTmuxResolvingIDs is fakeTmux plus real tmux's session-ID quirk:
// a target "=$<n>:" is the session with ID $<n> even in the exact form,
// so has-session succeeds for $0..$4 whatever the sessions are called
// (TestIntegration_TargetIDPrefixes pins that on a real tmux).
const fakeTmuxResolvingIDs = `printf '%s|' "$@" >> "$FAKE_TMUX_LOG"
printf '\n' >> "$FAKE_TMUX_LOG"
case "$1" in
has-session)
  case "$3" in '=$'[0-4]*) exit 0 ;; esac
  exit 1 ;;
list-sessions) echo "no server running on /tmp/tmux-fake/default" >&2; exit 1 ;;
esac
exit 0
`

// TestCLI_SessionIDTargetsAreRefused — `ccmux kill '$1'` exited 0 after
// killing whichever session had tmux ID $1, `rename '$4' x` renamed
// one, and `attach '$0'` attached to one: tmux reads a leading "$" as a
// session ID even in the exact `=name:` target. Each must now fail
// with a clear error before asking tmux anything (kill doesn't fall
// through to a project named "$1" either).
func TestCLI_SessionIDTargetsAreRefused(t *testing.T) {
	for _, args := range [][]string{
		{"kill", "$1"},
		{"rename", "$4", "x"},
		{"attach", "$0"},
	} {
		t.Run(args[0], func(t *testing.T) {
			e := newCLIEnv(t)
			e.writeExe("tmux", fakeTmuxResolvingIDs)
			e.mkdir("Projects/$1") // a project so named must not be what kill finds
			res := e.run("", args...)
			if res.code == 0 {
				t.Fatalf("ccmux %v exited 0; tmux calls:\n%s", args, strings.Join(e.tmuxCalls(), "\n"))
			}
			if !strings.Contains(res.stderr, "session ID") || !strings.Contains(res.stderr, `"`+args[1]+`"`) {
				t.Errorf("error should name %q and say tmux reads it as a session ID: %s", args[1], res.stderr)
			}
			if calls := e.tmuxCalls(); len(calls) != 0 {
				t.Errorf("no tmux call may be made for %q, got:\n%s", args[1], strings.Join(calls, "\n"))
			}
		})
	}
}

// TestRename_NewNameMustBeASessionName — the new name is held to the
// rename form's stricter rule, so it can't be "$4" (or "%1") either.
func TestRename_NewNameMustBeASessionName(t *testing.T) {
	for _, newName := range []string{"$4", "%1", "@1", "a.b"} {
		e := newCLIEnv(t)
		e.env["FAKE_TMUX_SESSIONS"] = "work"
		res := e.run("", "rename", "work", newName)
		if res.code == 0 || !strings.Contains(res.stderr, "invalid session name") {
			t.Errorf("rename work %q: exit %d, stderr %q; want an invalid-name error", newName, res.code, res.stderr)
		}
		if got := e.tmuxCallsWith("rename-session"); len(got) != 0 {
			t.Errorf("rename work %q sent %v", newName, got)
		}
	}
}

// TestRename_OldNameMustBeATarget — an old name a tmux target can't
// carry is refused rather than sent to tmux ("a:b" reaches session a).
func TestRename_OldNameMustBeATarget(t *testing.T) {
	e := newCLIEnv(t)
	e.env["FAKE_TMUX_SESSIONS"] = "a"
	res := e.run("", "rename", "a:b", "x")
	if res.code == 0 || !strings.Contains(res.stderr, `refusing to rename "a:b"`) {
		t.Errorf("rename a:b x: exit %d, stderr %q; want a refusal", res.code, res.stderr)
	}
	if got := e.tmuxCallsWith("rename-session"); len(got) != 0 {
		t.Errorf("rename a:b sent %v", got)
	}
}

// TestShell_NameMustBeATarget — `ccmux shell --name '$1'` is refused
// before any daemon is asked: a peer running an older ccmuxd would
// create "$1", and the attach that follows would land in session ID $1.
func TestShell_NameMustBeATarget(t *testing.T) {
	e := newCLIEnv(t)
	var mu sync.Mutex
	var paths []string
	e.fakeDaemon(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		http.Error(w, "unexpected request", http.StatusTeapot)
	}))
	res := e.run("", "shell", "--name", "$1", "--agent", "shell")
	if res.code == 0 || !strings.Contains(res.stderr, "session ID") {
		t.Errorf("shell --name '$1': exit %d, stderr %q; want a session-ID refusal", res.code, res.stderr)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 0 {
		t.Errorf("the daemon was asked %v; the name must be refused first", paths)
	}
}

// --- conversation IDs --------------------------------------------------------

// seedClaudeTranscript writes a minimal Claude Code transcript for id
// under $HOME/.claude/projects and returns its path. The conversation
// ran in $HOME/work/app, which exists, so it can be resumed.
func (e *cliEnv) seedClaudeTranscript(id, prompt string) string {
	e.t.Helper()
	return e.seedClaudeTranscriptIn(e.mkdir("work/app"), id, prompt)
}

// seedClaudeTranscriptIn is seedClaudeTranscript for a conversation
// that ran in cwd, which needn't exist.
func (e *cliEnv) seedClaudeTranscriptIn(cwd, id, prompt string) string {
	e.t.Helper()
	dir := e.mkdir(filepath.Join(".claude", "projects", strings.ReplaceAll(cwd, "/", "-")))
	p := filepath.Join(dir, id+".jsonl")
	line, err := json.Marshal(map[string]any{
		"type":      "user",
		"cwd":       cwd,
		"message":   map[string]string{"role": "user", "content": prompt},
		"timestamp": "2026-09-01T10:00:00.000Z",
	})
	if err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, append(line, '\n'), 0o644); err != nil {
		e.t.Fatal(err)
	}
	return p
}

// TestConversationIDs_TableFormRoundTrips — list-conversations prints
// IDs cut to 11 characters plus "…", but resume and delete-conversation
// took only exact IDs, so pasting what the table showed failed with
// "no conversation with id".
func TestConversationIDs_TableFormRoundTrips(t *testing.T) {
	e := newCLIEnv(t)
	id := "3f2a1b4c-5d6e-4f70-8a9b-0c1d2e3f4a5b"
	path := e.seedClaudeTranscript(id, "fix the login redirect")

	list := e.run("", "list-conversations")
	short := id[:11] + "…"
	if list.code != 0 || !strings.Contains(list.stdout, short) {
		t.Fatalf("list-conversations (exit %d) should show %q:\n%s%s", list.code, short, list.stdout, list.stderr)
	}

	res := e.run("", "resume", short)
	if res.code != 0 {
		t.Fatalf("resume %s exit %d\nstderr: %s", short, res.code, res.stderr)
	}
	if !hasCall(e.tmuxCallsWith("new-session"), "-s", conversations.ResumeSessionName(id)) {
		t.Errorf("resume should start the conversation's session; tmux calls:\n%s", strings.Join(e.tmuxCalls(), "\n"))
	}

	res = e.run("", "delete-conversation", "--force", short)
	if res.code != 0 {
		t.Fatalf("delete-conversation %s exit %d\nstderr: %s", short, res.code, res.stderr)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("transcript still on disk after delete-conversation %s (stat err %v)", short, err)
	}
}

// TestResume_TagsSessionInTheCreatingTmuxCall — `ccmux resume` ran
// `new-session` and then a separate `set-option @ccmux_agent`; a daemon
// poll tick in between classified the brand-new session with the
// project's agent. The tag must be part of the new-session invocation
// (tmux's `;` command separator), with no set-option call of its own.
func TestResume_TagsSessionInTheCreatingTmuxCall(t *testing.T) {
	e := newCLIEnv(t)
	id := "3f2a1b4c-5d6e-4f70-8a9b-0c1d2e3f4a5b"
	e.seedClaudeTranscript(id, "fix the login redirect")

	res := e.run("", "resume", id)
	if res.code != 0 {
		t.Fatalf("resume exit %d\nstderr: %s", res.code, res.stderr)
	}
	name := conversations.ResumeSessionName(id)
	if !hasCall(e.tmuxCallsWith("new-session"), "-s", name, ";", "set-option", "@ccmux_agent", "claude") {
		t.Errorf("new-session for %s must carry the agent tag in the same call; tmux calls:\n%s", name, strings.Join(e.tmuxCalls(), "\n"))
	}
	if hasCall(e.tmuxCallsWith("set-option"), "@ccmux_agent") {
		t.Errorf("a separate set-option @ccmux_agent call leaves the session untagged in between; tmux calls:\n%s", strings.Join(e.tmuxCalls(), "\n"))
	}
}

// TestResume_RefusesMissingProjectFolder — `ccmux resume` of a
// conversation whose project folder was deleted or moved started the
// session anyway; tmux put it in $HOME, where the agent can't find the
// conversation. It must refuse with the folder named and create
// nothing, as the TUI does — but still reattach to a session an earlier
// resume left running.
func TestResume_RefusesMissingProjectFolder(t *testing.T) {
	e := newCLIEnv(t)
	id := "3f2a1b4c-5d6e-4f70-8a9b-0c1d2e3f4a5b"
	gone := filepath.Join(e.home, "deleted", "app")
	e.seedClaudeTranscriptIn(gone, id, "fix the login redirect")

	res := e.run("", "resume", id)
	if res.code == 0 {
		t.Fatalf("resume in a missing folder succeeded; stdout: %s", res.stdout)
	}
	if !strings.Contains(res.stderr, gone) || !strings.Contains(res.stderr, "no longer exists") {
		t.Errorf("error doesn't say which folder is missing: %s", res.stderr)
	}
	if calls := e.tmuxCallsWith("new-session"); len(calls) != 0 {
		t.Errorf("a session was created for a missing folder: %v", calls)
	}

	name := conversations.ResumeSessionName(id)
	e.env["FAKE_TMUX_SESSIONS"] = name
	if res := e.run("", "resume", id); res.code != 0 {
		t.Errorf("a running resume session wasn't reattached: exit %d\n%s", res.code, res.stderr)
	}
	if calls := e.tmuxCallsWith("new-session"); len(calls) != 0 {
		t.Errorf("reattaching created a session: %v", calls)
	}
}

// TestResume_AgentListCoversEveryAgent — the --agent help and the
// unknown-agent error hard-coded seven agents, so every agent added
// since (gemini, opencode, kiro, …) was missing from both.
func TestResume_AgentListCoversEveryAgent(t *testing.T) {
	e := newCLIEnv(t)
	e.seedClaudeTranscript("3f2a1b4c-5d6e-4f70-8a9b-0c1d2e3f4a5b", "hello")

	help := e.run("", "resume", "--help")
	bad := e.run("", "resume", "--agent", "nosuchagent")
	if bad.code == 0 {
		t.Fatalf("resume --agent nosuchagent should fail; stdout: %s", bad.stdout)
	}
	for _, a := range agent.All() {
		id := string(a.ID())
		if !strings.Contains(help.stdout, id) {
			t.Errorf("resume --help doesn't list agent %q:\n%s", id, help.stdout)
		}
		if !strings.Contains(bad.stderr, id) {
			t.Errorf("unknown-agent error doesn't list agent %q: %s", id, bad.stderr)
		}
	}
}

// --- ccmux mcp unregister / uninstall ----------------------------------------

// TestMCPStatusAndRegister_ForeignEntry — `mcp status` called any
// "ccmux" entry registered and `mcp register --allow-mutate` overwrote
// one that runs some other server. Status must say it isn't ccmux-mcp;
// register must refuse without --force and replace it with it.
func TestMCPStatusAndRegister_ForeignEntry(t *testing.T) {
	e := newCLIEnv(t)
	cfgPath := filepath.Join(e.home, ".claude.json")
	body := `{"mcpServers":{"ccmux":{"type":"stdio","command":"/opt/tools/my-ccmux-bridge"}}}`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	st := e.run("", "mcp", "status")
	if st.code != 0 || !strings.Contains(st.stdout, "NOT registered") || !strings.Contains(st.stdout, "my-ccmux-bridge") {
		t.Errorf("mcp status (exit %d) should report the foreign entry as not ccmux-mcp:\n%s%s", st.code, st.stdout, st.stderr)
	}

	reg := e.run("", "mcp", "register", "--allow-mutate")
	if reg.code == 0 {
		t.Errorf("register over a foreign entry should fail without --force:\n%s", reg.stdout)
	}
	if raw, _ := os.ReadFile(cfgPath); string(raw) != body {
		t.Errorf("register without --force changed ~/.claude.json:\n%s", raw)
	}

	forced := e.run("", "mcp", "register", "--allow-mutate", "--force")
	if forced.code != 0 {
		t.Fatalf("register --force exit %d\n%s%s", forced.code, forced.stdout, forced.stderr)
	}
	if raw, _ := os.ReadFile(cfgPath); !strings.Contains(string(raw), `"ccmux-mcp"`) {
		t.Errorf("register --force didn't install ccmux-mcp:\n%s", raw)
	}
	if st := e.run("", "mcp", "status"); !strings.Contains(st.stdout, "✓ ccmux-mcp is registered") {
		t.Errorf("after --force, status = %s", st.stdout)
	}
}

// TestMCPUnregister_RemovesEntryKeepsTheRest — `ccmux mcp unregister`
// (no claude CLI on PATH, so it edits ~/.claude.json directly) removes
// only the ccmux server and is a no-op when re-run.
func TestMCPUnregister_RemovesEntryKeepsTheRest(t *testing.T) {
	e := newCLIEnv(t)
	cfgPath := filepath.Join(e.home, ".claude.json")
	body := `{"numStartups": 5, "mcpServers": {"ccmux": {"type": "stdio", "command": "ccmux-mcp", "args": []}, "pg": {"type": "stdio", "command": "pg-mcp"}}}`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	res := e.run("", "mcp", "unregister")
	if res.code != 0 {
		t.Fatalf("mcp unregister exit %d\nstderr: %s", res.code, res.stderr)
	}
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		NumStartups int                        `json:"numStartups"`
		MCPServers  map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("~/.claude.json no longer valid JSON: %v\n%s", err, raw)
	}
	if _, ok := got.MCPServers["ccmux"]; ok {
		t.Errorf("ccmux entry still registered:\n%s", raw)
	}
	if _, ok := got.MCPServers["pg"]; !ok || got.NumStartups != 5 {
		t.Errorf("unrelated config lost:\n%s", raw)
	}

	again := e.run("", "mcp", "unregister")
	if again.code != 0 || !strings.Contains(again.stdout, "nothing to remove") {
		t.Errorf("second unregister (exit %d) should be a no-op:\n%s%s", again.code, again.stdout, again.stderr)
	}
}

// TestUninstallDryRun_ListsMCPUnregister — the plan printed before the
// y/N prompt names the MCP cleanup, and --dry-run touches nothing.
func TestUninstallDryRun_ListsMCPUnregister(t *testing.T) {
	e := newCLIEnv(t)
	cfgPath := filepath.Join(e.home, ".claude.json")
	body := `{"mcpServers": {"ccmux": {"type": "stdio", "command": "ccmux-mcp", "args": []}}}`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	res := e.run("", "uninstall", "--dry-run")
	if res.code != 0 {
		t.Fatalf("uninstall --dry-run exit %d\nstderr: %s", res.code, res.stderr)
	}
	if !strings.Contains(res.stdout, "unregister the ccmux MCP server from Claude Code") {
		t.Errorf("uninstall plan doesn't mention the MCP registration:\n%s", res.stdout)
	}
	if raw, _ := os.ReadFile(cfgPath); string(raw) != body {
		t.Errorf("--dry-run modified ~/.claude.json:\n%s", raw)
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
