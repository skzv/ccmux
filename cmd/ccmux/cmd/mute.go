package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/tmux"
)

// newMuteCmd: `ccmux mute <session> [--off] [--host <name>]` —
// silences or re-enables terminal bell and push notifications for a session.
func newMuteCmd() *cobra.Command {
	var host string
	var off bool
	c := &cobra.Command{
		Use:   "mute <session>",
		Short: "Mute or unmute notifications for a session",
		Long: `Silence terminal bells and push notifications (Moshi/APNs/FCM) for a
session while keeping its dashboard state live. Stored on the tmux session
(@ccmux_muted) so it survives daemon restarts.

Pass --off to unmute. With --host, updates the session on that configured host.`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			session := args[0]
			if err := tmux.CheckTarget(session); err != nil {
				return fmt.Errorf("refusing to mute %q: %w", session, err)
			}
			muted := !off
			verb := "muted"
			if !muted {
				verb = "unmuted"
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
				if err := rh.cli.SetMuted(ctx, session, muted); err != nil {
					return rh.remoteErr("set mute", err)
				}
				fmt.Fprintf(c.OutOrStdout(), "%s %s (%s)\n", verb, session, rh.name)
				return nil
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if cli, lerr := daemon.LocalClient(); lerr == nil {
				if err := cli.SetMuted(ctx, session, muted); err == nil {
					fmt.Fprintf(c.OutOrStdout(), "%s %s\n", verb, session)
					return nil
				}
			}
			if err := tmux.SetSessionMuted(ctx, session, muted); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "%s %s\n", verb, session)
			return nil
		},
	}
	c.Flags().StringVar(&host, "host", "", hostFlagUsage)
	c.Flags().BoolVar(&off, "off", false, "unmute notifications for the session")
	return c
}
