package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/tailnet"
)

// handlePeers returns every tailnet peer plus an indication of which
// ones already run ccmuxd. Used by clients (iOS Settings → Add host)
// to populate a "your other Macs on the tailnet" picker without each
// needing tailscale-CLI access themselves. Probe budget is intentional:
// ScanTailnet pings each peer's /v1/health in parallel with a 1s
// deadline, so even a tailnet of dozens settles in ~1s.
func (s *server) handlePeers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	scan, err := tailnet.ScanTailnet(ctx, s.cfg.Daemon.TailnetPort)
	if err != nil {
		// Tailscale not installed / not running is a normal state on
		// some hosts — return an empty list rather than 500 so clients
		// can show "no peers found" instead of an error toast.
		log.Printf("ccmuxd: scan tailnet: %v", err)
		writeJSON(w, []daemon.PeerInfo{})
		return
	}
	writeJSON(w, peerInfos(scan, s.cfg.Daemon.TailnetPort))
}

// peerInfos maps a tailnet scan to the /v1/peers wire shape.
func peerInfos(scan tailnet.Scan, port int) []daemon.PeerInfo {
	out := make([]daemon.PeerInfo, 0, len(scan.Reachable)+len(scan.NeedsInstall)+len(scan.Mobile))
	if port == 0 {
		port = 7474
	}
	for _, d := range scan.Reachable {
		// Address is "ip:port" or "[ipv6]:port" — split safely.
		ip, _, err := net.SplitHostPort(d.Address)
		if err != nil {
			ip = d.Address
		}
		p := port
		out = append(out, daemon.PeerInfo{
			Hostname: d.Name, Addr: ip, OS: d.OS,
			Online: true, RunsCCMuxd: true, Port: &p,
		})
	}
	for _, peer := range scan.NeedsInstall {
		out = append(out, daemon.PeerInfo{
			Hostname: peer.DisplayName(), Addr: peer.Addr, OS: peer.OS,
			Online: peer.Online, RunsCCMuxd: false,
		})
	}
	for _, peer := range scan.Mobile {
		out = append(out, daemon.PeerInfo{
			Hostname: peer.DisplayName(), Addr: peer.Addr, OS: peer.OS,
			Online: peer.Online, RunsCCMuxd: false,
		})
	}
	return out
}
