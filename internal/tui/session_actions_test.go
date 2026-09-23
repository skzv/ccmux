package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/daemon"
)

// sessionRoutingRecorder swaps the local + remote kill/rename seams for
// recorders so a test can assert WHERE a mutation went without a live
// tmux server or network. Restored on cleanup.
type sessionRoutingRecorder struct {
	mu            sync.Mutex
	localKills    []string
	remoteKills   []string // "addr|host|name"
	localRenames  []string // "old→new"
	remoteRenames []string // "addr|host|old→new"
}

func recordSessionRouting(t *testing.T) *sessionRoutingRecorder {
	t.Helper()
	r := &sessionRoutingRecorder{}
	origKill, origRemoteKill := killSessionCmd, killRemoteSessionCmd
	origRename, origRemoteRename := renameSessionCmd, renameRemoteSessionCmd
	killSessionCmd = func(name string) tea.Cmd {
		return func() tea.Msg {
			r.mu.Lock()
			r.localKills = append(r.localKills, name)
			r.mu.Unlock()
			return sessionKilledMsg{Name: name}
		}
	}
	killRemoteSessionCmd = func(addr, host, name string) tea.Cmd {
		return func() tea.Msg {
			r.mu.Lock()
			r.remoteKills = append(r.remoteKills, addr+"|"+host+"|"+name)
			r.mu.Unlock()
			return sessionKilledMsg{Name: name, Host: host}
		}
	}
	renameSessionCmd = func(oldName, newName string) tea.Cmd {
		return func() tea.Msg {
			r.mu.Lock()
			r.localRenames = append(r.localRenames, oldName+"→"+newName)
			r.mu.Unlock()
			return sessionRenamedMsg{OldName: oldName, NewName: newName}
		}
	}
	renameRemoteSessionCmd = func(addr, host, oldName, newName string) tea.Cmd {
		return func() tea.Msg {
			r.mu.Lock()
			r.remoteRenames = append(r.remoteRenames, addr+"|"+host+"|"+oldName+"→"+newName)
			r.mu.Unlock()
			return sessionRenamedMsg{Host: host, OldName: oldName, NewName: newName}
		}
	}
	t.Cleanup(func() {
		killSessionCmd, killRemoteSessionCmd = origKill, origRemoteKill
		renameSessionCmd, renameRemoteSessionCmd = origRename, origRemoteRename
	})
	return r
}

// twoHostSessionsApp is the reported setup: `c-ccmux` running on this
// Mac ("sputnik") AND on the configured host "mac-mini", plus a
// discovered peer and a peer without ccmuxd. Delivered through the real
// sessionsLoadedMsg path so a.hosts matches what a refresh produces.
func twoHostSessionsApp(t *testing.T) App {
	t.Helper()
	a := newSessionsApp(t)
	a.width, a.height = 120, 40
	m, _ := a.Update(sessionsLoadedMsg{
		Sessions: []daemon.SessionState{
			{Name: "c-ccmux", Host: "local"},
			{Name: "c-ccmux", Host: "mac-mini"},
			{Name: "c-web", Host: "atelier"},
			{Name: "c-ghost", Host: "retired-box"},
		},
		Hosts: []hostStatus{
			{Name: "sputnik", Local: true, Source: "local", Address: "unix:///tmp/s.sock", OK: true, DaemonOK: true},
			{Name: "mac-mini", Source: "configured", Address: "100.64.0.2:7474", DialHost: "mac-mini", OK: true, DaemonOK: true},
			{Name: "atelier", Source: "discovered", Discovered: true, Address: "100.64.0.3:7474", OK: true, DaemonOK: true},
			{Name: "nopeer", Source: "discovered", Discovered: true, NeedsInstall: true, Address: "100.64.0.9:7474"},
		},
		At: time.Now(),
	})
	return m.(App)
}

// selectSession moves the Sessions cursor onto the (host, name) row.
func selectSession(t *testing.T, a App, host, name string) App {
	t.Helper()
	for i, s := range a.sessionsM.sessions {
		if s.Host == host && s.Name == name {
			a.sessionsM.cursor = i
			return a
		}
	}
	t.Fatalf("no session %s on %s in %+v", name, host, a.sessionsM.sessions)
	return a
}

// TestKillSession_RoutesByHost is the regression for "x,y on the
// mac-mini's c-ccmux row killed the LOCAL c-ccmux". Every row is killed
// on the machine it lives on; a remote row never reaches the local
// tmux seam, and a host without a daemon is refused, not retargeted.
func TestKillSession_RoutesByHost(t *testing.T) {
	cases := []struct {
		name        string
		host, sess  string
		wantLocal   []string
		wantRemote  []string
		wantInModal string
		wantToast   string
	}{
		{name: "local row", host: "local", sess: "c-ccmux", wantLocal: []string{"c-ccmux"}},
		{name: "configured remote with same name", host: "mac-mini", sess: "c-ccmux",
			wantRemote: []string{"100.64.0.2:7474|mac-mini|c-ccmux"}, wantInModal: "mac-mini"},
		{name: "discovered peer", host: "atelier", sess: "c-web",
			wantRemote: []string{"100.64.0.3:7474|atelier|c-web"}, wantInModal: "atelier"},
		{name: "unknown host is refused", host: "retired-box", sess: "c-ghost",
			wantInModal: "retired-box", wantToast: "no reachable daemon for host: retired-box"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := recordSessionRouting(t)
			a := selectSession(t, twoHostSessionsApp(t), tc.host, tc.sess)

			a, _ = sendKey(t, a, keyRunes("x"))
			if !a.confirm.open() || a.confirm.target != tc.sess {
				t.Fatalf("x did not open a kill confirmation for %s: %#v", tc.sess, a.confirm)
			}
			view := a.View()
			if tc.wantInModal != "" && !strings.Contains(view, tc.wantInModal) {
				t.Errorf("kill confirmation must name the remote host %q:\n%s", tc.wantInModal, view)
			}

			a, cmd := sendKey(t, a, keyRunes("y"))
			msgs := drainCmd(cmd)

			if strings.Join(rec.localKills, ",") != strings.Join(tc.wantLocal, ",") {
				t.Errorf("local kills = %v, want %v", rec.localKills, tc.wantLocal)
			}
			if strings.Join(rec.remoteKills, ",") != strings.Join(tc.wantRemote, ",") {
				t.Errorf("remote kills = %v, want %v", rec.remoteKills, tc.wantRemote)
			}
			if tc.wantToast != "" {
				found := false
				for _, m := range msgs {
					if tm, ok := m.(toastMsg); ok && tm.Kind == toastError && tm.Text == tc.wantToast {
						found = true
					}
				}
				if !found {
					t.Errorf("want error toast %q, got msgs %#v", tc.wantToast, msgs)
				}
			}
		})
	}
}

// TestKillSession_LocalHostnameRowIsLocal — a row labelled with this
// machine's own hostname is local and must use the local seam.
func TestKillSession_LocalHostnameRowIsLocal(t *testing.T) {
	rec := recordSessionRouting(t)
	a := twoHostSessionsApp(t)
	drainCmd(a.killSessionTargetCmd("sputnik", "c-ccmux"))
	if len(rec.localKills) != 1 || len(rec.remoteKills) != 0 {
		t.Fatalf("hostname-labelled row: local=%v remote=%v, want local only", rec.localKills, rec.remoteKills)
	}
}

// TestRenameSession_RoutesByHost — `R` on a remote row renames through
// that host's daemon; the local tmux rename seam is never called.
func TestRenameSession_RoutesByHost(t *testing.T) {
	cases := []struct {
		name       string
		host, sess string
		wantLocal  []string
		wantRemote []string
	}{
		{name: "local row", host: "local", sess: "c-ccmux", wantLocal: []string{"c-ccmux→c-renamed"}},
		{name: "remote row with same name", host: "mac-mini", sess: "c-ccmux",
			wantRemote: []string{"100.64.0.2:7474|mac-mini|c-ccmux→c-renamed"}},
		{name: "unknown host is refused", host: "retired-box", sess: "c-ghost"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := recordSessionRouting(t)
			a := selectSession(t, twoHostSessionsApp(t), tc.host, tc.sess)

			// Drive the real form: R opens it, Enter submits.
			a, _ = sendKey(t, a, keyRunes("R"))
			if a.sessionsM.renameForm == nil {
				t.Fatal("R did not open the rename form")
			}
			a.sessionsM.renameForm.input.SetValue("c-renamed")
			a, cmd := sendKey(t, a, tea.KeyMsg{Type: tea.KeyEnter})
			for _, m := range drainCmd(cmd) {
				var next tea.Cmd
				a, next = updateApp(t, a, m)
				drainCmd(next)
			}

			if strings.Join(rec.localRenames, ",") != strings.Join(tc.wantLocal, ",") {
				t.Errorf("local renames = %v, want %v", rec.localRenames, tc.wantLocal)
			}
			if strings.Join(rec.remoteRenames, ",") != strings.Join(tc.wantRemote, ",") {
				t.Errorf("remote renames = %v, want %v", rec.remoteRenames, tc.wantRemote)
			}
		})
	}
}

// TestKillRemoteSessionCmd_HitsRemoteDaemon exercises the production
// remote seam against a fake ccmuxd: the kill lands on
// POST /v1/sessions/<name>/kill of THAT host, and the result names it.
func TestKillRemoteSessionCmd_HitsRemoteDaemon(t *testing.T) {
	var mu sync.Mutex
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/rename") {
			var req daemon.RenameRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			got = append(got, "name="+req.Name)
			_ = json.NewEncoder(w).Encode(daemon.SessionState{Name: req.Name})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	msg := killRemoteSessionCmd(addr, "mac-mini", "c-ccmux")()
	killed, ok := msg.(sessionKilledMsg)
	if !ok || killed.Err != nil || killed.Host != "mac-mini" || killed.Name != "c-ccmux" {
		t.Fatalf("kill msg = %#v", msg)
	}
	msg = renameRemoteSessionCmd(addr, "mac-mini", "c-ccmux", "c-new")()
	renamed, ok := msg.(sessionRenamedMsg)
	if !ok || renamed.Err != nil || renamed.Host != "mac-mini" {
		t.Fatalf("rename msg = %#v", msg)
	}
	want := "POST /v1/sessions/c-ccmux/kill,POST /v1/sessions/c-ccmux/rename,name=c-new"
	if strings.Join(got, ",") != want {
		t.Errorf("daemon saw %v, want %s", got, want)
	}
}
