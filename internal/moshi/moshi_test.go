package moshi

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSuppressBell_HooksAlone(t *testing.T) {
	s := Status{HooksInstalled: true}
	if !s.SuppressBell() {
		t.Fatal("hooks-installed alone should suppress bell")
	}
}

func TestSuppressBell_BinaryAndPaired(t *testing.T) {
	if !(Status{BinaryInstalled: true, Paired: true}).SuppressBell() {
		t.Fatal("binary+paired should suppress bell")
	}
}

func TestSuppressBell_BinaryWithoutPairingDoesNotSuppress(t *testing.T) {
	if (Status{BinaryInstalled: true, Paired: false}).SuppressBell() {
		t.Fatal("unpaired moshi-hook should NOT suppress bell (else iOS gets nothing)")
	}
}

func TestSuppressBell_AllZero(t *testing.T) {
	if (Status{}).SuppressBell() {
		t.Fatal("zero status should not suppress")
	}
}

func TestInstallCmds_ContainsBrewTapAndInstall(t *testing.T) {
	got := InstallCmds()
	if len(got) < 2 {
		t.Fatalf("expected ≥ 2 commands, got %d", len(got))
	}
	hasTap, hasInstall := false, false
	for _, cmd := range got {
		joined := ""
		for _, a := range cmd {
			joined += a + " "
		}
		if joined != "" {
			if cmd[0] == "brew" && len(cmd) > 1 && cmd[1] == "tap" {
				hasTap = true
			}
			if cmd[0] == "brew" && len(cmd) > 1 && cmd[1] == "install" {
				hasInstall = true
			}
		}
	}
	if !hasTap || !hasInstall {
		t.Errorf("InstallCmds = %v; want a tap and install pair", got)
	}
}

func TestClaudeSettingsMentionsMoshi(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// No file → false.
	if claudeSettingsMentionsMoshi() {
		t.Fatal("no settings → should be false")
	}
	// File without moshi-hook → false.
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(`{"model":"opus"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if claudeSettingsMentionsMoshi() {
		t.Fatal("file without moshi-hook string → should be false")
	}
	// File mentioning moshi-hook → true.
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(`{"hooks":{"x":[{"hooks":[{"command":"moshi-hook fire"}]}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if !claudeSettingsMentionsMoshi() {
		t.Fatal("moshi-hook present in settings.json → should be true")
	}

	// settings.local.json alone is enough.
	_ = os.Remove(filepath.Join(claudeDir, "settings.json"))
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.local.json"), []byte(`{"x":"moshi-hook"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if !claudeSettingsMentionsMoshi() {
		t.Fatal("settings.local.json with moshi-hook → should be true")
	}
}

func TestBrewServiceStartedFromJSON_JSONFormat(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"empty array", `[]`, false},
		{
			"started",
			`[{"name":"moshi-hook","status":"started"},{"name":"other","status":"stopped"}]`,
			true,
		},
		{
			"stopped",
			`[{"name":"moshi-hook","status":"none"}]`,
			false,
		},
		{
			"not present",
			`[{"name":"redis","status":"started"}]`,
			false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := brewServiceStartedFromJSON(tc.body); got != tc.want {
				t.Fatalf("got %v, want %v (body=%s)", got, tc.want, tc.body)
			}
		})
	}
}

func TestBrewServiceStartedFromJSON_TableFallback(t *testing.T) {
	// Older brew prints a table:
	body := "Name        Status   User   File\nmoshi-hook  started  skz    ~/Library/.../plist\nnginx       none\n"
	if !brewServiceStartedFromJSON(body) {
		t.Fatal("table-format started line missed")
	}
	body2 := "Name        Status   User   File\nmoshi-hook  none     -      -\n"
	if brewServiceStartedFromJSON(body2) {
		t.Fatal("table-format 'none' wrongly matched")
	}
}

func TestDetectFix_RemoteLogin(t *testing.T) {
	// Real-world output from `moshi-hook host setup` when Remote
	// Login isn't enabled — verbatim sample from the user's logs.
	output := `found tailscale: sputnik.tail46b64f.ts.net (100.112.85.37)
host prerequisites failed:
- Remote Login is not enabled

Enable Remote Login with one of:
  • Run: sudo moshi-hook host enable-ssh
  • Open System Settings > General > Sharing and turn on Remote Login
`
	fix, ok := DetectFix(output)
	if !ok {
		t.Fatal("DetectFix should have recognized the Remote Login error")
	}
	if fix.Command != "sudo" || len(fix.Args) == 0 || fix.Args[0] != "moshi-hook" {
		t.Errorf("Command/Args wrong: %s %v", fix.Command, fix.Args)
	}
	if fix.SettingsURL == "" {
		t.Error("SettingsURL should be populated so callers can open the GUI alternative")
	}
}

func TestDetectFix_CaseInsensitive(t *testing.T) {
	output := "Some other text\nremote login is NOT enabled\netc."
	if _, ok := DetectFix(output); !ok {
		t.Error("DetectFix should be case-insensitive")
	}
}

func TestDetectFix_UnknownError(t *testing.T) {
	output := "some entirely different failure we haven't seen before"
	if fix, ok := DetectFix(output); ok {
		t.Errorf("unknown errors should return ok=false, got %+v", fix)
	}
}

func TestDetectFix_Empty(t *testing.T) {
	if _, ok := DetectFix(""); ok {
		t.Error("empty output should not match any known fix")
	}
}

func TestBrewServiceStartedFromJSON_GarbageInput(t *testing.T) {
	if brewServiceStartedFromJSON("") {
		t.Error("empty input should be false")
	}
	if brewServiceStartedFromJSON("not at all json [oops") {
		t.Error("malformed input should be false")
	}
}
