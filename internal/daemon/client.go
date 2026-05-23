package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Tunables for the cached transports built by LocalClient / RemoteClient.
// The motivating bug: a fresh *http.Client (and *http.Transport) was being
// constructed per refresh tick (every 2s on the TUI's dashboard refresh).
// Each Transport defaults to IdleConnTimeout=0 ("no timeout"), so its
// keep-alive Unix-socket conns sat in the idle pool forever, holding fds
// on BOTH the client process AND the ccmuxd server process. Over hours
// this exhausted kern.maxfiles and the daemon's accept loop spammed
// "too many open files in system" to ccmuxd.stderr.log.
//
// The fix is two-layered: (1) memoize so callers reuse one Client per
// target, and (2) cap idle conns + idle-conn lifetime as defense in
// depth in case a future caller bypasses the cache.
const (
	idleConnTimeout     = 30 * time.Second
	maxIdleConns        = 4
	maxIdleConnsPerHost = 2
	requestTimeout      = 5 * time.Second
)

// Client speaks the ccmuxd JSON protocol. One Client = one ccmuxd, whether
// local (via Unix socket) or remote (via HTTP on the tailnet).
type Client struct {
	hc     *http.Client
	base   string // base URL ("http://unix" for sockets, "http://host:port" for HTTP)
	scheme string
	addr   string // for diagnostics
}

// localClientCache memoizes the singleton LocalClient. One ccmuxd socket
// per user means one Client per process is the right cardinality. sync.Once
// gives us race-free lazy init without locking on the hot path.
var (
	localClientOnce sync.Once
	localClientVal  *Client
	localClientErr  error
)

// remoteClientCache memoizes RemoteClient(addr) by addr. sync.Map is
// the right shape for a read-heavy "look up by addr, occasionally insert"
// pattern — every refresh tick reads, only first-touch writes.
var remoteClientCache sync.Map // map[string]*Client

// LocalClient returns a process-wide Client targeting the local ccmuxd's
// Unix socket at ~/.local/state/ccmux/ccmuxd.sock. The Client (and its
// underlying *http.Transport) is constructed exactly once per process and
// reused on every subsequent call — see the package-level fd-leak comment
// above for why per-call construction is the wrong default here.
//
// The socket path is resolved INSIDE DialContext on every dial, not
// captured once at construction. In production this is a no-op (HOME
// doesn't change after process start), but it makes the singleton
// robust to `t.Setenv("HOME", ...)` between e2e tests — each test
// spawns its own daemon in a per-test temp HOME, and the cached client
// must follow. The Transport's keep-alive logic evicts the previous
// test's now-dead idle conn on first reuse and dials fresh against the
// new HOME's socket.
func LocalClient() (*Client, error) {
	localClientOnce.Do(func() {
		// Resolve once eagerly too, so a misconfigured environment
		// (no HOME) surfaces as an error from LocalClient() the same
		// way the pre-fix code did, instead of deferring to first dial.
		path, err := localSocketPath()
		if err != nil {
			localClientErr = err
			return
		}
		hc := &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					// Re-resolve per dial. localSocketPath is a cheap
					// os.UserHomeDir + filepath.Join; the alternative
					// (capturing `path` from the outer scope) pins the
					// singleton to whichever HOME was set at first call,
					// which breaks the e2e harness's per-test sandbox.
					p, err := localSocketPath()
					if err != nil {
						return nil, err
					}
					var d net.Dialer
					return d.DialContext(ctx, "unix", p)
				},
				IdleConnTimeout:     idleConnTimeout,
				MaxIdleConns:        maxIdleConns,
				MaxIdleConnsPerHost: maxIdleConnsPerHost,
			},
			Timeout: requestTimeout,
		}
		localClientVal = &Client{hc: hc, base: "http://unix", scheme: "unix", addr: path}
	})
	return localClientVal, localClientErr
}

// RemoteClient returns a process-wide Client for the given tailnet
// `addr` ("host:port", e.g. "mini.tail-xxxxx.ts.net:7474"). Successive
// calls with the same addr return the same *Client; different addrs
// each get their own.
func RemoteClient(addr string) *Client {
	if v, ok := remoteClientCache.Load(addr); ok {
		return v.(*Client)
	}
	cli := &Client{
		hc: &http.Client{
			Transport: &http.Transport{
				IdleConnTimeout:     idleConnTimeout,
				MaxIdleConns:        maxIdleConns,
				MaxIdleConnsPerHost: maxIdleConnsPerHost,
			},
			Timeout: requestTimeout,
		},
		base:   "http://" + addr,
		scheme: "http",
		addr:   addr,
	}
	// LoadOrStore handles the rare race where two goroutines miss the
	// initial Load and both construct: only one wins, the other is GC'd
	// without ever holding a connection.
	actual, _ := remoteClientCache.LoadOrStore(addr, cli)
	return actual.(*Client)
}

// resetClientCacheForTest clears the process-wide LocalClient/RemoteClient
// caches. Test-only; production code should never need this. Exposed at
// package scope (not in a _test.go file) so test helpers in other packages
// can use it if they ever need to, but kept unexported to keep the
// production surface clean.
func resetClientCacheForTest() {
	localClientOnce = sync.Once{}
	localClientVal = nil
	localClientErr = nil
	remoteClientCache = sync.Map{}
}

// localSocketPath returns the canonical Unix-socket path for ccmuxd.
func localSocketPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "ccmux", "ccmuxd.sock"), nil
}

// SocketPath is the public version of localSocketPath, used by the daemon
// to know where to bind.
func SocketPath() (string, error) { return localSocketPath() }

// Sessions returns every session known to this ccmuxd.
func (c *Client) Sessions(ctx context.Context) ([]SessionState, error) {
	var out []SessionState
	if err := c.getJSON(ctx, "/v1/sessions", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Health pings the daemon. Used to decide if a remote host is reachable
// for the dashboard's grey-out behavior.
func (c *Client) Health(ctx context.Context) (HealthInfo, error) {
	var out HealthInfo
	if err := c.getJSON(ctx, "/v1/health", &out); err != nil {
		return out, err
	}
	return out, nil
}

// Projects returns the list of projects discovered by this daemon
// under its configured projects root. Each entry is tagged with the
// daemon's hostname so callers merging across hosts can attribute
// rows back to origin.
func (c *Client) Projects(ctx context.Context) ([]ProjectInfo, error) {
	var out []ProjectInfo
	if err := c.getJSON(ctx, "/v1/projects", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// NewSession asks the daemon to spawn a fresh session.
func (c *Client) NewSession(ctx context.Context, req NewSessionRequest) (SessionState, error) {
	var out SessionState
	if err := c.post(ctx, "/v1/sessions", req, &out); err != nil {
		return out, err
	}
	return out, nil
}

// NewBareSession asks the daemon for a shell-only tmux session, no
// scaffold, no agent, no project association. Used by the Sessions
// tab's "new session" form for ad-hoc shells on the local host or
// any tailnet peer.
func (c *Client) NewBareSession(ctx context.Context, req NewBareSessionRequest) (NewBareSessionResponse, error) {
	var out NewBareSessionResponse
	if err := c.post(ctx, "/v1/sessions/bare", req, &out); err != nil {
		return out, err
	}
	return out, nil
}

// NewProject asks the daemon to scaffold a brand-new project on its
// host (under that daemon's configured Projects.Root) and start a
// Claude session inside it. Used by the Projects screen's "create on
// <host>" flow.
func (c *Client) NewProject(ctx context.Context, req NewProjectRequest) (NewProjectResponse, error) {
	var out NewProjectResponse
	if err := c.post(ctx, "/v1/projects", req, &out); err != nil {
		return out, err
	}
	return out, nil
}

// CreatePairToken asks the daemon to generate a one-time pairing token.
// Only succeeds over the Unix socket (unix-socket-only endpoint).
func (c *Client) CreatePairToken(ctx context.Context) (PairTokenResponse, error) {
	var out PairTokenResponse
	if err := c.post(ctx, "/v1/pair-token", nil, &out); err != nil {
		return out, err
	}
	return out, nil
}

// Addr returns a human description of this client's target.
func (c *Client) Addr() string { return c.scheme + "://" + c.addr }

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("ccmuxd %s GET %s: %w", c.addr, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("ccmuxd %s GET %s: status %d", c.addr, path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) post(ctx context.Context, path string, body, out any) error {
	// Important: pass an untyped nil io.Reader when there's no body —
	// a typed-nil *bytesReader satisfies the interface and trips
	// net/http's "non-nil body" path, which then nil-dereferences in
	// Read. (Bare-POST endpoints like /kill hit this.)
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = &bytesReader{b: b}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, rdr)
	if err != nil {
		return err
	}
	if rdr != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("ccmuxd %s POST %s: %w", c.addr, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("ccmuxd %s POST %s: status %d", c.addr, path, resp.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// IsUnreachable reports whether an error from this client is due to the
// daemon not running / not listening, vs. some other failure. Callers
// use this to fall back to direct tmux calls.
func IsUnreachable(err error) bool {
	if err == nil {
		return false
	}
	var nerr net.Error
	if errors.As(err, &nerr) {
		return true
	}
	return false
}

// bytesReader is a minimal io.Reader over a byte slice. We avoid bytes.NewReader
// to keep the import surface small.
type bytesReader struct {
	b []byte
	i int
}

func (r *bytesReader) Read(p []byte) (int, error) {
	if r.i >= len(r.b) {
		// Must be io.EOF (the sentinel) — Go's io.Copy treats any other
		// error as a real failure and net/http will not retire the
		// request body cleanly.
		return 0, io.EOF
	}
	n := copy(p, r.b[r.i:])
	r.i += n
	return n, nil
}
