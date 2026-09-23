package tui

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
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
