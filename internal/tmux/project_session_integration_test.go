//go:build integration

package tmux

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIntegration_SameBasenameProjects — on a real (isolated) tmux
// server, two projects whose folders share a name get a session each:
// the first keeps the plain c-api, the second a path-tagged name, and
// each resolves back to its own session — by the directory tmux reports
// — including after the other one ends.
func TestIntegration_SameBasenameProjects(t *testing.T) {
	ctx := isolatedServer(t)
	base, err := os.MkdirTemp("/tmp", "ccmp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	first := filepath.Join(base, "Projects", "api")
	second := filepath.Join(base, "work", "api")
	for _, d := range []string{first, second} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	create := func(dir string) string {
		t.Helper()
		name, running, err := ResolveProjectSession(ctx, dir)
		if err != nil {
			t.Fatal(err)
		}
		if running {
			t.Fatalf("%s: %s reported running before it was created", dir, name)
		}
		if err := New(ctx, name, dir, "sleep 300"); err != nil {
			t.Fatalf("create %s in %s: %v", name, dir, err)
		}
		return name
	}
	resolve := func(dir string) (string, bool) {
		t.Helper()
		name, running, err := ResolveProjectSession(ctx, dir)
		if err != nil {
			t.Fatal(err)
		}
		return name, running
	}

	if got := create(first); got != "c-api" {
		t.Fatalf("first project's session = %q, want c-api", got)
	}
	tagged := create(second)
	if tagged != PathTaggedSessionName(second) {
		t.Fatalf("second project's session = %q, want %q", tagged, PathTaggedSessionName(second))
	}

	// Each project resolves to its own running session.
	if name, running := resolve(first); name != "c-api" || !running {
		t.Errorf("first project resolves to %q (running %v), want c-api", name, running)
	}
	if name, running := resolve(second); name != tagged || !running {
		t.Errorf("second project resolves to %q (running %v), want %s", name, running, tagged)
	}
	sessions, err := List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sessions {
		want := map[string]string{"c-api": first, tagged: second}[s.Name]
		if want == "" {
			t.Errorf("unexpected session %q", s.Name)
		} else if !SamePath(s.Path, want) {
			t.Errorf("session %s runs in %s, want %s", s.Name, s.Path, want)
		}
	}

	// The second project keeps its session after the first one's ends;
	// the first project gets its plain name back.
	if err := Kill(ctx, "c-api"); err != nil {
		t.Fatal(err)
	}
	if name, running := resolve(second); name != tagged || !running {
		t.Errorf("after c-api ended, second project resolves to %q (running %v), want %s", name, running, tagged)
	}
	if name, running := resolve(first); name != "c-api" || running {
		t.Errorf("after c-api ended, first project resolves to %q (running %v), want c-api to create", name, running)
	}
}
