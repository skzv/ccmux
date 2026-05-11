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
	"testing"
	"time"
)

func TestProtocol_SessionStateRoundTrip(t *testing.T) {
	in := SessionState{
		Name: "c-foo", Host: "local", Project: "foo", Path: "/Users/skz/Projects/foo",
		State: "needs_input", Attached: true, Windows: 3,
		Created: time.Now().Truncate(time.Second), LastChange: time.Now().Truncate(time.Second),
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
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sessions/c-foo/keep-awake", func(w http.ResponseWriter, r *http.Request) {
		// No request body expected.
		w.WriteHeader(204)
	})
	c := spawnFakeDaemon(t, mux)

	if err := c.ToggleKeepAwake(context.Background(), "c-foo"); err != nil {
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
	in := ProjectInfo{
		Name: "x", Host: "h", Path: "/p",
		HasGit: true, HasCM: false, HasDocs: true,
		Modified: time.Now().Truncate(time.Second),
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
