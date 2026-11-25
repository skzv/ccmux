package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/tmux"
)

// newPreviewCmd: `ccmux preview <session> [--lines N] [--host <name>] [--json]`
// — CLI mirror of the Sessions screen `p` preview pane and MCP read_pane.
func newPreviewCmd() *cobra.Command {
	var (
		lines  int
		host   string
		asJSON bool
	)
	c := &cobra.Command{
		Use:   "preview <session>",
		Short: "Print the trailing lines of a session's agent pane",
		Long: `Capture and print the trailing lines of a running session's agent pane
without attaching. Mirrors pressing 'p' on the Sessions screen and the
MCP read_pane tool.

Queries ccmuxd first (or the remote peer's ccmuxd with --host), falling
back to local tmux capture-pane when the local daemon is offline.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := tmux.CheckTarget(name); err != nil {
				return fmt.Errorf("invalid session %q: %w", name, err)
			}
			if lines <= 0 {
				lines = 24
			}
			if lines > 500 {
				lines = 500
			}
			var (
				resp daemon.PreviewResponse
				err  error
			)
			if !isLocalHost(host) {
				cfg, lerr := config.Load()
				if lerr != nil {
					return fmt.Errorf("load config: %w", lerr)
				}
				rh, lerr := lookupHost(cfg, host)
				if lerr != nil {
					return lerr
				}
				ctx, cancel := context.WithTimeout(context.Background(), remoteOpTimeout)
				defer cancel()
				resp, err = rh.cli.Preview(ctx, name, lines)
				if err != nil {
					return rh.remoteErr("preview "+name, err)
				}
			} else {
				gotFromDaemon := false
				if cli, cerr := daemon.LocalClient(); cerr == nil {
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					p, perr := cli.Preview(ctx, name, lines)
					cancel()
					if perr == nil {
						resp = p
						gotFromDaemon = true
					} else if daemon.StatusCode(perr) != 0 {
						return perr
					}
				}
				if !gotFromDaemon {
					tctx, tcancel := context.WithTimeout(context.Background(), listTmuxTimeout)
					out, terr := tmux.CapturePane(tctx, name, lines)
					tcancel()
					if terr != nil {
						return fmt.Errorf("capture pane %q: %w", name, terr)
					}
					resp = daemon.PreviewResponse{Lines: lines, Content: trimTrailingPreviewLines(out, lines)}
				}
			}
			if asJSON {
				resp.Content = safeText(resp.Content)
				return json.NewEncoder(cmd.OutOrStdout()).Encode(resp)
			}
			fmt.Fprint(cmd.OutOrStdout(), safeText(resp.Content))
			return nil
		},
	}
	c.Flags().IntVarP(&lines, "lines", "n", 24, "number of trailing lines to capture (1..500)")
	c.Flags().StringVar(&host, "host", "", hostFlagUsage)
	c.Flags().BoolVar(&asJSON, "json", false, "output JSON ({lines, content})")
	return c
}

func trimTrailingPreviewLines(s string, n int) string {
	rows := strings.Split(s, "\n")
	end := len(rows)
	for end > 0 && strings.TrimSpace(rows[end-1]) == "" {
		end--
	}
	if end == 0 {
		return ""
	}
	start := max(0, end-n)
	return strings.Join(rows[start:end], "\n") + "\n"
}
