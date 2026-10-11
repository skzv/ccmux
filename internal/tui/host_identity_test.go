package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/tailnet"
)

// Drive discovery through the actual merge, then the Sessions keybindings.
// No session mutation or SSH command reaches a live machine.
func TestDiscoveredHostIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, peerName string
		configured     bool
	}{
		{"duplicate discovered names", "mini", false},
		{"reserved local name", "local", false},
		{"configured name collision", "mini", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first := startFakeCcmuxd(t, false,
				[]daemon.SessionState{{Name: "c-api", Path: "/first/api"}},
				[]daemon.ProjectInfo{{Name: "api", Path: "/first/api"}})
			second := startFakeCcmuxd(t, false,
				[]daemon.SessionState{{Name: "c-api", Path: "/second/api"}},
				[]daemon.ProjectInfo{{Name: "api", Path: "/second/api"}})
			peers := []tailnet.Discovered{
				{Name: tc.peerName, Address: first.addr(), DialHost: first.host},
				{Name: tc.peerName, Address: second.addr(), DialHost: second.host},
			}
			var configured []config.Host
			if tc.configured {
				configured = []config.Host{{Name: tc.peerName, Address: first.host, Port: first.port}}
			}
			stubRefreshSeams(t, func(context.Context, int) (tailnet.Scan, error) {
				return tailnet.Scan{Reachable: peers}, nil
			})
			got := collectSessions(configured, 7474)
			want := map[string]string{"/first/api": first.addr(), "/second/api": second.addr()}
			a := newSessionsApp(t)
			a.cfg.Hosts = configured
			m, _ := a.Update(got)
			a = m.(App)
			rec := recordSessionRouting(t)
			count := 0
			for i, session := range a.sessionsM.sessions {
				addr, remote := want[session.Path]
				if !remote {
					continue
				}
				count++
				key := sessionHostLabel(session)
				if resolved, ok := a.remoteDaemonAddr(key); !ok || resolved != addr {
					t.Fatalf("%s routes to %s, want %s", session.Path, resolved, addr)
				}
				a.sessionsM.cursor = i
				kill, _ := sendKey(t, a, keyRunes("x"))
				if !strings.Contains(kill.confirm.body(session.Name), addr) {
					t.Errorf("ambiguous confirmation: %s", kill.confirm.body(session.Name))
				}
				_, cmd := sendKey(t, kill, keyRunes("y"))
				drainCmd(cmd)
				if len(rec.remoteKills) != count {
					t.Fatalf("kill did not reach remote: local=%v remote=%v", rec.localKills, rec.remoteKills)
				}
				if got := rec.remoteKills[len(rec.remoteKills)-1]; got != addr+"|"+key+"|c-api" {
					t.Errorf("kill = %s", got)
				}
				rename, _ := sendKey(t, a, keyRunes("R"))
				if rename.sessionsM.renameForm.host != key {
					t.Fatal("rename form lost host identity")
				}
				_, cmd = rename.Update(renameSessionSubmitMsg{Host: rename.sessionsM.renameForm.host, OldName: "c-api", NewName: "c-renamed"})
				cmd()
				if got := rec.remoteRenames[len(rec.remoteRenames)-1]; got != addr+"|"+key+"|c-api→c-renamed" {
					t.Errorf("rename = %s", got)
				}
				attached, _ := sendKey(t, a, tea.KeyMsg{Type: tea.KeyEnter})
				if attached.attach.kind != attachKindRemote {
					t.Fatal("peer attached locally")
				}
			}
			if count != 2 || len(rec.localKills) != 0 || len(rec.localRenames) != 0 {
				t.Fatalf("remote rows = %d; local mutations = %v %v", count, rec.localKills, rec.localRenames)
			}
			projects := collectProjects(t.TempDir(), configured, 7474).Projects
			if len(projects) != 2 {
				t.Fatalf("projects = %v", projects)
			}
			notes := notesModel{hosts: got.Hosts}
			for _, p := range projects {
				key := projectHost(p)
				if addr, ok := a.remoteDaemonAddr(key); !ok || addr != want[p.Path] {
					t.Errorf("project %s routes to %s", p.Path, addr)
				}
				if addr, ok := notes.remoteAddr(key); !ok || addr != want[p.Path] {
					t.Errorf("notes for %s route to %s", p.Path, addr)
				}
			}
			for _, choice := range hostChoicesFrom(got.Hosts) {
				if !choice.Local && (choice.Label == "local" || !strings.Contains(choice.Label, choice.Address)) {
					t.Errorf("ambiguous device picker: %+v", choice)
				}
			}
		})
	}
}

func TestDiscoveredHostIdentity_SurvivesPeerRenameAndReorder(t *testing.T) {
	first := daemon.SessionState{Name: "c-api", Host: "mini", HostKey: discoveredHostKey("100.64.0.1:7474")}
	second := first
	second.HostKey = discoveredHostKey("100.64.0.2:7474")
	a := sendSessions(t, newSessionsApp(t), []daemon.SessionState{first, second})
	a.sessionsM.cursor = 1
	second.Host = "renamed-mini"
	a = sendSessions(t, a, []daemon.SessionState{second, first})
	if got := a.sessionsM.Selected(); got == nil || got.HostKey != second.HostKey {
		t.Fatalf("selection moved to another peer: %+v", got)
	}
	a.sessionsM.showPreview = true
	a.sessionsM.preview = "selected peer"
	updated, _ := a.sessionsM.Update(previewLoadedMsg{Session: "c-api", Host: first.HostKey, Content: "wrong peer"})
	if updated.preview != "selected peer" {
		t.Fatal("accepted another peer's preview")
	}
}
