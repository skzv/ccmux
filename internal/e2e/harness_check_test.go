//go:build integration

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
)

// TestHarness_Hermetic locks in the harness's isolation guarantee:
// the fixture's $HOME, config path, and daemon socket all resolve
// inside a temp sandbox, never the developer's real home. If this
// regresses, every other e2e test risks trampling user state.
func TestHarness_Hermetic(t *testing.T) {
	realHome := os.Getenv("HOME")

	e := newEnv(t)

	if got := os.Getenv("HOME"); got != e.Home {
		t.Fatalf("HOME = %q, want sandbox %q", got, e.Home)
	}
	if e.Home == realHome {
		t.Fatalf("sandbox HOME equals real HOME %q — not isolated", realHome)
	}

	cfgPath, err := config.Path()
	if err != nil {
		t.Fatalf("config.Path: %v", err)
	}
	if !strings.HasPrefix(cfgPath, e.Home) {
		t.Fatalf("config path %q escaped sandbox %q", cfgPath, e.Home)
	}

	sockPath, err := daemon.SocketPath()
	if err != nil {
		t.Fatalf("daemon.SocketPath: %v", err)
	}
	if !strings.HasPrefix(sockPath, e.Home) {
		t.Fatalf("daemon socket %q escaped sandbox %q", sockPath, e.Home)
	}

	// A session created during the test must land on the isolated
	// tmux server and nowhere else.
	e.newTmuxSession("c-hermetic-check", e.Home)
	if !e.hasSession("c-hermetic-check") {
		t.Fatal("session not found on the isolated tmux server")
	}
}

// TestHarness_CcmuxPathUsesTempBuild makes the e2e safety contract
// explicit: anything that resolves `ccmux` or `ccmuxd` through PATH
// inside a fixture must get the freshly built temp binaries, never a
// developer-installed copy.
func TestHarness_CcmuxPathUsesTempBuild(t *testing.T) {
	e := newEnv(t)

	ccmuxPath, err := exec.LookPath("ccmux")
	if err != nil {
		t.Fatalf("LookPath ccmux: %v", err)
	}
	if ccmuxPath != builtCcmux {
		t.Fatalf("PATH ccmux = %q, want temp build %q", ccmuxPath, builtCcmux)
	}

	ccmuxdPath, err := exec.LookPath("ccmuxd")
	if err != nil {
		t.Fatalf("LookPath ccmuxd: %v", err)
	}
	if ccmuxdPath != builtCcmuxd {
		t.Fatalf("PATH ccmuxd = %q, want temp build %q", ccmuxdPath, builtCcmuxd)
	}

	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == binDir {
			continue
		}
		if executableExists(filepath.Join(dir, "ccmux")) || executableExists(filepath.Join(dir, "ccmuxd")) {
			t.Fatalf("e2e PATH includes installed ccmux dir %q", dir)
		}
	}

	_ = e
}

// TestHarness_TmuxIsolated confirms two fixtures do not share a tmux
// server: a session created under one Env is invisible to a freshly
// listed server in a subtest with its own sandbox.
func TestHarness_TmuxIsolated(t *testing.T) {
	e := newEnv(t)
	e.newTmuxSession("c-iso-a", e.Home)
	if !e.hasSession("c-iso-a") {
		t.Fatal("c-iso-a missing on its own server")
	}

	t.Run("separate-sandbox", func(t *testing.T) {
		e2 := newEnv(t)
		for _, n := range e2.sessionNames() {
			if n == "c-iso-a" {
				t.Fatalf("session c-iso-a leaked into a separate sandbox")
			}
		}
	})
}
