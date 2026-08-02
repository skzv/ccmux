package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/agentcatalog"
	"github.com/skzv/ccmux/internal/daemon"
)

// newAgentsCommandsCmd: `ccmux agents commands [--agent id] [--session name] [--json]`
//
// Prints the command catalog the Telegram bridge surfaces for an agent —
// the same data, reachable from the CLI for discovery and scripting. For
// a Claude agent this includes the host's own ~/.claude commands/skills.
// With --session, the catalog is resolved by the daemon for that live
// session (so it reflects whichever agent the session runs); otherwise
// it's resolved locally for --agent (default claude).
func newAgentsCommandsCmd() *cobra.Command {
	var (
		agentFlag   string
		sessionFlag string
		asJSON      bool
	)
	c := &cobra.Command{
		Use:   "commands",
		Short: "List an agent's command catalog (built-ins + your custom commands)",
		Long: `Print the slash-commands an agent CLI understands — the catalog the Telegram
bridge offers as autocomplete. For Claude this merges the built-in commands
with your own ~/.claude/commands and skills.

By default it lists the catalog for --agent (claude unless overridden),
resolved on this machine. Pass --session <name> to ask the daemon for the
catalog of a live session's actual agent.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			var resp daemon.AgentCommandsResponse
			if sessionFlag != "" {
				cli, err := daemon.LocalClient()
				if err != nil {
					return err
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				resp, err = cli.AgentCommands(ctx, sessionFlag)
				if err != nil {
					return fmt.Errorf("get agent commands for %q: %w", sessionFlag, err)
				}
			} else {
				id := agentFlag
				if id == "" {
					id = "claude"
				}
				aid, ok := agent.ParseID(id)
				if !ok {
					return fmt.Errorf("unknown agent %q", id)
				}
				resp.Agent = string(aid)
				for _, cmd := range agentcatalog.Resolve(agent.ByID(aid)) {
					resp.Commands = append(resp.Commands, daemon.AgentCommand{
						Name:        cmd.Name,
						Description: cmd.Description,
						TakesArg:    cmd.TakesArg,
						Source:      cmd.Source,
					})
				}
			}

			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(resp)
			}

			if len(resp.Commands) == 0 {
				fmt.Fprintf(out, "%s is prompt-only — no slash-command catalog.\n", resp.Agent)
				return nil
			}
			fmt.Fprintf(out, "Commands for %s:\n", resp.Agent)
			tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
			for _, item := range resp.Commands {
				arg := ""
				if item.TakesArg {
					arg = " <arg>"
				}
				fmt.Fprintf(tw, "  %s%s\t%s\t%s\n", item.Name, arg, item.Source, item.Description)
			}
			return tw.Flush()
		},
	}
	c.Flags().StringVar(&agentFlag, "agent", "", "agent id (claude, codex, …); default claude")
	c.Flags().StringVar(&sessionFlag, "session", "", "resolve the catalog for a live session via the daemon")
	c.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	return c
}
