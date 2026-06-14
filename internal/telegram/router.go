package telegram

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/skzv/ccmux/internal/daemon"
)

// DaemonClient is the subset of *daemon.Client the bridge depends on.
// Declaring it here (rather than taking the concrete type) lets tests
// drive the whole bridge against a fake daemon with no socket. The real
// *daemon.Client satisfies it.
type DaemonClient interface {
	Sessions(ctx context.Context) ([]daemon.SessionState, error)
	Projects(ctx context.Context) ([]daemon.ProjectInfo, error)
	Preview(ctx context.Context, name string, lines int) (daemon.PreviewResponse, error)
	SendKeys(ctx context.Context, name, keys string) error
	Kill(ctx context.Context, name string) error
	NewSession(ctx context.Context, req daemon.NewSessionRequest) (daemon.SessionState, error)
	Usage(ctx context.Context) (daemon.AgentUsage, error)
	Notes(ctx context.Context, project string) ([]daemon.NoteEntry, error)
	NoteContent(ctx context.Context, project, rel string) (daemon.NoteContent, error)
	SearchNotes(ctx context.Context, project, query string) ([]daemon.SearchHit, error)
	AgentCommands(ctx context.Context, name string) (daemon.AgentCommandsResponse, error)
	Health(ctx context.Context) (daemon.HealthInfo, error)
}

// LocalHost is the reserved host label for the daemon the bridge runs
// on. A bare session target (no "host:") resolves here.
const LocalHost = "local"

// defaultPerHostTimeout bounds each peer fan-out call so one slow or
// dead peer can't stall a Telegram reply.
const defaultPerHostTimeout = 4 * time.Second

// Target is a parsed "host:session" address. A bare "session" parses to
// Host == LocalHost.
type Target struct {
	Host    string
	Session string
}

func (t Target) String() string { return t.Host + ":" + t.Session }

// ParseTarget splits a "host:session" address. Session names can't
// contain ":" (the daemon rejects such names), so splitting on the first
// ":" is unambiguous. A bare token is a local session.
func ParseTarget(s string) Target {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, ':'); i >= 0 {
		host := strings.TrimSpace(s[:i])
		if host == "" {
			host = LocalHost
		}
		return Target{Host: host, Session: strings.TrimSpace(s[i+1:])}
	}
	return Target{Host: LocalHost, Session: s}
}

// Router resolves session addresses to the daemon that owns them and
// fans reads out across the local daemon plus every configured peer —
// the tailnet-wide control plane. One Router is built per bridge from
// config; peers are keyed by host name.
type Router struct {
	local     DaemonClient
	peers     map[string]DaemonClient
	hostOrder []string // peer hosts, display order
	timeout   time.Duration
}

// NewRouter builds a Router. peers maps host name → client; nil/empty is
// fine (local-only). Host order is sorted for deterministic display.
func NewRouter(local DaemonClient, peers map[string]DaemonClient) *Router {
	order := make([]string, 0, len(peers))
	for h := range peers {
		order = append(order, h)
	}
	sort.Strings(order)
	return &Router{
		local:     local,
		peers:     peers,
		hostOrder: order,
		timeout:   defaultPerHostTimeout,
	}
}

// Client resolves the daemon that owns sessions on the given host. A
// bare/"local" host returns the local client.
func (r *Router) Client(host string) (DaemonClient, bool) {
	if host == "" || host == LocalHost {
		return r.local, r.local != nil
	}
	c, ok := r.peers[host]
	return c, ok
}

// ClientFor resolves the daemon owning a parsed target.
func (r *Router) ClientFor(t Target) (DaemonClient, bool) { return r.Client(t.Host) }

// Hosts returns "local" followed by the peer hosts in display order.
func (r *Router) Hosts() []string {
	out := make([]string, 0, 1+len(r.hostOrder))
	if r.local != nil {
		out = append(out, LocalHost)
	}
	return append(out, r.hostOrder...)
}

// AllSessions fans out across the local daemon and every reachable peer,
// re-tagging each session's Host with its routing label. An unreachable
// peer (timeout/error) is silently omitted so local results still land —
// the spec's "degrade gracefully" requirement.
func (r *Router) AllSessions(ctx context.Context) []daemon.SessionState {
	var out []daemon.SessionState
	if r.local != nil {
		out = append(out, r.sessionsFrom(ctx, LocalHost, r.local)...)
	}
	for _, h := range r.hostOrder {
		out = append(out, r.sessionsFrom(ctx, h, r.peers[h])...)
	}
	return out
}

func (r *Router) sessionsFrom(ctx context.Context, host string, cli DaemonClient) []daemon.SessionState {
	if cli == nil {
		return nil
	}
	cctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	ss, err := cli.Sessions(cctx)
	if err != nil {
		return nil
	}
	for i := range ss {
		ss[i].Host = host
	}
	return ss
}
