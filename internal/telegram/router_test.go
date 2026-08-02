package telegram

import (
	"context"
	"errors"
	"testing"

	"github.com/skzv/ccmux/internal/daemon"
)

func TestParseTarget(t *testing.T) {
	cases := []struct {
		in       string
		wantHost string
		wantSess string
	}{
		{"build", LocalHost, "build"},
		{"mini:build", "mini", "build"},
		{"local:api", "local", "api"},
		{"  mini : api ", "mini", "api"},
		{":api", LocalHost, "api"}, // empty host → local
	}
	for _, c := range cases {
		got := ParseTarget(c.in)
		if got.Host != c.wantHost || got.Session != c.wantSess {
			t.Errorf("ParseTarget(%q) = %+v, want {%s %s}", c.in, got, c.wantHost, c.wantSess)
		}
	}
	// Round-trips through String for a host:session form.
	if got := ParseTarget("mini:build").String(); got != "mini:build" {
		t.Errorf("round-trip = %q", got)
	}
}

func TestRouter_ClientResolution(t *testing.T) {
	local := &fakeDaemon{}
	mini := &fakeDaemon{}
	r := NewRouter(local, map[string]DaemonClient{"mini": mini})

	if c, ok := r.Client(""); !ok || c != local {
		t.Errorf("bare host should resolve to local")
	}
	if c, ok := r.Client("local"); !ok || c != local {
		t.Errorf("local should resolve to local")
	}
	if c, ok := r.Client("mini"); !ok || c != mini {
		t.Errorf("mini should resolve to the peer")
	}
	if _, ok := r.Client("ghost"); ok {
		t.Errorf("unknown host should not resolve")
	}
}

func TestRouter_AllSessions_FanOutTagsHosts(t *testing.T) {
	local := &fakeDaemon{sessions: []daemon.SessionState{{Name: "build", Host: "local"}}}
	mini := &fakeDaemon{sessions: []daemon.SessionState{{Name: "api", Host: "local"}}}
	r := NewRouter(local, map[string]DaemonClient{"mini": mini})

	got := r.AllSessions(context.Background())
	if len(got) != 2 {
		t.Fatalf("got %d sessions, want 2", len(got))
	}
	// Local first, then peer; peer session is re-tagged with its host.
	if got[0].Name != "build" || got[0].Host != "local" {
		t.Errorf("session 0 = %+v", got[0])
	}
	if got[1].Name != "api" || got[1].Host != "mini" {
		t.Errorf("peer session should be re-tagged mini, got %+v", got[1])
	}
}

func TestRouter_AllSessions_DeadPeerOmitted(t *testing.T) {
	local := &fakeDaemon{sessions: []daemon.SessionState{{Name: "build"}}}
	dead := &fakeDaemon{sessionsErr: errors.New("connection refused")}
	r := NewRouter(local, map[string]DaemonClient{"mini": dead})

	got := r.AllSessions(context.Background())
	if len(got) != 1 || got[0].Name != "build" {
		t.Errorf("dead peer should be omitted, local kept; got %+v", got)
	}
}

func TestRouter_Hosts(t *testing.T) {
	r := NewRouter(&fakeDaemon{}, map[string]DaemonClient{"zeta": &fakeDaemon{}, "alpha": &fakeDaemon{}})
	hosts := r.Hosts()
	// local first, peers sorted.
	if len(hosts) != 3 || hosts[0] != "local" || hosts[1] != "alpha" || hosts[2] != "zeta" {
		t.Errorf("Hosts() = %v, want [local alpha zeta]", hosts)
	}
}
