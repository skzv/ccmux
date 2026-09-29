//go:build !windows

package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/tmux"
)

// `ccmux kill` / `rename` (and `attach`, `list`) only ever acted on
// this machine's tmux: the TUI routes a remote row's `x` / `R` / Enter
// to that host's ccmuxd, but the CLI had no way to name a host. --host
// now sends them to a configured host's daemon. These run the real CLI
// against a fake remote ccmuxd (an httptest server configured as host
// "box").

// fakeRemoteHost is a configured host's ccmuxd: it lists sessions and
// projects, and answers kill / rename / create with the status a test
// sets, recording every request as "METHOD /path body".
type fakeRemoteHost struct {
	mu       sync.Mutex
	requests []string
	sessions []daemon.SessionState
	projects []daemon.ProjectInfo
	status   map[string]int // path → status for POSTs; default 204/200
	created  daemon.SessionState
}

func (f *fakeRemoteHost) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.requests = append(f.requests, strings.TrimSpace(r.Method+" "+r.URL.Path+" "+string(body)))
	status := f.status[r.URL.Path]
	sessions, projects, created := f.sessions, f.projects, f.created
	f.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/sessions":
		if sessions == nil {
			sessions = []daemon.SessionState{}
		}
		_ = json.NewEncoder(w).Encode(sessions)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/projects":
		_ = json.NewEncoder(w).Encode(projects)
	case status >= 400:
		http.Error(w, fmt.Sprintf("fake says %d", status), status)
	case r.URL.Path == "/v1/sessions":
		_ = json.NewEncoder(w).Encode(created)
	case strings.HasSuffix(r.URL.Path, "/rename"):
		_ = json.NewEncoder(w).Encode(daemon.SessionState{})
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// reset forgets the requests received so far.
func (f *fakeRemoteHost) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = nil
}

// setSessions replaces the sessions the fake lists.
func (f *fakeRemoteHost) setSessions(ss []daemon.SessionState) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions = ss
}

// all returns every request received so far.
func (f *fakeRemoteHost) all() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

// posts returns the POST requests the fake received.
func (f *fakeRemoteHost) posts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, r := range f.requests {
		if strings.HasPrefix(r, "POST ") {
			out = append(out, r)
		}
	}
	return out
}

// remoteHostConfig writes config.toml with one host, "box", whose
// ccmuxd is at addr ("127.0.0.1:port"), ssh user "me" on port 2222.
func (e *cliEnv) remoteHostConfig(addr string, mosh bool) {
	e.t.Helper()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		e.t.Fatal(err)
	}
	cfg := fmt.Sprintf("[[host]]\nname = \"box\"\naddress = %q\nport = %s\nuser = \"me\"\nssh_port = 2222\nmosh = %v\n", host, port, mosh)
	dir := filepath.Join(e.home, ".config", "ccmux")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfg), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

// remoteEnv is a CLI env whose host "box" is served by a fake ccmuxd.
func remoteEnv(t *testing.T, f *fakeRemoteHost) *cliEnv {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	e := newCLIEnv(t)
	e.remoteHostConfig(strings.TrimPrefix(srv.URL, "http://"), false)
	return e
}

// closedAddr is a loopback address nothing listens on.
func closedAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func TestKillHost_KillsOnThatHost(t *testing.T) {
	f := &fakeRemoteHost{sessions: []daemon.SessionState{{Name: "work", Path: "/srv/work"}}}
	e := remoteEnv(t, f)
	e.env["FAKE_TMUX_SESSIONS"] = "work" // a same-named local session must survive

	res := e.run("", "kill", "--host", "box", "work")
	if res.code != 0 {
		t.Fatalf("kill --host exit %d\nstderr: %s", res.code, res.stderr)
	}
	if got := f.posts(); len(got) != 1 || got[0] != "POST /v1/sessions/work/kill" {
		t.Errorf("remote requests = %v, want one kill of work", got)
	}
	if !strings.Contains(res.stdout, "killed work on box") {
		t.Errorf("stdout = %q, want it to say what was killed where", res.stdout)
	}
	if calls := e.tmuxCalls(); len(calls) != 0 {
		t.Errorf("kill --host touched the local tmux: %v", calls)
	}
}

// TestKillHost_ProjectByDirectory — a project name resolves on the
// host: the host's project of that name, and its own session there
// (the path-tagged one when a same-named project holds c-api).
func TestKillHost_ProjectByDirectory(t *testing.T) {
	tagged := tmux.PathTaggedSessionName("/srv/Projects/api")
	f := &fakeRemoteHost{
		sessions: []daemon.SessionState{{Name: "c-api", Path: "/srv/work/api"}, {Name: tagged, Path: "/srv/Projects/api"}},
		projects: []daemon.ProjectInfo{{Name: "api", Path: "/srv/Projects/api"}},
	}
	e := remoteEnv(t, f)
	res := e.run("", "kill", "--host", "box", "api")
	if res.code != 0 {
		t.Fatalf("kill --host box api exit %d\nstderr: %s", res.code, res.stderr)
	}
	if got := f.posts(); len(got) != 1 || got[0] != "POST /v1/sessions/"+tagged+"/kill" {
		t.Errorf("remote requests = %v, want a kill of %s", got, tagged)
	}

	// No such session or project on the host: nothing is sent.
	f.reset()
	res = e.run("", "kill", "--host", "box", "nope")
	if res.code == 0 || !strings.Contains(res.stderr, "host box") || !strings.Contains(res.stderr, `"nope"`) {
		t.Errorf("kill of a missing session: exit %d, stderr %q", res.code, res.stderr)
	}
	if got := f.posts(); len(got) != 0 {
		t.Errorf("a kill was sent for a guessed name: %v", got)
	}
}

func TestKillHost_Errors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   []string
	}{
		{"404: the session ended in between", http.StatusNotFound, []string{`no session "work" on box`}},
		{"409: the daemon refused", http.StatusConflict, []string{"kill work on box", "fake says 409"}},
		{"500", http.StatusInternalServerError, []string{"kill work on box", "fake says 500"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeRemoteHost{
				sessions: []daemon.SessionState{{Name: "work", Path: "/srv/work"}},
				status:   map[string]int{"/v1/sessions/work/kill": tc.status},
			}
			e := remoteEnv(t, f)
			res := e.run("", "kill", "--host", "box", "work")
			if res.code == 0 {
				t.Fatal("kill --host succeeded against a failing daemon")
			}
			for _, w := range tc.want {
				if !strings.Contains(res.stderr, w) {
					t.Errorf("stderr %q should mention %q", res.stderr, w)
				}
			}
		})
	}
}

func TestRenameHost(t *testing.T) {
	f := &fakeRemoteHost{}
	e := remoteEnv(t, f)
	e.env["FAKE_TMUX_SESSIONS"] = "old"

	res := e.run("", "rename", "--host", "box", "old", "new")
	if res.code != 0 {
		t.Fatalf("rename --host exit %d\nstderr: %s", res.code, res.stderr)
	}
	if got := f.posts(); len(got) != 1 || got[0] != `POST /v1/sessions/old/rename {"name":"new"}` {
		t.Errorf("remote requests = %v, want one rename of old to new", got)
	}
	if !strings.Contains(res.stdout, "renamed old → new on box") {
		t.Errorf("stdout = %q", res.stdout)
	}
	if calls := e.tmuxCallsWith("rename-session"); len(calls) != 0 {
		t.Errorf("rename --host renamed a local session: %v", calls)
	}

	// Names are checked as for a local rename, before anything is sent.
	f.reset()
	for _, args := range [][]string{{"$1", "x"}, {"a:b", "x"}, {"old", "a.b"}, {"old", "-x"}} {
		if res := e.run("", append([]string{"rename", "--host", "box"}, args...)...); res.code == 0 {
			t.Errorf("rename --host box %v accepted", args)
		}
	}
	if got := f.posts(); len(got) != 0 {
		t.Errorf("invalid names reached the host: %v", got)
	}
}

func TestRenameHost_Errors(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   string
	}{
		{http.StatusNotFound, `no session "old" on box`},
		{http.StatusConflict, `box already has a session named "new"`},
		{http.StatusInternalServerError, "fake says 500"},
	} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			f := &fakeRemoteHost{status: map[string]int{"/v1/sessions/old/rename": tc.status}}
			e := remoteEnv(t, f)
			res := e.run("", "rename", "--host", "box", "old", "new")
			if res.code == 0 || !strings.Contains(res.stderr, tc.want) {
				t.Errorf("status %d: exit %d, stderr %q; want an error with %q", tc.status, res.code, res.stderr, tc.want)
			}
		})
	}
}

// TestHostFlag_UnreachableAndUnknown — a host whose daemon doesn't
// answer says so (and which address it tried); a host that isn't
// configured is an error, never a fallback to this machine.
func TestHostFlag_UnreachableAndUnknown(t *testing.T) {
	e := newCLIEnv(t)
	addr := closedAddr(t)
	e.remoteHostConfig(addr, false)
	e.env["FAKE_TMUX_SESSIONS"] = "work"
	for _, args := range [][]string{
		{"kill", "--host", "box", "work"},
		{"rename", "--host", "box", "work", "x"},
		{"attach", "--host", "box", "work"},
		{"list", "--host", "box"},
		// shell printed the client's raw dial error instead.
		{"shell", "--host", "box", "--name", "scratch"},
	} {
		res := e.run("", args...)
		if res.code == 0 || !strings.Contains(res.stderr, "can't reach ccmuxd on box ("+addr+")") {
			t.Errorf("%v: exit %d, stderr %q; want an unreachable-host error", args, res.code, res.stderr)
		}
		res = e.run("", append(append([]string{}, args[0], "--host", "ghost"), args[3:]...)...)
		if res.code == 0 || !strings.Contains(res.stderr, `unknown host "ghost"`) {
			t.Errorf("%v on ghost: exit %d, stderr %q; want an unknown-host error", args[0], res.code, res.stderr)
		}
	}
	if calls := e.tmuxCalls(); len(calls) != 0 {
		t.Errorf("a remote command fell back to local tmux: %v", calls)
	}

	// --host local is this machine.
	res := e.run("", "kill", "--host", "local", "work")
	if res.code != 0 || !hasCall(e.tmuxCallsWith("kill-session"), "-t", exactTarget("work")) {
		t.Errorf("kill --host local: exit %d, stderr %q, tmux %v", res.code, res.stderr, e.tmuxCalls())
	}
}

func TestListHost(t *testing.T) {
	f := &fakeRemoteHost{sessions: []daemon.SessionState{{Name: "c-api", Host: "local", Path: "/srv/api", State: "needs_input"}}}
	e := remoteEnv(t, f)

	res := e.run("", "list", "--host", "box")
	if res.code != 0 {
		t.Fatalf("list --host exit %d\nstderr: %s", res.code, res.stderr)
	}
	if !strings.Contains(res.stdout, "c-api") || !strings.Contains(res.stdout, "box") || !strings.Contains(res.stdout, "needs_input") {
		t.Errorf("list --host output:\n%s", res.stdout)
	}
	res = e.run("", "list", "--host", "box", "--json")
	var got []daemon.SessionState
	if err := json.Unmarshal([]byte(res.stdout), &got); err != nil || len(got) != 1 || got[0].Host != "box" {
		t.Errorf("list --host --json = %s (%v), want c-api with host box", res.stdout, err)
	}
	f.setSessions(nil)
	if res := e.run("", "list", "--host", "box", "--json"); strings.TrimSpace(res.stdout) != "[]" {
		t.Errorf("empty host: list --json = %q, want []", res.stdout)
	}
}

// fakeSSH logs each invocation's argv, one line, args joined by "|".
const fakeSSH = `printf '%s|' "$@" >> "$FAKE_SSH_LOG"
printf '\n' >> "$FAKE_SSH_LOG"
`

func TestAttachHost(t *testing.T) {
	f := &fakeRemoteHost{
		sessions: []daemon.SessionState{{Name: "work", Path: "/srv/work"}},
		created:  daemon.SessionState{Name: "c-api", Path: "/srv/Projects/api"},
	}
	e := remoteEnv(t, f)
	sshLog := filepath.Join(e.home, "ssh.log")
	e.env["FAKE_SSH_LOG"] = sshLog
	e.writeExe("ssh", fakeSSH)
	e.writeExe("mosh", fakeSSH)
	sshCalls := func() []string {
		b, _ := os.ReadFile(sshLog)
		_ = os.Remove(sshLog)
		return strings.Split(strings.TrimSpace(string(b)), "\n")
	}

	// A running session: attached over ssh with the host's user and port.
	if res := e.runTTY("", "attach", "--host", "box", "work"); res.code != 0 {
		t.Fatalf("attach --host exit %d\nstderr: %s", res.code, res.stderr)
	}
	calls := sshCalls()
	if len(calls) != 1 || !hasCall(calls, "-t", "-p", "2222", "me@127.0.0.1") || !strings.Contains(calls[0], "tmux attach-session -t '=work:'") {
		t.Errorf("ssh calls = %v, want -t -p 2222 me@127.0.0.1 … tmux attach-session -t '=work:'", calls)
	}
	if got := f.posts(); len(got) != 0 {
		t.Errorf("attaching to a running session started one: %v", got)
	}

	// A project: its session started (or found) by the host's daemon, then attached.
	if res := e.runTTY("", "attach", "--host", "box", "api"); res.code != 0 {
		t.Fatalf("attach --host box api exit %d\nstderr: %s", res.code, res.stderr)
	}
	if got := f.posts(); len(got) != 1 || !strings.HasPrefix(got[0], "POST /v1/sessions ") || !strings.Contains(got[0], `"project":"api"`) {
		t.Errorf("remote requests = %v, want POST /v1/sessions for project api", got)
	}
	if calls := sshCalls(); len(calls) != 1 || !strings.Contains(calls[0], "'=c-api:'") {
		t.Errorf("ssh calls = %v, want an attach to c-api", calls)
	}

	// No terminal: say how to attach instead of failing in ssh.
	res := e.run("", "attach", "--host", "box", "work")
	if res.code != 0 || !strings.Contains(res.stdout, "ccmux attach --host box work") {
		t.Errorf("attach --host without a terminal: exit %d, stdout %q", res.code, res.stdout)
	}
	if _, err := os.Stat(sshLog); err == nil {
		t.Errorf("ssh ran without a terminal: %v", sshCalls())
	}

	// Refused before any request: no argument, a local relative path, a session ID.
	f.reset()
	for _, args := range [][]string{{}, {"./api"}, {"$1"}} {
		if res := e.run("", append([]string{"attach", "--host", "box"}, args...)...); res.code == 0 {
			t.Errorf("attach --host box %v succeeded", args)
		}
	}
	if got := f.all(); len(got) != 0 {
		t.Errorf("refused attaches reached the host: %v", got)
	}

	// A host configured for mosh attaches with mosh.
	e.remoteHostConfig(e.hostAddr(t), true)
	if res := e.runTTY("", "attach", "--host", "box", "work"); res.code != 0 {
		t.Fatalf("mosh attach exit %d\nstderr: %s", res.code, res.stderr)
	}
	if calls := sshCalls(); len(calls) != 1 || !hasCall(calls, "--ssh=ssh -p 2222", "me@127.0.0.1", "--", "bash", "-c") {
		t.Errorf("mosh calls = %v", calls)
	}
}

// hostAddr reads host box's ccmuxd address back from config.toml.
func (e *cliEnv) hostAddr(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(e.home, ".config", "ccmux", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var addr, port string
	for _, ln := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(ln, "address = "); ok {
			addr = strings.Trim(v, `"`)
		}
		if v, ok := strings.CutPrefix(ln, "port = "); ok {
			port = v
		}
	}
	return net.JoinHostPort(addr, port)
}

func TestHostFlag_Help(t *testing.T) {
	e := newCLIEnv(t)
	for _, cmd := range []string{"kill", "rename", "attach", "list"} {
		res := e.run("", cmd, "--help")
		if res.code != 0 || !strings.Contains(res.stdout, "--host") || !strings.Contains(res.stdout, "configured host") {
			t.Errorf("%s --help doesn't document --host:\n%s", cmd, res.stdout)
		}
	}
}
