//go:build integration

package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHTTPParity_LoopbackMatchesUnixSocket covers the daemon IPC/API
// CUJ's parity scenario: the daemon serves the same data over a
// loopback HTTP port and over its Unix socket, with schema-identical
// responses.
//
// Both transports serve the one http.ServeMux that srv.routes builds —
// ccmuxd's run() hands that mux to both the Unix-socket http.Server and
// the tailnet listener. This test stands the same mux up on a loopback
// TCP port (httptest binds 127.0.0.1) and on a Unix socket, issues the
// same GETs to each, and asserts byte-identical JSON. If a future
// change forks the routing or wraps one transport in output-altering
// middleware, the comparison fails.
func TestHTTPParity_LoopbackMatchesUnixSocket(t *testing.T) {
	dir := pollSandbox(t)

	// Real state for the endpoints to report: a project on disk and a
	// live session in the isolated tmux server.
	projDir := filepath.Join(dir, "parityproj")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, "CLAUDE.md"), []byte("# parityproj\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustTmux(t, "new-session", "-d", "-s", "c-parity", "-c", dir)

	srv := newServer(testDaemonCfg(dir))
	srv.startSleepManager() // handleHealth reads srv.sleeper

	mux := http.NewServeMux()
	srv.routes(mux)

	// Transport A: loopback HTTP — httptest binds 127.0.0.1:<port>.
	httpSrv := httptest.NewServer(mux)
	defer httpSrv.Close()

	// Transport B: Unix socket — the same mux, served on a unix
	// listener, exactly as ccmuxd's run() wires it.
	sockPath := filepath.Join(dir, "parity.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	unixSrv := &http.Server{Handler: mux}
	go func() { _ = unixSrv.Serve(ln) }()
	defer unixSrv.Close()

	unixClient := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", sockPath)
			},
		},
	}

	for _, path := range []string{"/v1/health", "/v1/sessions", "/v1/projects"} {
		httpBody := getBody(t, httpSrv.Client(), httpSrv.URL+path)
		unixBody := getBody(t, unixClient, "http://unix"+path)
		if string(httpBody) != string(unixBody) {
			t.Errorf("%s: HTTP and Unix-socket responses differ\n http: %s\n unix: %s",
				path, httpBody, unixBody)
		}
		// The body must be well-formed JSON, not an HTML error page.
		var v any
		if err := json.Unmarshal(httpBody, &v); err != nil {
			t.Errorf("%s: response is not valid JSON: %v\n%s", path, err, httpBody)
		}
	}

	// Guard against the degenerate pass where both transports return an
	// identical empty/error body: the responses must actually reflect
	// the observed tmux + project state.
	if sessions := getBody(t, httpSrv.Client(), httpSrv.URL+"/v1/sessions"); !strings.Contains(string(sessions), "c-parity") {
		t.Errorf("/v1/sessions did not report the live session c-parity: %s", sessions)
	}
	if projects := getBody(t, httpSrv.Client(), httpSrv.URL+"/v1/projects"); !strings.Contains(string(projects), "parityproj") {
		t.Errorf("/v1/projects did not report the project parityproj: %s", projects)
	}
}

// getBody issues a GET and returns the response body, failing the test
// on any transport error or non-200 status.
func getBody(t *testing.T, c *http.Client, url string) []byte {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d", url, resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	return b
}
