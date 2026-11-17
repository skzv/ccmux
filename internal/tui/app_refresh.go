package tui

import (
	"context"
	"fmt"
	"net"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/claude"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/tmux"
)

// refreshSessionsCmd fetches sessions from local ccmuxd, every
// explicitly-configured remote host, AND every tailnet peer auto-
// discovered via `tailscale status` + a /v1/health probe. Falls back
// to direct tmux call when the local daemon is down. Every probe runs
// concurrently under its own budget — see collectSessions (refresh.go).
//
// Each refresh is numbered (Gen). Refreshes overlap — the 2s tick, `r`,
// the post-kill / post-attach refreshes — and a slow host makes them
// finish out of order, so the sessionsLoadedMsg handler drops a result
// older than one it already applied instead of letting a stale list
// overwrite a newer one.
func (a *App) refreshSessionsCmd() tea.Cmd {
	a.sessionsLoadGen++
	return a.sessionsRefreshCmd(a.sessionsLoadGen)
}

// sessionsRefreshCmd is refreshSessionsCmd for an already-assigned
// generation.
func (a App) sessionsRefreshCmd(gen int) tea.Cmd {
	hosts := append([]config.Host(nil), a.cfg.Hosts...)
	tailnetPort := a.cfg.Daemon.TailnetPort
	if tailnetPort == 0 {
		tailnetPort = 7474
	}
	return func() tea.Msg {
		msg := collectSessions(hosts, tailnetPort)
		msg.Gen = gen
		return msg
	}
}

// refreshProjectsCmd discovers local projects plus every configured
// host's and discovered peer's projects, concurrently — see
// collectProjects (refresh.go). Numbered like refreshSessionsCmd.
func (a *App) refreshProjectsCmd() tea.Cmd {
	a.projectsLoadGen++
	return a.projectsRefreshCmd(a.projectsLoadGen)
}

// projectsRefreshCmd is refreshProjectsCmd for an already-assigned
// generation.
func (a App) projectsRefreshCmd(gen int) tea.Cmd {
	root := a.cfg.Projects.Root
	hosts := append([]config.Host(nil), a.cfg.Hosts...)
	tailnetPort := a.cfg.Daemon.TailnetPort
	if tailnetPort == 0 {
		tailnetPort = 7474
	}
	return func() tea.Msg {
		msg := collectProjects(root, hosts, tailnetPort)
		msg.Gen = gen
		return msg
	}
}

// configuredHostKeys returns every "addr:port" string that should be
// treated as already-known when merging tailnet auto-discovery into a
// list of configured hosts. Resolves DNS names to tailnet IPs so a
// host configured as "user:7474" still dedupes against the
// scan's "100.x.x.x:7474" entry for the same machine. Falls back to
// the literal address-only key when resolution fails — better to
// dedupe one form than none.
//
// `defaultPort` is the port to use when h.Port == 0. Callers pass the
// same value they hand to ScanTailnet so both paths agree on what
// "the default daemon port" means in this user's config.
func configuredHostKeys(h config.Host, defaultPort int) []string {
	port := h.Port
	if port == 0 {
		port = defaultPort
	}
	keys := []string{fmt.Sprintf("%s:%d", h.Address, port)}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	var r net.Resolver
	if ips, err := r.LookupHost(ctx, h.Address); err == nil {
		for _, ip := range ips {
			keys = append(keys, fmt.Sprintf("%s:%d", ip, port))
		}
	}
	return keys
}

func fallbackDirectTmux(ctx context.Context) ([]daemon.SessionState, error) {
	tss, err := tmux.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]daemon.SessionState, 0, len(tss))
	for _, ts := range tss {
		out = append(out, daemon.SessionState{
			Name: ts.Name, Host: "local", Path: ts.Path,
			Attached: ts.Attached, Windows: ts.Windows,
			Created: ts.Created, LastChange: ts.LastAttach,
			State: string(claude.StateUnknown),
		})
	}
	return out, nil
}

// daemonOnline reports whether the LOCAL ccmuxd answered — the question
// the status bar's daemon chip asks.
//
// It deliberately reads DaemonOK, not OK. OK means "this device's
// sessions are listable", which stays true on the direct-tmux fallback
// path with no daemon running at all; reading it here made the chip
// render "✓ daemon" on a machine whose daemon was dead, hiding the loss
// of bells, push, and the sleep lock behind a green check.
func daemonOnline(hs []hostStatus) bool {
	for _, h := range hs {
		// Check the Local flag, not the literal name "local": refresh
		// now stamps the local row with the actual hostname so the
		// Devices panel can show it alongside other machines. The
		// flag was added precisely so this predicate didn't need to
		// know the convention.
		if h.Local && h.DaemonOK {
			return true
		}
	}
	return false
}

func statePriority(s string) int {
	switch s {
	case string(claude.StateNeedsInput):
		return 0
	case string(claude.StateActive):
		return 1
	case string(claude.StateIdle):
		return 2
	case string(claude.StateError):
		return 3
	default:
		return 4
	}
}

// shortPeerName squeezes Tailscale's human-friendly HostName ("Sasha's
// Mac mini") into something legible on the Devices panel
// ("sashas-mac-mini"). Lifted out of tailnet so the App can also use
// it for NeedsInstall rows without re-exporting the helper.
func shortPeerName(s string) string {
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
	if len(out) == 0 {
		return s
	}
	return string(out)
}
