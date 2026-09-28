package tui

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tailnet"
)

// fakeCcmuxd is a minimal ccmuxd over httptest. A hung daemon accepts
// the connection and never answers (a sleeping laptop, a wedged
// handler) until the client gives up.
type fakeCcmuxd struct {
	srv  *httptest.Server
	host string // "127.0.0.1"
	port int
}

func (f fakeCcmuxd) addr() string { return net.JoinHostPort(f.host, strconv.Itoa(f.port)) }

func startFakeCcmuxd(t *testing.T, hung bool, sessions []daemon.SessionState, projects []daemon.ProjectInfo) fakeCcmuxd {
	t.Helper()
	release := make(chan struct{})
	mux := http.NewServeMux()
	block := func(r *http.Request) bool {
		if !hung {
			return false
		}
		select {
		case <-r.Context().Done():
		case <-release:
		}
		return true
	}
	mux.HandleFunc("/v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		if block(r) {
			return
		}
		_ = json.NewEncoder(w).Encode(sessions)
	})
	mux.HandleFunc("/v1/health", func(w http.ResponseWriter, r *http.Request) {
		if block(r) {
			return
		}
		_ = json.NewEncoder(w).Encode(daemon.HealthInfo{OK: true, Version: "v-test", Sessions: len(sessions)})
	})
	mux.HandleFunc("/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		if block(r) {
			return
		}
		_ = json.NewEncoder(w).Encode(projects)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(func() {
		close(release)
		srv.Close()
	})
	h, p, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(p)
	return fakeCcmuxd{srv: srv, host: h, port: port}
}

// stubRefreshSeams shrinks the per-probe budgets and replaces the local
// probe + tailnet scan so a refresh test never reaches the real daemon
// socket, tmux server, or `tailscale`.
func stubRefreshSeams(t *testing.T, scan func(ctx context.Context, port int) (tailnet.Scan, error)) {
	t.Helper()
	origHost, origScanT := hostProbeTimeout, tailnetScanTimeout
	origLocal, origScan := probeLocalSessions, scanTailnet
	hostProbeTimeout = 300 * time.Millisecond
	tailnetScanTimeout = 300 * time.Millisecond
	probeLocalSessions = func() localSessionsProbe {
		return localSessionsProbe{
			sessions: []daemon.SessionState{{Name: "c-here", Host: "local", State: "idle"}},
			host:     &hostStatus{Name: "sputnik", Local: true, Source: "local", OK: true, DaemonOK: true},
		}
	}
	if scan == nil {
		scan = func(context.Context, int) (tailnet.Scan, error) { return tailnet.Scan{}, nil }
	}
	scanTailnet = scan
	t.Cleanup(func() {
		hostProbeTimeout, tailnetScanTimeout = origHost, origScanT
		probeLocalSessions, scanTailnet = origLocal, origScan
	})
}

func hostByName(hs []hostStatus, name string) *hostStatus {
	for i := range hs {
		if hs[i].Name == name {
			return &hs[i]
		}
	}
	return nil
}

func hasSession(ss []daemon.SessionState, host, name string) bool {
	for _, s := range ss {
		if s.Host == host && s.Name == name {
			return true
		}
	}
	return false
}

// TestRefreshSessions_HungHostDoesNotStarveOthers is the regression for
// "one hung host starves the whole refresh": every host (and the
// tailnet scan) used to share one sequential 5s context, so a sleeping
// laptop first in the config marked every later host unreachable and
// the scan ran with an expired context. Each probe now has its own
// budget; healthy hosts come back healthy, and the rows keep a
// deterministic order (local, configured in config order, discovered
// sorted by name) regardless of which probe finished first.
func TestRefreshSessions_HungHostDoesNotStarveOthers(t *testing.T) {
	cases := []struct {
		name      string
		setup     func(t *testing.T) ([]config.Host, func(ctx context.Context, port int) (tailnet.Scan, error))
		wantOrder []string
		wantOK    []string // hosts whose sessions must be listed
		wantDown  []string // hosts that must report an error
	}{
		{
			name: "first configured host hangs",
			setup: func(t *testing.T) ([]config.Host, func(ctx context.Context, port int) (tailnet.Scan, error)) {
				sleepy := startFakeCcmuxd(t, true, nil, nil)
				mini := startFakeCcmuxd(t, false, []daemon.SessionState{{Name: "c-ccmux", State: "idle"}}, nil)
				return []config.Host{
					{Name: "sleepy", Address: sleepy.host, Port: sleepy.port},
					{Name: "mac-mini", Address: mini.host, Port: mini.port},
				}, nil
			},
			wantOrder: []string{"sputnik", "sleepy", "mac-mini"},
			wantOK:    []string{"mac-mini"},
			wantDown:  []string{"sleepy"},
		},
		{
			name: "tailnet scan hangs",
			setup: func(t *testing.T) ([]config.Host, func(ctx context.Context, port int) (tailnet.Scan, error)) {
				mini := startFakeCcmuxd(t, false, []daemon.SessionState{{Name: "c-ccmux", State: "idle"}}, nil)
				hangScan := func(ctx context.Context, _ int) (tailnet.Scan, error) {
					<-ctx.Done()
					return tailnet.Scan{}, ctx.Err()
				}
				return []config.Host{{Name: "mac-mini", Address: mini.host, Port: mini.port}}, hangScan
			},
			wantOrder: []string{"sputnik", "mac-mini"},
			wantOK:    []string{"mac-mini"},
		},
		{
			name: "discovered peer hangs",
			setup: func(t *testing.T) ([]config.Host, func(ctx context.Context, port int) (tailnet.Scan, error)) {
				slow := startFakeCcmuxd(t, true, nil, nil)
				zeta := startFakeCcmuxd(t, false, []daemon.SessionState{{Name: "c-web", State: "idle"}}, nil)
				scan := func(context.Context, int) (tailnet.Scan, error) {
					// Completion order, not name order — the rows must
					// still come out sorted.
					return tailnet.Scan{Reachable: []tailnet.Discovered{
						{Name: "zeta", Address: zeta.addr()},
						{Name: "alpha", Address: slow.addr()},
					}}, nil
				}
				return nil, scan
			},
			wantOrder: []string{"sputnik", "alpha", "zeta"},
			wantOK:    []string{"zeta"},
			wantDown:  []string{"alpha"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hosts, scan := tc.setup(t)
			stubRefreshSeams(t, scan)
			a := App{cfg: config.Config{Hosts: hosts}}

			start := time.Now()
			msg, ok := a.refreshSessionsCmd()().(sessionsLoadedMsg)
			elapsed := time.Since(start)
			if !ok {
				t.Fatal("refreshSessionsCmd did not return sessionsLoadedMsg")
			}
			// One budget (plus the scan's), not one per host in series.
			if elapsed > 2*time.Second {
				t.Errorf("refresh took %v; a hung host must only cost its own budget", elapsed)
			}

			var order []string
			for _, h := range msg.Hosts {
				order = append(order, h.Name)
			}
			if len(order) != len(tc.wantOrder) {
				t.Fatalf("hosts = %v, want %v", order, tc.wantOrder)
			}
			for i := range order {
				if order[i] != tc.wantOrder[i] {
					t.Fatalf("hosts = %v, want %v", order, tc.wantOrder)
				}
			}
			for _, name := range tc.wantOK {
				h := hostByName(msg.Hosts, name)
				if h == nil || !h.OK || h.Err != nil || h.Sessions != 1 {
					t.Errorf("host %s should be healthy with 1 session, got %+v", name, h)
				}
				found := false
				for _, s := range msg.Sessions {
					if s.Host == name {
						found = true
					}
				}
				if !found {
					t.Errorf("sessions from healthy host %s missing: %+v", name, msg.Sessions)
				}
			}
			for _, name := range tc.wantDown {
				if h := hostByName(msg.Hosts, name); h == nil || h.Err == nil {
					t.Errorf("hung host %s should report an error, got %+v", name, h)
				}
			}
			if !hasSession(msg.Sessions, "local", "c-here") {
				t.Errorf("local sessions missing: %+v", msg.Sessions)
			}
		})
	}
}

// TestRefreshProjects_HungHostDoesNotStarveOthers — same starvation on
// the Projects refresh: a hung first host must not drop a healthy
// host's projects.
func TestRefreshProjects_HungHostDoesNotStarveOthers(t *testing.T) {
	stubRefreshSeams(t, nil)
	sleepy := startFakeCcmuxd(t, true, nil, nil)
	mini := startFakeCcmuxd(t, false, nil, []daemon.ProjectInfo{{Name: "alpha", Path: "/Users/skz/Projects/alpha"}})
	a := App{cfg: config.Config{
		Projects: config.ProjectsConfig{Root: t.TempDir()},
		Hosts: []config.Host{
			{Name: "sleepy", Address: sleepy.host, Port: sleepy.port},
			{Name: "mac-mini", Address: mini.host, Port: mini.port},
		},
	}}
	start := time.Now()
	msg := a.refreshProjectsCmd()().(projectsLoadedMsg)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("projects refresh took %v; a hung host must only cost its own budget", elapsed)
	}
	if len(msg.Projects) != 1 || msg.Projects[0].Name != "alpha" || msg.Projects[0].Host != "mac-mini" {
		t.Fatalf("projects = %+v, want mac-mini's alpha", msg.Projects)
	}
}

// TestRefreshProjects_RemoteAgentKept — the remote daemon reports each
// project's agent, but fetchRemoteProjects dropped it, so every remote
// project showed (and was labelled) as Claude.
func TestRefreshProjects_RemoteAgentKept(t *testing.T) {
	stubRefreshSeams(t, nil)
	mini := startFakeCcmuxd(t, false, nil, []daemon.ProjectInfo{
		{Name: "api", Path: "/Users/me/Projects/api", Agent: "codex"},
		{Name: "legacy", Path: "/Users/me/Projects/legacy"},
	})
	a := App{cfg: config.Config{
		Projects: config.ProjectsConfig{Root: t.TempDir()},
		Hosts:    []config.Host{{Name: "mac-mini", Address: mini.host, Port: mini.port}},
	}}
	msg := a.refreshProjectsCmd()().(projectsLoadedMsg)
	agents := map[string]agent.ID{}
	for _, p := range msg.Projects {
		agents[p.Name] = p.Agent
	}
	if agents["api"] != agent.IDCodex {
		t.Errorf("remote codex project has agent %q, want codex", agents["api"])
	}
	if got, ok := agents["legacy"]; !ok || got != "" {
		t.Errorf("remote project without an agent = %q (present %v), want unset (reads as claude)", got, ok)
	}
}

// TestSessionsLoaded_StaleGenerationDropped — overlapping refreshes
// finish out of order when a host is slow; an older result must not
// overwrite a newer list. Unnumbered (Gen 0) results always apply.
func TestSessionsLoaded_StaleGenerationDropped(t *testing.T) {
	a := newSessionsApp(t)
	deliver := func(gen int, names ...string) {
		t.Helper()
		var ss []daemon.SessionState
		for _, n := range names {
			ss = append(ss, daemon.SessionState{Name: n, Host: "local"})
		}
		a, _ = updateApp(t, a, sessionsLoadedMsg{Sessions: ss, Gen: gen, At: time.Now()})
	}
	names := func() []string {
		var out []string
		for _, s := range a.sessions {
			out = append(out, s.Name)
		}
		return out
	}
	deliver(2, "c-new")
	deliver(1, "c-stale")
	if got := names(); len(got) != 1 || got[0] != "c-new" {
		t.Fatalf("stale generation overwrote the newer list: %v", got)
	}
	deliver(3, "c-newer")
	if got := names(); len(got) != 1 || got[0] != "c-newer" {
		t.Fatalf("newer generation not applied: %v", got)
	}
	deliver(0, "c-unnumbered")
	if got := names(); len(got) != 1 || got[0] != "c-unnumbered" {
		t.Fatalf("unnumbered result not applied: %v", got)
	}
}

// TestTick_DoesNotStackRefreshes — the 2s tick must not issue another
// refresh while its previous one is still in flight (slow hosts used to
// pile refreshes up), but resumes once that one lands or goes overdue.
func TestTick_DoesNotStackRefreshes(t *testing.T) {
	a := newSessionsApp(t)
	a, _ = updateApp(t, a, tickMsg{At: time.Now()})
	if a.sessionsLoadGen != 1 || a.sessionsTickGen != 1 {
		t.Fatalf("first tick: loadGen=%d tickGen=%d, want 1/1", a.sessionsLoadGen, a.sessionsTickGen)
	}
	a, _ = updateApp(t, a, tickMsg{At: time.Now()})
	if a.sessionsLoadGen != 1 {
		t.Fatalf("tick issued a second refresh while the first was in flight (loadGen=%d)", a.sessionsLoadGen)
	}
	a, _ = updateApp(t, a, sessionsLoadedMsg{Gen: 1, At: time.Now()})
	if a.sessionsTickGen != 0 {
		t.Fatalf("landed result did not clear the in-flight tick (tickGen=%d)", a.sessionsTickGen)
	}
	a, _ = updateApp(t, a, tickMsg{At: time.Now()})
	if a.sessionsLoadGen != 2 {
		t.Fatalf("tick after the result landed should refresh (loadGen=%d)", a.sessionsLoadGen)
	}
	// A result that never lands must not stall polling forever.
	a.sessionsTickAt = time.Now().Add(-sessionsTickStaleAfter - time.Second)
	a, _ = updateApp(t, a, tickMsg{At: time.Now()})
	if a.sessionsLoadGen != 3 {
		t.Fatalf("overdue in-flight refresh should not block the tick (loadGen=%d)", a.sessionsLoadGen)
	}
}

// newRefreshGenApp is a real New() App (Init's refreshes carry the
// generation New reserved for them) with HOME sandboxed.
func newRefreshGenApp(t *testing.T) App {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	a := New(config.Defaults(), "test")
	a.tour.Close()
	return a
}

// TestSessionsRefresh_InitGenerationIsReserved — Init has a value
// receiver, so the generation bump in refreshSessionsCmd happened on a
// copy: Init's refresh and the first real one (the 2s tick's) were
// both generation 1. Init's older list could then overwrite the tick's
// newer one, and its landing cleared the tick's in-flight marker early.
func TestSessionsRefresh_InitGenerationIsReserved(t *testing.T) {
	a := newRefreshGenApp(t)
	a, _ = updateApp(t, a, tickMsg{At: time.Now()})
	tickGen := a.sessionsTickGen
	if tickGen <= initRefreshGen {
		t.Fatalf("the first tick's refresh got generation %d, which Init's refresh already carries", tickGen)
	}
	// Init's refresh lands after the tick's: it is older and is dropped,
	// and it must not clear the tick's in-flight marker.
	a, _ = updateApp(t, a, sessionsLoadedMsg{Gen: initRefreshGen, Sessions: []daemon.SessionState{{Name: "c-stale", Host: "local"}}, At: time.Now()})
	if a.sessionsTickGen != tickGen {
		t.Errorf("Init's result cleared the tick's in-flight refresh (tickGen=%d)", a.sessionsTickGen)
	}
	a, _ = updateApp(t, a, sessionsLoadedMsg{Gen: tickGen, Sessions: []daemon.SessionState{{Name: "c-new", Host: "local"}}, At: time.Now()})
	a, _ = updateApp(t, a, sessionsLoadedMsg{Gen: initRefreshGen, Sessions: []daemon.SessionState{{Name: "c-stale", Host: "local"}}, At: time.Now()})
	if len(a.sessions) != 1 || a.sessions[0].Name != "c-new" {
		t.Errorf("sessions = %+v, want the tick's newer list", a.sessions)
	}
}

// TestProjectsLoaded_StaleGenerationDropped — project refreshes overlap
// too (Init, every detach, `r` on Projects) and a slow host makes them
// finish out of order; projectsLoadedMsg carried no generation, so an
// older list could replace a newer one.
func TestProjectsLoaded_StaleGenerationDropped(t *testing.T) {
	stubRefreshSeams(t, nil)
	a := newRefreshGenApp(t)
	a.cfg.Projects.Root = t.TempDir()
	older := a.refreshProjectsCmd()().(projectsLoadedMsg)
	newer := a.refreshProjectsCmd()().(projectsLoadedMsg)
	newer.Projects = []project.Project{{Name: "fresh", Host: "local", Path: "/p/fresh"}}
	older.Projects = []project.Project{{Name: "stale", Host: "local", Path: "/p/stale"}}
	a, _ = updateApp(t, a, newer)
	a, _ = updateApp(t, a, older)
	if len(a.projects) != 1 || a.projects[0].Name != "fresh" {
		t.Errorf("projects = %+v, want the newer refresh's list", a.projects)
	}
}

// localDaemonStub serves /v1/sessions and /v1/health on the local
// ccmuxd socket under a short sandbox HOME. Each handler sleeps its
// delay (or, with a negative delay, hangs until the client gives up).
func localDaemonStub(t *testing.T, sessionsDelay, healthDelay time.Duration) {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "cxh") // unix socket paths must stay short
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
	t.Setenv("TMUX_TMPDIR", home) // the direct-tmux fallback must never see the user's server
	t.Setenv("TMUX", "")
	sock := filepath.Join(home, ".local", "state", "ccmux", "ccmuxd.sock")
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	wait := func(r *http.Request, d time.Duration) bool {
		if d < 0 {
			<-r.Context().Done()
			return false
		}
		select {
		case <-time.After(d):
			return true
		case <-r.Context().Done():
			return false
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		if wait(r, sessionsDelay) {
			_ = json.NewEncoder(w).Encode([]daemon.SessionState{{Name: "c-here", State: "idle"}})
		}
	})
	mux.HandleFunc("/v1/health", func(w http.ResponseWriter, r *http.Request) {
		if wait(r, healthDelay) {
			_ = json.NewEncoder(w).Encode(daemon.HealthInfo{OK: true, Hostname: "fakehost.local", Version: "v-test", Sessions: 1})
		}
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
}

// TestLocalProbe_SlowDaemonStaysOnline — /v1/health ran on the context
// /v1/sessions had just used up, so a slow but live daemon answered the
// session list, then "failed" health: the status bar said offline and
// the local row's name flipped from the hostname to "local". Health has
// its own budget now, and a daemon that listed the sessions is up.
func TestLocalProbe_SlowDaemonStaysOnline(t *testing.T) {
	orig := hostProbeTimeout
	hostProbeTimeout = time.Second
	t.Cleanup(func() { hostProbeTimeout = orig })

	t.Run("slow health", func(t *testing.T) {
		localDaemonStub(t, 700*time.Millisecond, 500*time.Millisecond)
		p := realProbeLocalSessions()
		if p.host == nil || !p.host.DaemonOK {
			t.Fatalf("a slow but live daemon was reported offline: %+v", p.host)
		}
		if p.host.Name != "fakehost" || p.host.Version != "v-test" {
			t.Errorf("local row = %q %q, want the daemon's health (fakehost, v-test)", p.host.Name, p.host.Version)
		}
		if !hasSession(p.sessions, "local", "c-here") {
			t.Errorf("sessions = %+v", p.sessions)
		}
	})
	t.Run("health never answers", func(t *testing.T) {
		localDaemonStub(t, 0, -1)
		p := realProbeLocalSessions()
		if p.host == nil || !p.host.DaemonOK || !p.host.OK {
			t.Fatalf("daemon listed the sessions but was reported down: %+v", p.host)
		}
		hn, _ := os.Hostname()
		if want := shortHostname(hn); want != "" && p.host.Name != want {
			t.Errorf("local row name = %q, want this machine's hostname %q", p.host.Name, want)
		}
	})
}
