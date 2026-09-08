package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
			ctx, cancel := context.WithTimeout(c.Context(), 15*time.Second)
			defer cancel()
			data, err := client.Usage(ctx)
			if err != nil {
				return fmt.Errorf("read usage (start ccmuxd first): %w", err)
			}
			return writeUsage(c.OutOrStdout(), data, asJSON)
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "output the daemon usage JSON")
	return c
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
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%d\t%s\n", row.Agent, s.Prompts, s.InputTokens, s.OutputTokens, s.CachedInputTokens, s.ReasoningTokens, cost)
	}
	return tw.Flush()
}
