// `ccmux shell` — the CLI parallel to the Sessions tab's `n` form.
// Spawns a bare tmux session (no project, no agent) on the local
// host or any reachable tailnet peer. Useful for ad-hoc work on the
// always-on Mac mini ("quick shell to check disk space") without
// remembering tmux session names.
package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/tmux"
)

const remoteShellAttachPath = "PATH=/opt/homebrew/bin:/usr/local/bin:/home/linuxbrew/.linuxbrew/bin:/snap/bin:$PATH"

// newShellCmd registers the `ccmux shell` subcommand. CLI parity
// with the TUI's Sessions-tab `n` form per CLAUDE.md's feature-
// surface policy. Same four knobs: name, path, host, agent.
func newShellCmd() *cobra.Command {
	var (
		name      string
		path      string
		host      string
		agentFlag string
	)
	c := &cobra.Command{
		Use:   "shell",
		Short: "Spawn a tmux session (agent or shell only) on local or any tailnet peer",
		Long: `Start a tmux session running an AI agent (claude / codex / antigravity / cursor / pi / grok)
or a bare shell. Equivalent to pressing 'n' in the Sessions tab.

Defaults:
  --name      auto-generated (c-shell-<runid>)
  --path      sessions.default_dir from config; falls back to $HOME
              on the daemon's host (NOT the client's)
  --host      local (use a tailnet peer name to spawn on that host)
  --agent     sessions.default_agent from config (claude unless overridden);
              pass "shell" for a no-agent $SHELL session`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runShellCmd(cmd.Context(), name, path, host, agentFlag)
		},
	}
	c.Flags().StringVar(&name, "name", "", "tmux session name; empty for auto")
	c.Flags().StringVar(&path, "path", "", "working directory; empty uses config default")
	c.Flags().StringVar(&host, "host", "", "remote tailnet peer; empty for local")
	c.Flags().StringVar(&agentFlag, "agent", "", `agent to launch: "claude" / "codex" / "antigravity" / "cursor" / "pi" / "grok" / "shell"; empty uses config default`)
	return c
}

func runShellCmd(ctx context.Context, name, path, host, agentFlag string) error {
	host = strings.TrimSpace(host)
	if host == "" || host == "local" {
		return runShellLocal(ctx, name, path, agentFlag)
	}
	return runShellRemote(ctx, name, path, host, agentFlag)
}

// runShellLocal goes through the local daemon. Same code path the
// TUI's local-spawn uses, behind the same /v1/sessions/bare endpoint,
// so behavior stays uniform.
func runShellLocal(ctx context.Context, name, path, agentFlag string) error {
	cli, err := daemon.LocalClient()
	if err != nil {
		return fmt.Errorf("connect to local ccmuxd: %w", err)
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	res, err := cli.NewBareSession(cctx, daemon.NewBareSessionRequest{
		Name:  name,
		Path:  path,
		Agent: agentFlag,
	})
	if err != nil {
		return fmt.Errorf("new bare session: %w", err)
	}
	// Exec into tmux attach — the foreground replaces us. After detach
	// the user lands back in their shell, not back in `ccmux shell`.
	return execTmuxAttach(res.Session)
}

// runShellRemote POSTs to the named peer's ccmuxd over the tailnet,
// then exec's into `ssh -t <host> -- tmux attach`. The host name is
// resolved against the user's configured hosts; if it's not there,
// we error out with a hint pointing at `ccmux host add` or the
// auto-discovery flow.
func runShellRemote(ctx context.Context, name, path, host, agentFlag string) error {
	cfg, _ := config.Load()
	var hostCfg config.Host
	found := false
	for _, h := range cfg.Hosts {
		if h.Name == host {
			hostCfg = h
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("no host named %q in ~/.config/ccmux/config.toml; configure it with `ccmux host add` or attach via the TUI's auto-discovered list", host)
	}
	addr := fmt.Sprintf("%s:%d", hostCfg.Address, defaultPort(hostCfg.Port))
	cli := daemon.RemoteClient(addr)
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	res, err := cli.NewBareSession(cctx, daemon.NewBareSessionRequest{
		Name:  name,
		Path:  path,
		Agent: agentFlag,
	})
	if err != nil {
		return fmt.Errorf("new bare session on %s: %w", host, err)
	}
	// ssh -t <host> "tmux attach …" with the same PATH prepend the
	// TUI uses for cross-platform tmux discovery. Duplicating here
	// rather than reaching into internal/tui — that package is
	// gigantic and the CLI shouldn't drag it in.
	tmuxAttach := remoteShellTmuxAttach(res.Session)
	c := exec.Command("ssh", shellSSHArgs(hostCfg, tmuxAttach)...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

// remoteShellTmuxAttach builds the remote command for `ccmux shell --host`.
// The session was just created, so attach in mirror mode and preserve any
// other tmux clients on that remote server.
func remoteShellTmuxAttach(session string) string {
	return fmt.Sprintf(
		`%s tmux attach-session -t %s`,
		remoteShellAttachPath,
		shellQuote(session),
	)
}

// shellAttachCmd builds the foreground tmux attach for a freshly-created
// `ccmux shell` local session. Fresh-session attach preserves other clients.
func shellAttachCmd(name string) *exec.Cmd {
	return tmux.AttachCmd(name, false)
}

// execTmuxAttach is the foreground replacement for "tmux attach -t <name>".
// We use exec.Command + Run rather than syscall.Exec so deferred cleanups
// in the parent run; for a one-shot CLI subcommand the difference doesn't
// matter.
func execTmuxAttach(name string) error {
	c := shellAttachCmd(name)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

func defaultPort(p int) int {
	if p == 0 {
		return 7474
	}
	return p
}

// shellSSHArgs builds the `ssh` argv (minus the leading "ssh") for
// `ccmux shell --host`, honoring the host's configured SSH user and
// port. Pulled out of runShellRemote so the user/port handling is
// unit-testable without a live ssh. The bug it fixes: the old inline
// form used the bare address with no user@ qualifier and no -p, so a
// host with a non-local username or a non-22 sshd port failed auth /
// connected to the wrong port — while the TUI attach for the same
// host worked.
func shellSSHArgs(h config.Host, remoteCmd string) []string {
	dial := h.Address
	if h.User != "" {
		dial = h.User + "@" + h.Address
	}
	args := []string{"-t"}
	if p := h.EffectiveSSHPort(); p != 0 && p != 22 {
		// Non-default sshd port (the ISP-blocked-22 case ccmux
		// supports); 0/22 stay on the bare form.
		args = append(args, "-p", strconv.Itoa(p))
	}
	return append(args, dial, remoteCmd)
}

// shellQuote wraps `s` in single quotes, escaping any embedded
// single quotes via close/escape/reopen ('foo'\”bar'). Duplicate of
// the helper in internal/tui (kept local so the CLI doesn't drag
// the TUI package in).
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
