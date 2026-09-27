//go:build integration

package main

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/tmux"
)

// TestTmuxDollarNamesAreSessionIDs — pins, on a real tmux, why
// badSessionName refuses a leading `$`: even the exact `=name:` target
// resolves "$<n>" as a session ID (so a kill by that "name" hits another
// session) and never finds a session actually named "$x".
func TestTmuxDollarNamesAreSessionIDs(t *testing.T) {
	dir := pollSandbox(t)
	mustTmux(t, "new-session", "-d", "-s", "c-first", "-c", dir, "sleep 300")
	ctx := context.Background()
	id, err := exec.Command("tmux", "display-message", "-p", "-t", "=c-first:", "#{session_id}").Output()
	if err != nil {
		t.Fatal(err)
	}
	byID := strings.TrimSpace(string(id)) // e.g. "$0"; no session has that name
	if ok, err := tmux.Has(ctx, byID); err != nil || !ok {
		t.Skipf("this tmux doesn't resolve %q as a session ID in an exact target; nothing to pin", byID)
	}
	if !badSessionName(byID) {
		t.Errorf("tmux resolves %q to another session, but badSessionName allows it", byID)
	}
	_ = tmux.New(ctx, "$named", dir, "sleep 300")
	if ok, _ := tmux.Has(ctx, "$named"); ok {
		t.Skip("this tmux finds $-prefixed names by name")
	}
	if !badNewSessionName("$named") {
		t.Error(`tmux can't find a session created as "$named", but badNewSessionName allows it`)
	}
	// `@` and `%` look like window/pane IDs but tmux finds such session
	// names by name in an exact target, so they stay allowed.
	for _, name := range []string{"@1", "%1"} {
		if err := tmux.New(ctx, name, dir, "sleep 300"); err != nil {
			t.Fatal(err)
		}
		if ok, err := tmux.Has(ctx, name); err != nil || !ok {
			t.Errorf("session %q isn't found by name (has=%v, %v), but badSessionName allows it", name, ok, err)
		}
	}
}
