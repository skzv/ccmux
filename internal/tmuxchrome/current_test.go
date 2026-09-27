package tmuxchrome

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestCurrentSession(t *testing.T) {
	origEnv, origRun := envLookup, currentSessionRun
	t.Cleanup(func() { envLookup, currentSessionRun = origEnv, origRun })

	var gotArgs []string
	currentSessionRun = func(_ context.Context, args []string) ([]byte, error) {
		gotArgs = args
		return []byte("c-ccmux\n"), nil
	}

	// Outside tmux: no lookup at all.
	envLookup = func(string) string { return "" }
	if got := CurrentSession(context.Background()); got != "" || gotArgs != nil {
		t.Fatalf("outside tmux: CurrentSession = %q (ran %v), want \"\" without running tmux", got, gotArgs)
	}

	// Inside tmux: the lookup targets this process's own pane.
	envLookup = func(name string) string {
		switch name {
		case "TMUX":
			return "/tmp/tmux-501/default,123,4"
		case "TMUX_PANE":
			return "%7"
		}
		return ""
	}
	if got := CurrentSession(context.Background()); got != "c-ccmux" {
		t.Errorf("CurrentSession = %q, want c-ccmux", got)
	}
	want := []string{"display-message", "-p", "-t", "%7", "#{session_name}"}
	if !reflect.DeepEqual(gotArgs, want) {
		t.Errorf("tmux args = %q, want %q", gotArgs, want)
	}

	// tmux failing is "don't know", not an error for the caller.
	currentSessionRun = func(context.Context, []string) ([]byte, error) { return nil, errors.New("no server") }
	if got := CurrentSession(context.Background()); got != "" {
		t.Errorf("CurrentSession on tmux error = %q, want \"\"", got)
	}
}
