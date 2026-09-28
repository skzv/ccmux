package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"text/tabwriter"
	"time"

	"github.com/skzv/ccmux/internal/daemon"
	"github.com/spf13/cobra"
)

func newUsageCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use: "usage", Short: "Show local daemon usage for all coding agents", Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			client, err := daemon.LocalClient()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(c.Context(), usageTimeout)
			defer cancel()
			data, err := client.Usage(ctx)
			if err != nil {
				return usageError(err)
			}
			return writeUsage(c.OutOrStdout(), data, asJSON)
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "output the daemon usage JSON")
	return c
}

// usageTimeout bounds `ccmux usage`'s daemon call: the transcript walk
// behind it can legitimately take a while on a big history.
const usageTimeout = 15 * time.Second

// usageError explains a failed usage read. Every failure used to say
// "(start ccmuxd first)" — including a daemon that was running but hung,
// where starting it is exactly the wrong advice.
func usageError(err error) error {
	var opErr *net.OpError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("ccmuxd didn't respond within %v — it may be wedged; try `ccmux daemon restart`: %w", usageTimeout, err)
	case errors.As(err, &opErr) && opErr.Op == "dial":
		return fmt.Errorf("can't reach ccmuxd — start it with `ccmux daemon start`: %w", err)
	default:
		return fmt.Errorf("read usage from ccmuxd: %w", err)
	}
}

func writeUsage(w io.Writer, data daemon.AgentUsage, asJSON bool) error {
	if asJSON {
		return json.NewEncoder(w).Encode(data)
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "AGENT\tPROMPTS\tINPUT\tOUTPUT\tCACHED INPUT\tREASONING\tEST. COST")
	rows := append([]daemon.OtherUsage{{Agent: "claude", Usage: data.Claude}, {Agent: "codex", Usage: data.Codex}, {Agent: "antigravity", Usage: data.Antigravity}}, data.Others...)
	for _, row := range rows {
		s := row.Usage
		if !s.HasData {
			continue
		}
		cost := "unavailable"
		if (s.CostAvailable != nil && *s.CostAvailable) || (s.CostAvailable == nil && s.EstimatedCost > 0) {
			cost = fmt.Sprintf("$%.2f", s.EstimatedCost)
		}
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%d\t%s\n", safeField(row.Agent), s.Prompts, s.InputTokens, s.OutputTokens, s.CachedInputTokens, s.ReasoningTokens, cost)
	}
	return tw.Flush()
}
