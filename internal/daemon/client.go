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
	"time"
)

// Client speaks the ccmuxd JSON protocol. One Client = one ccmuxd, whether
// local (via Unix socket) or remote (via HTTP on the tailnet).
type Client struct {
	hc     *http.Client
	base   string // base URL ("http://unix" for sockets, "http://host:port" for HTTP)
	scheme string
	addr   string // for diagnostics
}

// LocalClient connects to the local ccmuxd via the canonical Unix socket
// at ~/.local/state/ccmux/ccmuxd.sock.
func LocalClient() (*Client, error) {
	path, err := localSocketPath()
	if err != nil {
		return nil, err
	}
	hc := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", path)
			},
		},
		Timeout: 5 * time.Second,
	}
	return &Client{hc: hc, base: "http://unix", scheme: "unix", addr: path}, nil
}

// RemoteClient connects to a ccmuxd over plain HTTP on a tailnet address.
// `addr` is "host:port" (e.g. "mini.tail-xxxxx.ts.net:7474").
func RemoteClient(addr string) *Client {
	return &Client{
		hc:     &http.Client{Timeout: 5 * time.Second},
		base:   "http://" + addr,
		scheme: "http",
		addr:   addr,
	}
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

// ToggleKeepAwake flips the per-session keep-awake pin.
func (c *Client) ToggleKeepAwake(ctx context.Context, session string) error {
	return c.post(ctx, "/v1/sessions/"+session+"/keep-awake", nil, nil)
}

// Kill terminates a session via the daemon. (For local-only operations
// the TUI can also call internal/tmux directly; going through the daemon
// lets the daemon clean up its own state in one path.)
func (c *Client) Kill(ctx context.Context, session string) error {
	return c.post(ctx, "/v1/sessions/"+session+"/kill", nil, nil)
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
	// Read. (Bare-POST endpoints like keep-awake/kill hit this.)
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
