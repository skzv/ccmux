package tmuxchrome

import (
	"context"
	"os/exec"
	"strings"
)

// CurrentSession returns the name of the tmux session the calling
// process runs in, or "" when it isn't inside tmux or tmux can't say.
// $TMUX_PANE pins the lookup to this process's own pane: without -t,
// display-message answers for the most recently active client, which
// may be a different session.
func CurrentSession(ctx context.Context) string {
	if !InTmux() {
		return ""
	}
	args := []string{"display-message", "-p"}
	if pane := strings.TrimSpace(envLookup("TMUX_PANE")); pane != "" {
		args = append(args, "-t", pane)
	}
	args = append(args, "#{session_name}")
	out, err := currentSessionRun(ctx, args)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// currentSessionRun runs tmux for CurrentSession; a seam for tests.
var currentSessionRun = func(ctx context.Context, args []string) ([]byte, error) {
	return exec.CommandContext(ctx, "tmux", args...).Output()
}
