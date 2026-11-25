package cmd

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/skzv/ccmux/internal/config"
)

func TestUpdateDryRun_HomebrewSkipsSetupOffer(t *testing.T) {
	origCheck, origOffer := checkHomebrew, offerSetup
	t.Cleanup(func() {
		checkHomebrew = origCheck
		offerSetup = origOffer
	})
	checkHomebrew = func(context.Context, string) bool { return true }
	offered := false
	offerSetup = func(context.Context, bool, bool) error {
		offered = true
		return nil
	}

	c := newUpdateCmd()
	c.SetArgs([]string{"--dry-run", "--setup"})
	if err := c.Execute(); err != nil {
		t.Fatalf("update --dry-run failed: %v", err)
	}
	if offered {
		t.Fatal("update --dry-run on Homebrew path offered/ran setup")
	}
}

func TestParseAdHocTarget_PortsAndIPv6(t *testing.T) {
	valid := []struct {
		in       string
		wantHost string
		wantPort int
		wantUser string
	}{
		{"[fd7a::1]", "fd7a::1", 22, ""},
		{"alice@[fd7a::1]:2222", "fd7a::1", 2222, "alice"},
		{"fd7a::1", "fd7a::1", 22, ""},
		{"alice@sputnik:65535", "sputnik", 65535, "alice"},
		{"alice@sputnik:1", "sputnik", 1, "alice"},
	}
	for _, tc := range valid {
		t.Run("valid/"+tc.in, func(t *testing.T) {
			got, _, err := parseAdHocTarget(tc.in)
			if err != nil {
				t.Fatalf("parseAdHocTarget(%q) unexpected error: %v", tc.in, err)
			}
			if got.Host != tc.wantHost || got.Port != tc.wantPort {
				t.Errorf("got %+v, want host=%q port=%d", got, tc.wantHost, tc.wantPort)
			}
			if tc.wantUser != "" && got.User != tc.wantUser {
				t.Errorf("User = %q, want %q", got.User, tc.wantUser)
			}
		})
	}

	invalid := []string{
		"sputnik:",
		"alice@sputnik:0",
		"alice@sputnik:65536",
		"alice@sputnik:99999999999999999999",
		"[fd7a::1]:",
		"[fd7a::1]:70000",
		"[]",
		"[fd7a::1]extra",
	}
	for _, in := range invalid {
		t.Run("invalid/"+in, func(t *testing.T) {
			if got, _, err := parseAdHocTarget(in); err == nil {
				t.Errorf("parseAdHocTarget(%q) = %+v, want error", in, got)
			}
		})
	}
}

func TestDefaultHostName_PreservesIPs(t *testing.T) {
	for _, ip := range []string{"100.64.1.2", "10.0.0.1", "fd7a:115c:a1e0::1"} {
		if got := defaultHostName(ip); got != ip {
			t.Errorf("defaultHostName(%q) = %q, want %q", ip, got, ip)
		}
	}
}

func TestRemoteAttachHintPort_IncludesNonDefaultPort(t *testing.T) {
	def := remoteAttachHintPort("me@mini", 22, "c-shell-1")
	if strings.Contains(def, " -p ") {
		t.Errorf("default port 22 should omit -p: %q", def)
	}
	custom := remoteAttachHintPort("me@mini", 2222, "c-shell-1")
	if !strings.Contains(custom, "ssh -t -p 2222 me@mini") {
		t.Errorf("custom port hint = %q, want -p 2222", custom)
	}
}

func TestRemoteAttachCmd_HonorsMosh(t *testing.T) {
	moshHost := config.Host{Address: "mini", User: "alice", SSHPort: 2222, Mosh: true}
	cmd := remoteAttachCmd(moshHost, "c-work", false)
	if len(cmd.Args) == 0 || cmd.Args[0] != "mosh" {
		t.Errorf("mosh host args = %v, want mosh", cmd.Args)
	}
	sshHost := config.Host{Address: "mini", User: "alice", SSHPort: 2222, Mosh: false}
	cmd2 := remoteAttachCmd(sshHost, "c-work", false)
	if len(cmd2.Args) == 0 || cmd2.Args[0] != "ssh" {
		t.Errorf("ssh host args = %v, want ssh", cmd2.Args)
	}
}

func TestTruncateRunes_MultiByteIDValidUTF8(t *testing.T) {
	got := truncateRunes("会话标识符非常长的一个名字", 12)
	if !utf8.ValidString(got) {
		t.Errorf("truncateRunes produced invalid UTF-8: %q", got)
	}
	if len([]rune(got)) != 12 {
		t.Errorf("rune len = %d, want 12 (%q)", len([]rune(got)), got)
	}
}
