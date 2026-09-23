package tui

import (
	"context"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tailnet"
)

// Dashboard refresh fan-out.
//
// A refresh asks this machine, every configured host, and every tailnet
// peer what's running. It used to walk them one after another under a
// single 5s context: one hung host (a laptop asleep on the tailnet, a
// daemon wedged mid-request) burned the whole budget, so every host
// after it came back "unreachable" and the tailnet scan ran with an
// already-expired context. Now every probe runs concurrently under its
// OWN budget and the results are assembled in a deterministic order —
// this machine, then configured hosts in config order, then discovered
// peers sorted by name — so a slow host only ever costs its own row.

// Per-probe budgets. Vars rather than consts so tests can shrink them.
var (
	// hostProbeTimeout bounds one host's probe: the local daemon (and
	// separately, its direct-tmux fallback) or one remote daemon's
	// sessions/projects + health calls.
	hostProbeTimeout = 3 * time.Second
	// tailnetScanTimeout bounds `tailscale status` plus the parallel
	// per-peer health probes inside tailnet.ScanTailnet.
	tailnetScanTimeout = 4 * time.Second
)

// sessionsTickStaleAfter is how long the 2s tick waits on its previous
// refresh before issuing another anyway. Well past the worst case of a
// refresh (scan budget + one host budget), so it only fires if a result
// was lost.
const sessionsTickStaleAfter = 15 * time.Second

// Seams for tests: the local-machine probe (daemon socket + tmux
// fallback) and the tailnet scan both reach real system state.
var (
	probeLocalSessions = realProbeLocalSessions
	scanTailnet        = tailnet.ScanTailnet
)

// localSessionsProbe is this machine's contribution to a refresh.
// host is nil when there is nothing to show (no HOME to find the
// daemon socket in); err is set when neither the daemon nor tmux
// could list sessions.
type localSessionsProbe struct {
	sessions []daemon.SessionState
	host     *hostStatus
	err      error
}

// realProbeLocalSessions asks the local ccmuxd for its sessions, and
// falls back to driving tmux directly when the daemon is down.
func realProbeLocalSessions() localSessionsProbe {
	local, lerr := daemon.LocalClient()
	if lerr != nil {
		return localSessionsProbe{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), hostProbeTimeout)
	defer cancel()
	ss, e := local.Sessions(ctx)
	if e == nil {
		for i := range ss {
			ss[i].Host = "local"
		}
		h, _ := local.Health(ctx)
		localName := shortHostname(h.Hostname)
		if localName == "" {
			localName = "local"
		}
		return localSessionsProbe{sessions: ss, host: &hostStatus{
			Name:    localName,
			Local:   true,
			Source:  "local",
			Address: local.Addr(),
			OK:      h.OK,
			// The daemon answered /v1/health, so the chip can
			// honestly say so.
			DaemonOK:  h.OK,
			Sessions:  h.Sessions,
			SleepMode: h.SleepMode,
			Version:   h.Version,
			LastProbe: time.Now(),
		}}
	}
	// Fresh budget for the fallback: a daemon that hung until the
	// deadline must not hand tmux an already-expired context.
	fctx, fcancel := context.WithTimeout(context.Background(), hostProbeTimeout)
	defer fcancel()
	direct, e2 := fallbackDirectTmux(fctx)
	if e2 != nil {
		return localSessionsProbe{err: fmt.Errorf("local: %w", e2)}
	}
	localHost, _ := os.Hostname()
	name := shortHostname(localHost)
	if name == "" {
		name = "local"
	}
	// tmux is responding — sessions came back. ccmuxd is down, but the
	// device itself is fine; mark OK so the Devices dot stays green.
	//
	// DaemonOK stays false: the status-bar chip must say "offline"
	// here. Without the daemon there are no bells, no push
	// notifications, and no sleep lock — the user needs to see that,
	// not a green check. `ccmux daemon install` fixes it.
	return localSessionsProbe{sessions: direct, host: &hostStatus{
		Name:      name,
		Local:     true,
		Source:    "local",
		Address:   "tmux (no daemon)",
		OK:        true,
		DaemonOK:  false,
		LastProbe: time.Now(),
	}}
}

// hostDaemonAddr is the "host:port" of a configured host's ccmuxd.
func hostDaemonAddr(h config.Host, tailnetPort int) string {
	port := h.Port
	if port == 0 {
		port = tailnetPort
	}
	return fmt.Sprintf("%s:%d", h.Address, port)
}

// configuredSeenKeys resolves every configured host's dedupe keys (see
// configuredHostKeys) concurrently — each lookup can take up to its own
// 500ms DNS budget, and doing them in series delayed the whole refresh.
func configuredSeenKeys(hosts []config.Host, tailnetPort int) map[string]bool {
	keys := make([][]string, len(hosts))
	var wg sync.WaitGroup
	for i, h := range hosts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			keys[i] = configuredHostKeys(h, tailnetPort)
		}()
	}
	wg.Wait()
	seen := map[string]bool{}
	for _, ks := range keys {
		for _, k := range ks {
			seen[k] = true
		}
	}
	return seen
}

// probeConfiguredHost lists one configured host's sessions under its
// own budget.
func probeConfiguredHost(h config.Host, tailnetPort int) (hostStatus, []daemon.SessionState) {
	addr := hostDaemonAddr(h, tailnetPort)
	st := hostStatus{
		Name:      h.Name,
		Source:    "configured",
		Address:   addr,
		DialHost:  h.Address, // bare address without port, for ssh/mosh
		User:      h.User,
		Mosh:      h.Mosh,
		SSHPort:   h.SSHPort, // 0 → default 22 at the dial site
		LastProbe: time.Now(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), hostProbeTimeout)
	defer cancel()
	cli := daemon.RemoteClient(addr)
	ss, e := cli.Sessions(ctx)
	if e != nil {
		st.Err = e
		return st, nil
	}
	// A configured host is reached THROUGH its ccmuxd, so a successful
	// session list means its daemon answered.
	st.OK = true
	st.DaemonOK = true
	st.Sessions = len(ss)
	for i := range ss {
		ss[i].Host = h.Name
	}
	if hi, hErr := cli.Health(ctx); hErr == nil {
		st.Version = hi.Version
	}
	return st, ss
}

// scanTailnetSorted runs the tailnet scan under its own budget and
// returns its lists in a stable order (ScanTailnet appends reachable
// peers in probe-completion order, which reshuffled the Devices panel
// between refreshes). ok is false when the scan failed.
func scanTailnetSorted(tailnetPort int) (tailnet.Scan, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), tailnetScanTimeout)
	defer cancel()
	scan, err := scanTailnet(ctx, tailnetPort)
	if err != nil {
		return tailnet.Scan{}, false
	}
	sort.SliceStable(scan.Reachable, func(i, j int) bool {
		if scan.Reachable[i].Name != scan.Reachable[j].Name {
			return scan.Reachable[i].Name < scan.Reachable[j].Name
		}
		return scan.Reachable[i].Address < scan.Reachable[j].Address
	})
	byPeer := func(ps []tailnet.Peer) func(i, j int) bool {
		return func(i, j int) bool {
			ni, nj := shortPeerName(ps[i].DisplayName()), shortPeerName(ps[j].DisplayName())
			if ni != nj {
				return ni < nj
			}
			return ps[i].Addr < ps[j].Addr
		}
	}
	sort.SliceStable(scan.NeedsInstall, byPeer(scan.NeedsInstall))
	sort.SliceStable(scan.Mobile, byPeer(scan.Mobile))
	return scan, true
}

// probeDiscoveredHosts turns a tailnet scan into Devices rows and
// sessions, skipping peers already covered by a configured host
// (`seen`, which this function takes ownership of). Each reachable
// peer's session list is fetched concurrently under its own budget.
func probeDiscoveredHosts(tailnetPort int, seen map[string]bool) ([]hostStatus, []daemon.SessionState) {
	// Tailnet auto-discovery. ScanTailnet probes every online
	// non-mobile peer for ccmuxd /v1/health and partitions:
	//   - Reachable: ccmuxd answered → merge as a regular host.
	//   - NeedsInstall: peer is up but didn't answer → surface
	//     with a "ccmux not installed / running here" hint so
	//     the user knows what to do.
	// Mobile peers (iOS, iPadOS, Android) are skipped entirely
	// because the Moshi app handles them, and installing ccmux
	// there isn't an option.
	// Errors are non-fatal — discovery is convenience.
	scan, ok := scanTailnetSorted(tailnetPort)
	if !ok {
		return nil, nil
	}
	var reach []tailnet.Discovered
	for _, d := range scan.Reachable {
		if seen[d.Address] {
			continue
		}
		seen[d.Address] = true
		reach = append(reach, d)
	}
	rows := make([]hostStatus, len(reach))
	per := make([][]daemon.SessionState, len(reach))
	var wg sync.WaitGroup
	for i, d := range reach {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// The probe already succeeded (that's how this peer ended
			// up in Reachable). Mark OK regardless of the follow-up
			// Sessions call — a Sessions error means "couldn't list
			// sessions right now," not "host is down," so we shouldn't
			// make the dot red.
			st := hostStatus{
				Name: d.Name, Address: d.Address,
				Source:     "discovered",
				Discovered: true, DialHost: d.DialHost,
				// Discovered via a successful ccmuxd health probe, so
				// its daemon is by definition answering.
				Version: d.Version, OK: true, DaemonOK: true,
				TailscaleSSH: d.TailscaleSSH,
				LastProbe:    time.Now(),
			}
			ctx, cancel := context.WithTimeout(context.Background(), hostProbeTimeout)
			defer cancel()
			if ss, e := daemon.RemoteClient(d.Address).Sessions(ctx); e == nil {
				st.Sessions = len(ss)
				for j := range ss {
					ss[j].Host = d.Name
				}
				per[i] = ss
			} else {
				st.Err = e
			}
			rows[i] = st
		}()
	}
	wg.Wait()

	hs := rows
	var sessions []daemon.SessionState
	for _, ss := range per {
		sessions = append(sessions, ss...)
	}
	for _, p := range scan.NeedsInstall {
		addr := fmt.Sprintf("%s:%d", p.Addr, tailnetPort)
		if seen[addr] {
			continue
		}
		seen[addr] = true
		hs = append(hs, hostStatus{
			Name:         shortPeerName(p.DisplayName()),
			Source:       "discovered",
			Address:      addr,
			Discovered:   true,
			NeedsInstall: true,
			OS:           p.OS,
			OK:           p.Online,
			LastProbe:    time.Now(),
		})
	}
	for _, p := range scan.Mobile {
		// Mobile rows don't have an ccmuxd address; key the dedupe by
		// the tailnet IP itself so the same phone doesn't show twice
		// across refreshes.
		key := "mobile://" + p.Addr
		if seen[key] {
			continue
		}
		seen[key] = true
		hs = append(hs, hostStatus{
			Name:       shortPeerName(p.DisplayName()),
			Source:     "mobile",
			Address:    p.Addr,
			Discovered: true,
			Mobile:     true,
			OS:         p.OS,
			OK:         p.Online,
			LastProbe:  time.Now(),
		})
	}
	return hs, sessions
}

// collectSessions is the body of refreshSessionsCmd: every probe in
// parallel, each under its own budget, assembled deterministically.
func collectSessions(hosts []config.Host, tailnetPort int) sessionsLoadedMsg {
	var wg sync.WaitGroup

	var local localSessionsProbe
	wg.Add(1)
	go func() {
		defer wg.Done()
		local = probeLocalSessions()
	}()

	cfgRows := make([]hostStatus, len(hosts))
	cfgSessions := make([][]daemon.SessionState, len(hosts))
	for i, h := range hosts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cfgRows[i], cfgSessions[i] = probeConfiguredHost(h, tailnetPort)
		}()
	}

	// Configured hosts are tracked so a peer that's both configured AND
	// auto-discovered isn't listed twice. configuredHostKeys resolves
	// DNS names to IPs so a host configured as "mac-mini:7474" still
	// dedupes against the scan's "100.x.x.x:7474" entry.
	seen := configuredSeenKeys(hosts, tailnetPort)
	var discRows []hostStatus
	var discSessions []daemon.SessionState
	wg.Add(1)
	go func() {
		defer wg.Done()
		discRows, discSessions = probeDiscoveredHosts(tailnetPort, seen)
	}()
	wg.Wait()

	var (
		sessions []daemon.SessionState
		hs       []hostStatus
	)
	sessions = append(sessions, local.sessions...)
	if local.host != nil {
		hs = append(hs, *local.host)
	}
	for i := range hosts {
		hs = append(hs, cfgRows[i])
		sessions = append(sessions, cfgSessions[i]...)
	}
	hs = append(hs, discRows...)
	sessions = append(sessions, discSessions...)

	sort.SliceStable(sessions, func(i, j int) bool {
		pi := statePriority(sessions[i].State)
		pj := statePriority(sessions[j].State)
		if pi != pj {
			return pi < pj
		}
		if sessions[i].Host != sessions[j].Host {
			return sessions[i].Host < sessions[j].Host
		}
		return sessions[i].Name < sessions[j].Name
	})
	return sessionsLoadedMsg{Sessions: sessions, Hosts: hs, Err: local.err, At: time.Now()}
}

// fetchRemoteProjects fetches projects from one remote ccmuxd at `addr`
// under its own budget and tags each entry with `hostLabel` (the
// dashboard's friendly name for that host). Failures are silently
// swallowed — project discovery is best-effort, and a single
// unreachable peer shouldn't drop the user's local list.
func fetchRemoteProjects(addr, hostLabel string) []project.Project {
	ctx, cancel := context.WithTimeout(context.Background(), hostProbeTimeout)
	defer cancel()
	infos, err := daemon.RemoteClient(addr).Projects(ctx)
	if err != nil {
		return nil
	}
	out := make([]project.Project, 0, len(infos))
	for _, p := range infos {
		out = append(out, project.Project{
			Name: p.Name, Host: hostLabel, Path: p.Path,
			HasGit: p.HasGit, HasCM: p.HasCM, HasAgents: p.HasAgents, HasDocs: p.HasDocs,
			Modified: p.Modified,
		})
	}
	return out
}

// collectProjects is the body of refreshProjectsCmd: local discovery,
// every configured host, and every discovered peer in parallel, each
// remote under its own budget.
func collectProjects(root string, hosts []config.Host, tailnetPort int) projectsLoadedMsg {
	var wg sync.WaitGroup

	cfgProjects := make([][]project.Project, len(hosts))
	for i, h := range hosts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cfgProjects[i] = fetchRemoteProjects(hostDaemonAddr(h, tailnetPort), h.Name)
		}()
	}

	// seen is keyed by every form of each configured host's address we
	// can resolve (literal + each tailnet IP) so the auto-discovery scan
	// doesn't re-fetch a peer that's already configured under a DNS name.
	seen := configuredSeenKeys(hosts, tailnetPort)
	var discProjects [][]project.Project
	wg.Add(1)
	go func() {
		defer wg.Done()
		scan, ok := scanTailnetSorted(tailnetPort)
		if !ok {
			return
		}
		var reach []tailnet.Discovered
		for _, d := range scan.Reachable {
			if seen[d.Address] {
				continue
			}
			seen[d.Address] = true
			reach = append(reach, d)
		}
		discProjects = make([][]project.Project, len(reach))
		var inner sync.WaitGroup
		for i, d := range reach {
			inner.Add(1)
			go func() {
				defer inner.Done()
				discProjects[i] = fetchRemoteProjects(d.Address, d.Name)
			}()
		}
		inner.Wait()
	}()

	var all []project.Project
	// Local projects first so the merge sort keeps them grouped
	// naturally by Modified after combining.
	if ps, err := project.Discover(root); err == nil {
		for _, p := range ps {
			p.Host = "local"
			all = append(all, p)
		}
	}
	wg.Wait()
	for _, ps := range cfgProjects {
		all = append(all, ps...)
	}
	for _, ps := range discProjects {
		all = append(all, ps...)
	}

	sort.SliceStable(all, func(i, j int) bool {
		hi, hj := projectHost(all[i]), projectHost(all[j])
		if hi != hj {
			if hi == "local" {
				return true
			}
			if hj == "local" {
				return false
			}
			return hi < hj
		}
		return all[i].Modified.After(all[j].Modified)
	})
	return projectsLoadedMsg{Projects: all}
}
