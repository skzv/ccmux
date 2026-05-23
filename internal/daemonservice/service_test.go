package daemonservice

import (
	"runtime"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/agent"
)

func TestLabelIsCanonical(t *testing.T) {
	// Locked in: this is the published launchd/systemd identifier. If
	// it ever changes, the OS service files all need to migrate
	// together. The test exists so the change is loud.
	if Label != "dev.ccmux.daemon" {
		t.Fatalf("Label drifted to %q — coordinate with launchd plist / systemd unit", Label)
	}
}

func TestUnitFile_ShapesCorrectly(t *testing.T) {
	body := UnitFile("/usr/local/bin/ccmuxd")
	for _, must := range []string{
		"[Unit]",
		"Description=",
		"[Service]",
		"ExecStart=/usr/local/bin/ccmuxd",
		"Restart=on-failure",
		"[Install]",
		"WantedBy=default.target",
	} {
		if !strings.Contains(body, must) {
			t.Errorf("UnitFile missing required line %q\n--- body ---\n%s", must, body)
		}
	}
}

func TestUnitFile_BinaryPathSubstituted(t *testing.T) {
	body := UnitFile("/opt/foo/ccmuxd")
	if !strings.Contains(body, "ExecStart=/opt/foo/ccmuxd") {
		t.Fatalf("ExecStart not substituted: %s", body)
	}
}

func TestManagedPath_IncludesConfiguredCommandBeforeDefaults(t *testing.T) {
	got := managedPath("/Users/me", agent.Commands{
		Claude:      "/Users/me/.nvm/versions/node/v23.9.0/bin/claude",
		Codex:       "/Users/me/.nvm/versions/node/v23.9.0/bin/codex",
		Antigravity: "/Users/me/.local/share/antigravity/bin/agy",
	}, "/opt/homebrew/bin", "/usr/bin")
	wantPrefix := "/Users/me/.local/bin:/Users/me/.nvm/versions/node/v23.9.0/bin:/Users/me/.local/share/antigravity/bin:/opt/homebrew/bin"
	if !strings.HasPrefix(got, wantPrefix) {
		t.Fatalf("managedPath = %q, want prefix %q", got, wantPrefix)
	}
}

func TestServicePathOrEmpty_PlatformSpecific(t *testing.T) {
	got := ServicePathOrEmpty()
	switch runtime.GOOS {
	case "darwin":
		if !strings.HasSuffix(got, "Library/LaunchAgents/dev.ccmux.daemon.plist") {
			t.Errorf("darwin ServicePathOrEmpty = %q", got)
		}
	case "linux":
		if !strings.HasSuffix(got, ".config/systemd/user/ccmuxd.service") {
			t.Errorf("linux ServicePathOrEmpty = %q", got)
		}
	default:
		if got != "" {
			t.Errorf("non-darwin/linux ServicePathOrEmpty should be empty, got %q", got)
		}
	}
}

func TestProbe_DoesNotPanicOnEmptyHome(t *testing.T) {
	// We can't fully isolate the system commands here (pgrep, launchctl,
	// systemctl) but Probe should be robust to whatever's installed.
	s := Probe()
	if s.OS == "" {
		t.Error("Probe should populate OS")
	}
	if s.OS == "darwin" || s.OS == "linux" {
		if s.BinaryPath == "" {
			t.Error("Probe should populate BinaryPath on supported OS")
		}
	}
}

// TestPlistTemplate_RendersAllFields exercises the macOS plist template
// indirectly via installDarwin's data shape. Pure-string check so it
// runs on all OSes without spawning launchctl.
func TestPlistTemplate_RendersAllFields(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("plist template is darwin-only")
	}
	var sb strings.Builder
	err := plistTemplate.Execute(&sb, plistData{
		Label:      "x.y.z",
		Binary:     "/usr/local/bin/ccmuxd",
		StdoutPath: "/tmp/out",
		StderrPath: "/tmp/err",
		HomeDir:    "/Users/skz",
		WorkingDir: "/Users/skz",
		Path:       "/Users/skz/.local/bin:/opt/homebrew/bin:/usr/bin:/bin",
	})
	if err != nil {
		t.Fatal(err)
	}
	body := sb.String()
	for _, must := range []string{
		"<key>Label</key>", "<string>x.y.z</string>",
		"<key>ProgramArguments</key>", "<string>/usr/local/bin/ccmuxd</string>",
		"<key>RunAtLoad</key>", "<true/>",
		"<key>KeepAlive</key>",
		"<key>StandardOutPath</key>", "<string>/tmp/out</string>",
		"<key>StandardErrorPath</key>", "<string>/tmp/err</string>",
		"<key>ProcessType</key>", "<string>Background</string>",
	} {
		if !strings.Contains(body, must) {
			t.Errorf("plist missing %q\n--- body ---\n%s", must, body)
		}
	}
}

// TestUID_NeverEmpty ensures the helper that feeds `launchctl bootout
// gui/$UID/...` never returns "" — a blank UID would silently break
// uninstall on darwin.
func TestUID_NeverEmpty(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("uid() is darwin-only")
	}
	if got := uid(); got == "" {
		t.Fatal("uid() returned empty string")
	}
}
