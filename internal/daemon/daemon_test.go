package daemon

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestProtocol_SessionStateRoundTrip(t *testing.T) {
	// Normalize to UTC so the round-trip's reflected location matches.
	// JSON encodes time.Time in RFC3339; Unmarshal always returns the
	// time with a *fixed* location (the offset embedded in the string)
	// rather than the original *time.Location pointer. On a TZ=local
	// runner (most laptops) the input pointer and the parsed pointer
	// don't `==` even though both represent the same wall time, so the
	// struct-level `==` below fails on CI runners (TZ=UTC) where the
	// input is local and the parsed back is UTC. Pre-normalizing to
	// UTC kills the difference.
	now := time.Now().UTC().Truncate(time.Second)
	in := SessionState{
		Name: "c-foo", Host: "local", Project: "foo", Path: "/Users/skz/Projects/foo",
		State: "needs_input", Attached: true, Windows: 3,
		Created: now, LastChange: now,
		PromptCount: 42, KeepAwake: true,
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out SessionState
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Fatalf("round-trip mismatch:\ngot=%+v\nwant=%+v", out, in)
	}
}

func TestProtocol_HealthInfoRoundTrip(t *testing.T) {
	in := HealthInfo{OK: true, Hostname: "h", Version: "1.0", Sessions: 7, SleepMode: "safe"}
	b, _ := json.Marshal(in)
	var out HealthInfo
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Fatalf("round-trip mismatch:\ngot=%+v\nwant=%+v", out, in)
	}
}

func TestSocketPath_UnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, err := SocketPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".local", "state", "ccmux", "ccmuxd.sock")
	if got != want {
		t.Fatalf("SocketPath = %q, want %q", got, want)
	}
}

// spawnFakeDaemon stands up a tiny HTTP server on a tempfile Unix socket
// and returns a Client pointed at it. Each test gets its own daemon.
// Uses /tmp directly (not t.TempDir()) because macOS sockaddr_un is
// capped at 104 bytes and the per-test temp paths overflow.
func spawnFakeDaemon(t *testing.T, mux http.Handler) *Client {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ccmux-d-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		_ = srv.Close()
		_ = os.Remove(sock)
	})

	// Build a client that targets THIS socket directly (we bypass
	// LocalClient because that derives path from $HOME, and we'd rather
	// keep the env in this test small).
	hc := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", sock)
			},
		},
		Timeout: 2 * time.Second,
	}
	return &Client{hc: hc, base: "http://unix", scheme: "unix", addr: sock}
}

func TestClient_Health(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(HealthInfo{OK: true, Hostname: "test", Version: "v0", Sessions: 3, SleepMode: "off"})
	})
	c := spawnFakeDaemon(t, mux)

	got, err := c.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !got.OK || got.Sessions != 3 || got.Hostname != "test" {
		t.Fatalf("Health: %+v", got)
	}
}

func TestClient_Sessions(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]SessionState{
			{Name: "c-foo", Host: "local", State: "active"},
			{Name: "c-bar", Host: "local", State: "idle"},
		})
	})
	c := spawnFakeDaemon(t, mux)

	got, err := c.Sessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "c-foo" || got[1].Name != "c-bar" {
		t.Fatalf("Sessions: %+v", got)
	}
}

func TestClient_GetReturnsErrorOn5xx(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	c := spawnFakeDaemon(t, mux)

	if _, err := c.Sessions(context.Background()); err == nil {
		t.Fatal("expected error on 500, got nil")
	} else if !strings.Contains(err.Error(), "status 500") {
		t.Errorf("error doesn't mention status: %v", err)
	}
}

func TestClient_PostJSONAndDecodeResponse(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "missing CT", 400)
			return
		}
		var req NewSessionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		_ = json.NewEncoder(w).Encode(SessionState{Name: "c-" + req.Project, State: "active"})
	})
	c := spawnFakeDaemon(t, mux)

	got, err := c.NewSession(context.Background(), NewSessionRequest{Project: "x", FirstInput: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "c-x" {
		t.Fatalf("NewSession: %+v", got)
	}
}

func TestClient_PostNoBodyStillSucceeds(t *testing.T) {
	// Regression: a typed-nil io.Reader trips net/http's non-nil body
	// path and nil-dereferences on Read. c.post(nil, nil) must send a
	// bare POST with no body — not a typed-nil Reader.
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/noop", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	c := spawnFakeDaemon(t, mux)

	if err := c.post(context.Background(), "/v1/noop", nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestIsUnreachable(t *testing.T) {
	if IsUnreachable(nil) {
		t.Error("nil shouldn't be unreachable")
	}
	// A real connection-refused error (no daemon at this socket).
	dir, err := os.MkdirTemp("/tmp", "ccmux-d-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	hc := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", filepath.Join(dir, "nope"))
			},
		},
		Timeout: 200 * time.Millisecond,
	}
	c := &Client{hc: hc, base: "http://unix", scheme: "unix", addr: "nope"}
	_, err = c.Health(context.Background())
	if err == nil {
		t.Fatal("expected error from missing daemon")
	}
	if !IsUnreachable(err) {
		t.Errorf("IsUnreachable should classify connect-failure: %v", err)
	}
}

func TestRemoteClient_TargetsHTTPHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(HealthInfo{OK: true, Hostname: "remote", Version: "x"})
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	c := RemoteClient(addr)
	got, err := c.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Hostname != "remote" {
		t.Fatalf("Health: %+v", got)
	}
	if !strings.HasPrefix(c.Addr(), "http://") {
		t.Errorf("Addr should be http-scheme: %s", c.Addr())
	}
}

func TestClient_Projects(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]ProjectInfo{
			{Name: "alpha", Host: "mac-mini", Path: "/Users/skz/Projects/alpha"},
			{Name: "beta", Host: "mac-mini", Path: "/Users/skz/Projects/beta", HasGit: true},
		})
	})
	c := spawnFakeDaemon(t, mux)
	got, err := c.Projects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "alpha" || got[1].Host != "mac-mini" {
		t.Fatalf("Projects: %+v", got)
	}
	if !got[1].HasGit {
		t.Errorf("HasGit not deserialized: %+v", got[1])
	}
}

func TestProjectInfo_JSONRoundTrip(t *testing.T) {
	// UTC for the same time.Location round-trip reason as
	// TestProtocol_SessionStateRoundTrip — see comment there.
	in := ProjectInfo{
		Name: "x", Host: "h", Path: "/p",
		HasGit: true, HasCM: false, HasDocs: true,
		Modified: time.Now().UTC().Truncate(time.Second),
	}
	b, _ := json.Marshal(in)
	var out ProjectInfo
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Fatalf("round-trip:\n got=%+v\nwant=%+v", out, in)
	}
}

func TestClient_AddrReportsScheme(t *testing.T) {
	c := RemoteClient("host:1234")
	if want := "http://host:1234"; c.Addr() != want {
		t.Errorf("Addr = %q, want %q", c.Addr(), want)
	}
}

// TestNewBareSessionRequest_JSONRoundTrip pins the wire shape. The
// Sessions tab's "new session" feature posts this to the daemon;
// once we've shipped, a json tag rename here is a breaking change.
func TestNewBareSessionRequest_JSONRoundTrip(t *testing.T) {
	cases := []NewBareSessionRequest{
		{Name: "c-shell-1", Path: "/Users/skz"},
		{Name: "", Path: "/tmp/x"},
		{},
	}
	for _, in := range cases {
		b, _ := json.Marshal(in)
		var out NewBareSessionRequest
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if out != in {
			t.Errorf("round-trip mismatch: got %+v want %+v\nwire: %s", out, in, b)
		}
	}
}

// TestNewBareSessionRequest_OmitsEmpty — `omitempty` keeps unused
// fields off the wire so an older daemon doesn't have to handle
// strings it didn't expect.
func TestNewBareSessionRequest_OmitsEmpty(t *testing.T) {
	empty, _ := json.Marshal(NewBareSessionRequest{})
	if string(empty) != "{}" {
		t.Errorf("empty request should marshal to {}, got %s", empty)
	}
	pop, _ := json.Marshal(NewBareSessionRequest{Name: "x", Path: "/tmp"})
	if !strings.Contains(string(pop), `"name":"x"`) || !strings.Contains(string(pop), `"path":"/tmp"`) {
		t.Errorf("non-empty request missing fields: %s", pop)
	}
}

// TestClient_NewBareSession_RoundTrip — fake-daemon end-to-end. The
// client serializes a request, the server echoes via a known shape,
// the client decodes the response. Pins the path is /v1/sessions/bare
// (not /v1/sessions/ which would route to handleSessionsItem).
func TestClient_NewBareSession_RoundTrip(t *testing.T) {
	mux := http.NewServeMux()
	gotPath := ""
	mux.HandleFunc("/v1/sessions/bare", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.Method != http.MethodPost {
			http.Error(w, "want POST", http.StatusMethodNotAllowed)
			return
		}
		var req NewBareSessionRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = json.NewEncoder(w).Encode(NewBareSessionResponse{
			Session: "c-shell-42",
			Path:    req.Path,
			Host:    "test-host",
		})
	})
	c := spawnFakeDaemon(t, mux)
	got, err := c.NewBareSession(context.Background(), NewBareSessionRequest{
		Name: "", Path: "/tmp/x",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/sessions/bare" {
		t.Errorf("server saw path %q, want /v1/sessions/bare", gotPath)
	}
	if got.Session != "c-shell-42" || got.Path != "/tmp/x" || got.Host != "test-host" {
		t.Errorf("response: %+v", got)
	}
}

// TestClient_NewProject_RoundTrip covers the Projects screen's "create
// on <host>" path: client serializes a NewProjectRequest, server
// echoes a NewProjectResponse, client decodes it. The server-side
// scaffold work lives in ccmuxd; this test pins the protocol so a
// rename can't silently break cross-device project creation.
func TestClient_NewProject_RoundTrip(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "want POST, got "+r.Method, http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "missing CT", http.StatusBadRequest)
			return
		}
		var req NewProjectRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(NewProjectResponse{
			Session: "c-" + req.Name,
			Path:    "/Users/skz/Projects/" + req.Name,
			Host:    "mac-mini",
		})
	})
	c := spawnFakeDaemon(t, mux)

	got, err := c.NewProject(context.Background(), NewProjectRequest{
		Name: "alpha",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Session != "c-alpha" || got.Host != "mac-mini" || got.Path != "/Users/skz/Projects/alpha" {
		t.Errorf("NewProject = %+v, want session=c-alpha host=mac-mini", got)
	}
}

// TestClient_NewProject_ServerError ensures a 5xx from the daemon
// surfaces as an error (not a zero-value response masquerading as
// success). The TUI's toast key off err != nil to show "new project on
// <host>: …", so silent success on 500 would be a real footgun.
func TestClient_NewProject_ServerError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "disk full", http.StatusInternalServerError)
	})
	c := spawnFakeDaemon(t, mux)

	_, err := c.NewProject(context.Background(), NewProjectRequest{Name: "x"})
	if err == nil {
		t.Fatal("expected error on 500, got nil")
	}
}

// TestNewProjectResponse_JSONRoundTrip pins the wire shape of the
// response so json tag renames trip a test instead of a runtime bug.
func TestNewProjectResponse_JSONRoundTrip(t *testing.T) {
	in := NewProjectResponse{Session: "c-foo", Path: "/p", Host: "h"}
	b, _ := json.Marshal(in)
	var out NewProjectResponse
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Errorf("round-trip: got %+v want %+v", out, in)
	}
}

// TestNewProjectRequest_AgentField_RoundTrip pins the Phase-3 wire
// addition. Without this an older daemon talking to a newer client
// would silently drop the field, and every new project on that remote
// would default to claude regardless of the picker.
func TestNewProjectRequest_AgentField_RoundTrip(t *testing.T) {
	for _, id := range []string{"claude", "codex", "antigravity", "gemini", ""} {
		in := NewProjectRequest{Name: "p", Agent: id}
		b, _ := json.Marshal(in)
		var out NewProjectRequest
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatal(err)
		}
		if out.Agent != id {
			t.Errorf("Agent round-trip: in=%q out=%q", id, out.Agent)
		}
	}
}

// TestNewProjectRequest_AgentOmitted_WhenEmpty — `Agent,omitempty`
// keeps old clients from sending an empty string that a strict server
// might reject. This is what gives us back-compat across mixed-version
// daemons on the same tailnet.
func TestNewProjectRequest_AgentOmitted_WhenEmpty(t *testing.T) {
	b, _ := json.Marshal(NewProjectRequest{Name: "p"})
	if strings.Contains(string(b), `"agent"`) {
		t.Errorf("empty Agent should be omitted from wire:\n%s", b)
	}
	b, _ = json.Marshal(NewProjectRequest{Name: "p", Agent: "codex"})
	if !strings.Contains(string(b), `"agent":"codex"`) {
		t.Errorf("non-empty Agent missing from wire:\n%s", b)
	}
}

// TestSessionState_AgentField_RoundTrip — Phase 4 adds Agent to the
// SessionState wire shape so the dashboard can show per-row badges.
// Empty must be omitted on the wire so older daemons that don't
// populate the field don't ship empty quoted strings that a strict
// client would have to special-case.
func TestSessionState_AgentField_RoundTrip(t *testing.T) {
	cases := []struct {
		name       string
		agent      string
		wantInWire string // substring expected (or NOT expected via omit)
		omit       bool
	}{
		{"codex serialized", "codex", `"agent":"codex"`, false},
		{"claude serialized", "claude", `"agent":"claude"`, false},
		{"empty omitted", "", `"agent"`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := json.Marshal(SessionState{Name: "c-x", Agent: tc.agent})
			has := strings.Contains(string(b), tc.wantInWire)
			if tc.omit && has {
				t.Errorf("empty Agent should be omitted; wire = %s", b)
			}
			if !tc.omit && !has {
				t.Errorf("Agent missing from wire; got %s", b)
			}
		})
	}
}

// TestClient_NewProject_ForwardsAgent — the client serializes whatever
// the caller hands it. End-to-end pin so the protocol field actually
// reaches the server side; the picker fix doesn't help if the client
// drops Agent on its way over the wire.
func TestClient_NewProject_ForwardsAgent(t *testing.T) {
	gotBody := make(chan NewProjectRequest, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		var req NewProjectRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		gotBody <- req
		_ = json.NewEncoder(w).Encode(NewProjectResponse{Session: "c-x", Path: "/p", Host: "h"})
	})
	c := spawnFakeDaemon(t, mux)
	if _, err := c.NewProject(context.Background(), NewProjectRequest{
		Name: "x", Agent: "codex",
	}); err != nil {
		t.Fatal(err)
	}
	got := <-gotBody
	if got.Agent != "codex" {
		t.Errorf("server received Agent=%q, want codex", got.Agent)
	}
}

// TestLocalClient_IsProcessSingleton pins the fix for the fd leak that
// motivated the LocalClient memoization: callers got a fresh *http.Client
// (and *http.Transport) per call, each with its own idle-conn pool that
// the Go runtime only reaped via GC + finalizers — which on a low-allocation
// TUI almost never ran promptly. After the fix, every LocalClient() must
// return the SAME *Client pointer.
func TestLocalClient_IsProcessSingleton(t *testing.T) {
	resetClientCacheForTest()
	t.Cleanup(resetClientCacheForTest)

	a, err := LocalClient()
	if err != nil {
		t.Fatalf("first LocalClient: %v", err)
	}
	b, err := LocalClient()
	if err != nil {
		t.Fatalf("second LocalClient: %v", err)
	}
	if a != b {
		t.Fatalf("LocalClient returned different pointers across calls: %p vs %p", a, b)
	}
	if a.hc != b.hc {
		t.Fatalf("LocalClient returned different http.Client across calls")
	}
}

// TestRemoteClient_MemoizesByAddr pins the matching guarantee for
// RemoteClient: same addr re-uses the cached Client, different addrs
// each get their own.
func TestRemoteClient_MemoizesByAddr(t *testing.T) {
	resetClientCacheForTest()
	t.Cleanup(resetClientCacheForTest)

	a1 := RemoteClient("host-a:7474")
	a2 := RemoteClient("host-a:7474")
	b1 := RemoteClient("host-b:7474")
	if a1 != a2 {
		t.Errorf("same addr returned different *Client: %p vs %p", a1, a2)
	}
	if a1 == b1 {
		t.Errorf("different addrs returned the same *Client: %p", a1)
	}
}

// TestLocalClient_TransportHasIdleTuning checks the defense-in-depth
// layer: even if a future refactor accidentally bypasses the singleton
// cache, every Transport must carry a finite IdleConnTimeout so leaked
// conns get reaped within ~30s instead of "never" (the zero-value
// behavior of http.Transport).
func TestLocalClient_TransportHasIdleTuning(t *testing.T) {
	resetClientCacheForTest()
	t.Cleanup(resetClientCacheForTest)

	c, err := LocalClient()
	if err != nil {
		t.Fatalf("LocalClient: %v", err)
	}
	tr, ok := c.hc.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("LocalClient Transport not *http.Transport: %T", c.hc.Transport)
	}
	if tr.IdleConnTimeout <= 0 {
		t.Errorf("IdleConnTimeout=%v, want >0 (zero-value lets idle conns linger forever)", tr.IdleConnTimeout)
	}
	if tr.MaxIdleConnsPerHost <= 0 {
		t.Errorf("MaxIdleConnsPerHost=%d, want >0", tr.MaxIdleConnsPerHost)
	}
}

// TestRemoteClient_TransportHasIdleTuning mirrors the above for
// RemoteClient — both factories need to pin the same guarantee since
// they share the leak surface.
func TestRemoteClient_TransportHasIdleTuning(t *testing.T) {
	resetClientCacheForTest()
	t.Cleanup(resetClientCacheForTest)

	c := RemoteClient("example:7474")
	tr, ok := c.hc.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("RemoteClient Transport not *http.Transport: %T", c.hc.Transport)
	}
	if tr.IdleConnTimeout <= 0 {
		t.Errorf("IdleConnTimeout=%v, want >0", tr.IdleConnTimeout)
	}
	if tr.MaxIdleConnsPerHost <= 0 {
		t.Errorf("MaxIdleConnsPerHost=%d, want >0", tr.MaxIdleConnsPerHost)
	}
}

// TestRemoteClient_ReusesConnectionsAcrossManyRequests is the
// end-to-end behavioral test. It wraps the test server's listener with
// an accept-counter, fires N sequential requests through one cached
// RemoteClient, and asserts the server saw FAR fewer accepts than
// requests — proving keep-alive + idle-conn reuse is working as
// intended. Before the fix, every refresh tick built a fresh Transport,
// so each request opened a new conn; this test would fail with
// accepts == requests (or worse).
func TestRemoteClient_ReusesConnectionsAcrossManyRequests(t *testing.T) {
	resetClientCacheForTest()
	t.Cleanup(resetClientCacheForTest)

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(HealthInfo{OK: true, Hostname: "x", Version: "v"})
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var accepts int64
	cl := &countingListener{Listener: ln, accepts: &accepts}
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(cl) }()
	t.Cleanup(func() { _ = srv.Close() })

	addr := ln.Addr().String()
	c1 := RemoteClient(addr)

	const N = 50
	for i := 0; i < N; i++ {
		if _, err := c1.Health(context.Background()); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}

	// Second lookup must hit the cache.
	c2 := RemoteClient(addr)
	if c1 != c2 {
		t.Fatalf("RemoteClient(%q) returned different pointers across calls", addr)
	}

	for i := 0; i < N; i++ {
		if _, err := c2.Health(context.Background()); err != nil {
			t.Fatalf("second-batch request %d: %v", i, err)
		}
	}

	got := atomic.LoadInt64(&accepts)
	// Sequential requests over keep-alive should reuse a single conn.
	// We allow a small slack (≤4) to absorb transient pool churn — the
	// real assertion is "nowhere near 2*N=100". Pre-fix this would be 100.
	if got > 4 {
		t.Errorf("server accepted %d conns for %d requests; expected ≤4 (keep-alive reuse not working)", got, 2*N)
	}
}

// countingListener wraps a net.Listener and increments a counter on
// every Accept. Used by TestRemoteClient_ReusesConnectionsAcrossManyRequests
// to assert keep-alive is reusing conns rather than dialing fresh.
type countingListener struct {
	net.Listener
	accepts *int64
}

func (c *countingListener) Accept() (net.Conn, error) {
	conn, err := c.Listener.Accept()
	if err == nil {
		atomic.AddInt64(c.accepts, 1)
	}
	return conn, err
}
