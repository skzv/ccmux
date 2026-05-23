package tailnet

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/daemon"
)

func TestPeer_IsMobile(t *testing.T) {
	cases := []struct {
		os   string
		want bool
	}{
		{"iOS", true},
		{"ios", true},
		{"iPadOS", true},
		{"Android", true},
		{"macOS", false},
		{"Linux", false},
		{"Windows", false},
		{"FreeBSD", false},
		{"", false},
	}
	for _, tc := range cases {
		got := Peer{OS: tc.os}.IsMobile()
		if got != tc.want {
			t.Errorf("IsMobile(%q) = %v, want %v", tc.os, got, tc.want)
		}
	}
}

func TestParsePeers_ExtractsOS(t *testing.T) {
	raw := []byte(`{
  "BackendState": "Running",
  "Self": {"HostName":"x","OS":"macOS","TailscaleIPs":["1.1.1.1"],"Online":true},
  "Peer": {
    "a": {"HostName":"phone","OS":"iOS","TailscaleIPs":["2.2.2.2"],"Online":true},
    "b": {"HostName":"laptop","OS":"Linux","TailscaleIPs":["3.3.3.3"],"Online":true}
  }
}`)
	peers, err := parsePeers(raw)
	if err != nil {
		t.Fatal(err)
	}
	osBy := map[string]string{}
	for _, p := range peers {
		osBy[p.Addr] = p.OS
	}
	if osBy["1.1.1.1"] != "macOS" || osBy["2.2.2.2"] != "iOS" || osBy["3.3.3.3"] != "Linux" {
		t.Fatalf("OS not parsed: %v", osBy)
	}
}

func TestShortName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Sasha's Mac mini", "sashas-mac-mini"},
		{"  mac-mini  ", "mac-mini"},
		{"Server01", "server01"},
		{"a__b--c  d", "a-b-c-d"},
		{"---", ""},
		{"", ""},
		{"héllo", "hllo"}, // non-ASCII chars are dropped (not preserved, not converted)
	}
	for _, tc := range cases {
		if got := shortName(tc.in); got != tc.want {
			t.Errorf("shortName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParsePeers_HappyPath(t *testing.T) {
	raw := []byte(`{
  "BackendState": "Running",
  "Self": {
    "HostName": "Sasha's Mac mini",
    "DNSName": "sashas-mac-mini.tail-abcd.ts.net.",
    "TailscaleIPs": ["100.75.64.20", "fd7a::1"],
    "Online": true
  },
  "Peer": {
    "n1": {"HostName": "sputnik", "DNSName": "sputnik.tail-abcd.ts.net.", "TailscaleIPs": ["100.112.85.37"], "Online": false},
    "n2": {"HostName": "laptop", "DNSName": "laptop.tail-abcd.ts.net.", "TailscaleIPs": ["100.87.28.92"], "Online": true},
    "n3": {"HostName": "no-ip", "DNSName": "no-ip.ts.net.", "TailscaleIPs": [], "Online": true}
  }
}`)
	got, err := parsePeers(raw)
	if err != nil {
		t.Fatal(err)
	}
	// Self + 2 with IPs (n3 skipped). n1/n2 order isn't guaranteed in Go
	// map iteration; sort-by-Addr for deterministic check.
	var addrs []string
	for _, p := range got {
		addrs = append(addrs, p.Addr)
	}
	if len(addrs) != 3 {
		t.Fatalf("got %d peers (%v), want 3 (Self + 2 with IPs, skip no-ip)", len(addrs), addrs)
	}

	// Self should be flagged.
	foundSelf := false
	for _, p := range got {
		if p.Self {
			foundSelf = true
			if p.Addr != "100.75.64.20" {
				t.Errorf("Self.Addr = %q, want 100.75.64.20", p.Addr)
			}
		}
	}
	if !foundSelf {
		t.Error("Self peer missing")
	}
}

func TestParsePeers_TailscaleNotRunning(t *testing.T) {
	raw := []byte(`{"BackendState":"NeedsLogin","Self":{}}`)
	if _, err := parsePeers(raw); err == nil {
		t.Fatal("expected error when BackendState != Running")
	}
}

func TestParsePeers_EmptyDoc(t *testing.T) {
	// No BackendState field — accept (some tailscale versions omit it).
	raw := []byte(`{"Self":{},"Peer":{}}`)
	got, err := parsePeers(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty peers, got %v", got)
	}
}

func TestParsePeers_MalformedJSON(t *testing.T) {
	if _, err := parsePeers([]byte("not json")); err == nil {
		t.Fatal("expected parse error")
	}
}

// TestDiscover_FiltersAndProbes covers the three peer filters: Self
// peers are skipped, offline peers are skipped, and online non-Self
// peers get probed and surfaced. The slice-input discoverFromPeers
// helper bypasses tailscale.Peers() (which would shell out).
func TestDiscover_FiltersAndProbes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/health") {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(daemon.HealthInfo{OK: true, Hostname: "fake", Version: "v0", Sessions: 2})
	}))
	defer srv.Close()
	host, portStr, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portStr)

	peers := []Peer{
		{HostName: "Self Box", Addr: "1.2.3.4", Online: true, Self: true}, // skipped (Self)
		{HostName: "Sleeping", Addr: "5.6.7.8", Online: false},            // skipped (offline)
		{HostName: "ccmuxd peer", Addr: host, Online: true},               // probed → found
	}
	got := discoverFromPeers(context.Background(), peers, port)
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 discovered host (only the online non-Self peer), got %d: %v",
			len(got), got)
	}
	if got[0].Name != "ccmuxd-peer" {
		t.Errorf("discovered name = %q, want ccmuxd-peer", got[0].Name)
	}
}

// TestDiscover_ProbeFailureIsSilent covers the other half of discover's
// contract: an online peer with no ccmuxd at the configured port is
// silently dropped, not surfaced as an error or a half-populated row.
// Start an httptest server, capture its port, shut it down, then use
// that now-dead port for the probe — connections refuse and the peer
// should fall out.
func TestDiscover_ProbeFailureIsSilent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(daemon.HealthInfo{OK: true})
	}))
	host, portStr, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portStr)
	srv.Close() // tear the listener down — host:port now refuses connections.

	peers := []Peer{
		{HostName: "dead peer", Addr: host, Online: true},
	}
	got := discoverFromPeers(context.Background(), peers, port)
	if len(got) != 0 {
		t.Errorf("expected 0 discovered hosts (probe must fail silently), got %d: %v",
			len(got), got)
	}
}

// discoverFromPeers is the test-friendly slice-input version of
// Discover. The real Discover calls tailscale, which we can't sandbox.
// Pulling the loop out lets us hit it with fixtures.
func discoverFromPeers(ctx context.Context, peers []Peer, port int) []Discovered {
	var out []Discovered
	for _, p := range peers {
		if p.Self || !p.Online || p.Addr == "" {
			continue
		}
		addr := p.Addr + ":" + strconv.Itoa(port)
		info, err := probeOne(ctx, addr)
		if err != nil {
			continue
		}
		out = append(out, Discovered{
			Name:     shortName(p.HostName),
			Address:  addr,
			Version:  info.Version,
			Sessions: info.Sessions,
		})
	}
	return out
}
