package main

import (
	"testing"

	"github.com/skzv/ccmux/internal/tailnet"
)

// TestPeerInfos_CarriesReachablePeerOS — /v1/peers reported every peer
// running ccmuxd as macOS, so the iOS "add host" picker labelled Linux
// boxes as Macs.
func TestPeerInfos_CarriesReachablePeerOS(t *testing.T) {
	got := peerInfos(tailnet.Scan{
		Reachable:    []tailnet.Discovered{{Name: "box", Address: "100.64.0.2:7474", OS: "linux"}},
		NeedsInstall: []tailnet.Peer{{HostName: "win", Addr: "100.64.0.3", OS: "windows", Online: true}},
	}, 0)
	if len(got) != 2 {
		t.Fatalf("peers = %+v, want 2", got)
	}
	if got[0].OS != "linux" || got[0].Addr != "100.64.0.2" || !got[0].RunsCCMuxd || got[0].Port == nil || *got[0].Port != 7474 {
		t.Errorf("reachable peer = %+v, want linux at 100.64.0.2 on the default port", got[0])
	}
	if got[1].OS != "windows" || got[1].RunsCCMuxd {
		t.Errorf("needs-install peer = %+v", got[1])
	}
}
