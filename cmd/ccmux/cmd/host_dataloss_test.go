package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHostAdd_CorruptConfigDoesNotWipe — regression for the data-loss
// bug where `ccmux host add` did `cfg, _ := config.Load()`, discarding
// the error. On a corrupt config.toml, Load returns Defaults() + an
// error; swallowing it and Saving would truncate the file, erasing
// every other host and all settings. The fix returns the error and
// leaves the file byte-for-byte untouched.
func TestHostAdd_CorruptConfigDoesNotWipe(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgPath := filepath.Join(home, ".config", "ccmux", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	// A deliberately broken TOML file with real user content we must not lose.
	corrupt := "theme = \"dracula\"\n[[host]\nname = \"mini\"\n=== not toml ==="
	if err := os.WriteFile(cfgPath, []byte(corrupt), 0o644); err != nil {
		t.Fatal(err)
	}

	c := newHostCmd()
	c.SetArgs([]string{"add", "newhost", "100.64.0.9"})
	c.SilenceUsage = true
	c.SilenceErrors = true
	err := c.Execute()
	if err == nil {
		t.Fatal("host add on a corrupt config should error, not silently rewrite")
	}
	if !strings.Contains(err.Error(), "load config") {
		t.Errorf("error should mention the load failure, got: %v", err)
	}

	// The file must be untouched — same bytes as before.
	got, rerr := os.ReadFile(cfgPath)
	if rerr != nil {
		t.Fatalf("config file disappeared: %v", rerr)
	}
	if string(got) != corrupt {
		t.Errorf("corrupt config was modified despite the abort:\n%s", got)
	}
}
