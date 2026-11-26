package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/tmux"
)

// newSendCmd: `ccmux send <session> <text...> [--host <name>] [--no-enter]` —
// sends a quick reply to a session's active agent pane without attaching.
func newSendCmd() *cobra.Command {
	var host string
	var noEnter bool
	c := &cobra.Command{
		Use:   "send <session> <text...>",
		Short: "Send a quick reply to a session without attaching",
		Long: `Send text directly into a session's agent pane (followed by Enter unless
--no-enter is set), without attaching to tmux.

With --host, routes through that configured host's ccmuxd over Tailscale.`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			session := args[0]
			if err := tmux.CheckTarget(session); err != nil {
				return fmt.Errorf("refusing to send to %q: %w", session, err)
			}
			text := strings.Join(args[1:], " ")
			if strings.TrimSpace(text) == "" {
				return fmt.Errorf("text cannot be empty")
			}

			if !isLocalHost(host) {
				cfg, err := config.Load()
				if err != nil {
					return fmt.Errorf("load config: %w", err)
				}
				rh, err := lookupHost(cfg, host)
				if err != nil {
					return err
				}
				ctx, cancel := context.WithTimeout(context.Background(), remoteOpTimeout)
				defer cancel()
				if err := rh.cli.SendKeys(ctx, session, text); err != nil {
					return rh.remoteErr("send keys", err)
				}
				if !noEnter {
					if err := rh.cli.SendKeys(ctx, session, "Enter"); err != nil {
						return rh.remoteErr("send Enter", err)
					}
				}
				fmt.Fprintf(c.OutOrStdout(), "sent to %s (%s)\n", session, rh.name)
				return nil
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if cli, lerr := daemon.LocalClient(); lerr == nil {
				if err := cli.SendKeys(ctx, session, text); err == nil {
					if !noEnter {
						if err := cli.SendKeys(ctx, session, "Enter"); err != nil {
							return err
						}
					}
					fmt.Fprintf(c.OutOrStdout(), "sent to %s\n", session)
					return nil
				}
			}
			// Fallback to direct tmux when ccmuxd isn't running locally.
			if err := tmux.SendText(ctx, session, text); err != nil {
				return err
			}
			if !noEnter {
				if err := tmux.SendKeys(ctx, session, "Enter"); err != nil {
					return err
				}
			}
			fmt.Fprintf(c.OutOrStdout(), "sent to %s\n", session)
			return nil
		},
	}
	c.Flags().StringVar(&host, "host", "", hostFlagUsage)
	c.Flags().BoolVar(&noEnter, "no-enter", false, "do not press Enter after sending text")
	return c
}
