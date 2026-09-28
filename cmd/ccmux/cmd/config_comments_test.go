package cmd

import (
	"os"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/config"
)

// TestHostAddRemove_KeepsConfigComments — `ccmux host add` and `host
// remove` used to re-encode config.toml, wiping the user's comments.
// Now add appends one [[host]] block, and remove takes it back out,
// leaving the file exactly as the user wrote it.
func TestHostAddRemove_KeepsConfigComments(t *testing.T) {
	withTempCcmuxConfig(t)
	cfg := config.Defaults()
	cfg.Hosts = []config.Host{{Name: "mini", Address: "100.64.0.1", Port: 7474, Mosh: true}}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	p, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}
	base, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	annotated := strings.Replace(string(base), "[[host]]\n", "# the mac mini under the desk\n[[host]] # always on\n", 1)
	annotated = strings.Replace(annotated, `  mosh = true`, `  mosh = true # phone roams`, 1)
	if annotated == string(base) {
		t.Fatal("fixture drifted: nothing annotated")
	}
	if err := os.WriteFile(p, []byte(annotated), 0o600); err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) {
		t.Helper()
		c := newHostCmd()
		c.SetArgs(args)
		c.SilenceUsage = true
		c.SilenceErrors = true
		if err := c.Execute(); err != nil {
			t.Fatalf("host %v: %v", args, err)
		}
	}
	run("add", "air", "100.64.0.2")
	added, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"# the mac mini under the desk", "[[host]] # always on", "mosh = true # phone roams"} {
		if !strings.Contains(string(added), c) {
			t.Errorf("host add lost %q:\n%s", c, added)
		}
	}
	if !strings.Contains(string(added), `name = "air"`) {
		t.Fatalf("host add didn't write the new host:\n%s", added)
	}

	run("remove", "air")
	removed, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(removed) != annotated {
		t.Errorf("add + remove didn't restore the file:\n--- got\n%s\n--- want\n%s", removed, annotated)
	}
}
