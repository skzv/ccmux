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

func TestBrewServiceStartedFromJSON_GarbageInput(t *testing.T) {
	if brewServiceStartedFromJSON("") {
		t.Error("empty input should be false")
	}
	if brewServiceStartedFromJSON("not at all json [oops") {
		t.Error("malformed input should be false")
	}
}
