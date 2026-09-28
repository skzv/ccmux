//go:build !windows

package cmd

import (
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestShellHost_UsesConfiguredTailnetPort — `ccmux shell --host` built
// the daemon address itself and fell back to 7474 when the host entry
// had no port, ignoring daemon.tailnet_port. Every other --host command
// (and the TUI) dials hostDaemonAddr: the host's port, else
// daemon.tailnet_port, else 7474.
func TestShellHost_UsesConfiguredTailnetPort(t *testing.T) {
	f := &fakeRemoteHost{}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	host, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	e := newCLIEnv(t)
	cfg := fmt.Sprintf("[daemon]\ntailnet_port = %s\n\n[[host]]\nname = \"box\"\naddress = %q\n", port, host)
	dir := filepath.Join(e.home, ".config", "ccmux")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	e.run("", "shell", "--host", "box", "--name", "scratch")
	for _, r := range f.all() {
		if strings.HasPrefix(r, "POST /v1/sessions/bare") {
			return
		}
	}
	t.Fatalf("shell --host never reached the daemon on daemon.tailnet_port %s; requests: %v", port, f.all())
}
