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
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("ccmuxd didn't respond within %v — it may be wedged; try `ccmux daemon restart`: %w", usageTimeout, err)
	}
	if down := daemonDownErr(err); down != nil {
		return down
	}
	return fmt.Errorf("read usage from ccmuxd: %w", err)
}

// daemonDownErr is how every command reports a call to the local ccmuxd
// that couldn't even connect — no socket, or a stale one nothing listens
// on ("connection refused"): the error with the start hint. It returns
// nil for any other failure (the daemon answered with an error, or
// didn't answer in time), which the caller reports its own way. `notes`,
// `pair` and `shell` used to print the raw dial error with no hint.
func daemonDownErr(err error) error {
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return fmt.Errorf("can't reach ccmuxd — start it with `ccmux daemon start`: %w", err)
	}
	return nil
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
	if err := tw.Flush(); err != nil {
		return err
	}
	return writeClaudeBlock(w, data.ClaudeBlock, time.Now())
}

// writeClaudeBlock prints Claude's current 5-hour session block, the
// one the dashboard's quota bar and "resets in" line show. The table
// above covers a rolling window instead, so its Claude row differs.
// Nothing is printed for a daemon too old to report the block.
func writeClaudeBlock(w io.Writer, b *daemon.ClaudeBlock, now time.Time) error {
	if b == nil {
		return nil
	}
	if !b.Active {
		_, err := fmt.Fprintln(w, "\nCLAUDE 5-HOUR BLOCK  none running; the next message starts one")
		return err
	}
	fmt.Fprintf(w, "\nCLAUDE 5-HOUR BLOCK  started %s, resets %s (%s)\n",
		b.Start.Local().Format("15:04"), b.ResetAt.Local().Format("15:04"), untilReset(b.ResetAt.Sub(now)))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PROMPTS\tMESSAGES\tINPUT\tOUTPUT\tCACHE WRITE\tCACHE READ\tEST. COST")
	fmt.Fprintf(tw, "%d\t%d\t%d\t%d\t%d\t%d\t$%.2f\n", b.Prompts, b.Messages, b.InputTokens, b.OutputTokens,
		b.CacheCreationTokens, b.CacheReadTokens, b.EstimatedCost)
	return tw.Flush()
}

// untilReset says how long until a block resets: "in 3h05m", "in 42m",
// "in <1m", or "resetting now" once it is due.
func untilReset(d time.Duration) string {
	if d <= 0 {
		return "resetting now"
	}
	if d < time.Minute {
		return "in <1m"
	}
	d = d.Round(time.Minute)
	if h, m := int(d/time.Hour), int(d%time.Hour/time.Minute); h > 0 {
		return fmt.Sprintf("in %dh%02dm", h, m)
	}
	return fmt.Sprintf("in %dm", int(d/time.Minute))
}
