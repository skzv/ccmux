// Package tailnet discovers other Tailscale-reachable ccmuxd instances
// so the dashboard "just sees" every device on your tailnet without the
// user running `ccmux host add` for each one. Detection is best-effort:
// if `tailscale` isn't installed, isn't authed, or returns no peers, we
// surface an empty list and let the caller fall back to configured hosts.
package tailnet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"sync"
	"time"

	"github.com/skzv/ccmux/internal/daemon"
)

// Peer is one host on the user's tailnet (or Self).
type Peer struct {
	// HostName is the human-friendly hostname Tailscale reports (e.g.
	// "Sasha's Mac mini").
	HostName string
	// Addr is the tailnet IPv4 (e.g. "100.75.64.20"). What ccmuxd binds to.
	Addr string
	// DNSName is the full MagicDNS name (e.g. "mac-mini.tail-abcd.ts.net.").
	DNSName string
	// Online is whether Tailscale considers the peer currently up.
	Online bool
	// Self marks this machine. Useful for skipping a self-probe.
	Self bool
}

// Peers returns every peer Tailscale knows about, plus Self. Returns an
// empty slice (not an error) when tailscale isn't installed or hasn't
// been authed — those are normal user states, not failures.
func Peers(ctx context.Context) ([]Peer, error) {
	bin, err := exec.LookPath("tailscale")
	if err != nil {
		return nil, nil
	}
	c, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(c, bin, "status", "--json").Output()
	if err != nil {
		// Common reasons (BackendState=NeedsLogin, tailscaled not running):
		// we'd rather show "no peers" than block the dashboard.
		return nil, nil
	}
	return parsePeers(out)
}

// parsePeers is the JSON-parsing half of Peers. Split out so tests can
// hit it with a fixture instead of shelling to tailscale.
func parsePeers(raw []byte) ([]Peer, error) {
	var doc struct {
		BackendState string `json:"BackendState"`
		Self         struct {
			HostName     string   `json:"HostName"`
			DNSName      string   `json:"DNSName"`
			TailscaleIPs []string `json:"TailscaleIPs"`
			Online       bool     `json:"Online"`
		} `json:"Self"`
		Peer map[string]struct {
			HostName     string   `json:"HostName"`
			DNSName      string   `json:"DNSName"`
			TailscaleIPs []string `json:"TailscaleIPs"`
			Online       bool     `json:"Online"`
		} `json:"Peer"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse tailscale status: %w", err)
	}
	if doc.BackendState != "" && doc.BackendState != "Running" {
		return nil, errors.New("tailscale: " + doc.BackendState)
	}
	var peers []Peer
	if len(doc.Self.TailscaleIPs) > 0 {
		peers = append(peers, Peer{
			HostName: doc.Self.HostName,
			Addr:     doc.Self.TailscaleIPs[0],
			DNSName:  doc.Self.DNSName,
			Online:   true,
			Self:     true,
		})
	}
	for _, p := range doc.Peer {
		if len(p.TailscaleIPs) == 0 {
			continue
		}
		peers = append(peers, Peer{
			HostName: p.HostName,
			Addr:     p.TailscaleIPs[0],
			DNSName:  p.DNSName,
			Online:   p.Online,
		})
	}
	return peers, nil
}

// Discovered is one tailnet peer that responded to a ccmuxd health probe.
type Discovered struct {
	Name    string // pretty short name (peer's HostName)
	Address string // "host:port" suitable for daemon.RemoteClient
	Version string // ccmuxd version reported by /v1/health
	Sessions int
}

// Discover probes every online tailnet peer for a ccmuxd HTTP listener
// at `port` and returns those that respond with a healthy /v1/health.
// Skips Self (which the local Unix-socket path handles).
//
// `port` should match the daemon.tailnet_port setting on remote hosts.
// Default 7474 if 0.
func Discover(ctx context.Context, port int) ([]Discovered, error) {
	if port == 0 {
		port = 7474
	}
	peers, err := Peers(ctx)
	if err != nil {
		return nil, err
	}
	if len(peers) == 0 {
		return nil, nil
	}
	type result struct {
		d   Discovered
		err error
	}
	results := make(chan result, len(peers))
	var wg sync.WaitGroup
	for _, p := range peers {
		if p.Self || !p.Online || p.Addr == "" {
			continue
		}
		wg.Add(1)
		go func(p Peer) {
			defer wg.Done()
			addr := fmt.Sprintf("%s:%d", p.Addr, port)
			info, err := probeOne(ctx, addr)
			if err != nil {
				results <- result{err: err}
				return
			}
			results <- result{d: Discovered{
				Name:     shortName(p.HostName),
				Address:  addr,
				Version:  info.Version,
				Sessions: info.Sessions,
			}}
		}(p)
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	var out []Discovered
	for r := range results {
		if r.err != nil {
			continue
		}
		out = append(out, r.d)
	}
	return out, nil
}

// probeOne is the cheap "is there a ccmuxd here" check. 1-second hard
// timeout so a slow host can't stall the dashboard.
func probeOne(ctx context.Context, addr string) (daemon.HealthInfo, error) {
	c, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()
	cli := daemon.RemoteClient(addr)
	return cli.Health(c)
}

// shortName turns "Sasha's Mac mini" into "mac-mini" — readable, no
// spaces or apostrophes that'd choke a tmux session name.
func shortName(s string) string {
	out := make([]rune, 0, len(s))
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out = append(out, r)
			prevDash = false
		case r >= 'A' && r <= 'Z':
			out = append(out, r+32)
			prevDash = false
		case r == ' ' || r == '-' || r == '_':
			if len(out) > 0 && !prevDash {
				out = append(out, '-')
				prevDash = true
			}
		}
	}
	for len(out) > 0 && out[len(out)-1] == '-' {
		out = out[:len(out)-1]
	}
	return string(out)
}

// HTTPClient is the http.Client used for the health probe. Exposed so a
// test can swap it for an in-process server without touching the real
// network. Unused outside tests today.
var HTTPClient = &http.Client{Timeout: 1 * time.Second}
