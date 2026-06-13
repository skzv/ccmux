package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/skzv/ccmux/internal/setupwizard"
)

// newMCPCmd returns the `ccmux mcp` command group. Subcommands wire
// the ccmux-mcp server into MCP-aware coding agents (Claude Code,
// for now). Mirrors the setup-wizard step so users who don't want
// to walk the whole wizard have a direct path.
//
// Why a command group instead of a single command: this WILL grow
// to cover Codex/Cursor/Antigravity once their MCP config formats
// settle, and `ccmux mcp register --client codex` reads better than
// stacking flags on a flat command.
func newMCPCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "mcp",
		Short: "Wire ccmux-mcp into MCP-aware coding agents",
		Long: `ccmux ships an MCP server (ccmux-mcp) that lets coding agents see and act
on every ccmux session, project, conversation, and tailnet peer through the
Model Context Protocol.

These subcommands wire it into MCP-aware clients without you hand-editing
their settings file. Each one's idempotent — re-run them freely.`,
	}
	c.AddCommand(
		newMCPRegisterCmd(),
		newMCPStatusCmd(),
	)
	return c
}

// newMCPRegisterCmd is `ccmux mcp register [--allow-mutate]`. The
// non-wizard path to adding the ccmux entry to ~/.claude/settings.json.
func newMCPRegisterCmd() *cobra.Command {
	var allowMutate bool
	c := &cobra.Command{
		Use:   "register",
		Short: "Register ccmux-mcp into Claude Code's settings.json",
		Long: `Adds a 'ccmux' entry to ~/.claude/settings.json under mcpServers, pointed
at the ccmux-mcp binary. Existing MCP servers are preserved; a timestamped
backup is written to ~/.claude/backups/ before the change.

Pass --allow-mutate to expose the mutating tools (spawn_session, send_keys,
kill_session). Read-only by default — safe to leave it on, the agent can
only see, not type.

Idempotent: re-running prints the current registration mode without
changing the file. To switch modes, unregister first or hand-edit the
'args' array in settings.json.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return setupwizard.RegisterMCPForCLI(context.Background(), os.Stdout, allowMutate)
		},
	}
	c.Flags().BoolVar(&allowMutate, "allow-mutate", false,
		"expose mutating tools (spawn_session, send_keys, kill_session). Off by default.")
	return c
}

// newMCPStatusCmd is `ccmux mcp status`. Quick check for "is ccmux
// wired into Claude Code, and in what mode?"
func newMCPStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Report whether ccmux-mcp is registered with Claude Code",
		RunE: func(_ *cobra.Command, _ []string) error {
			mode, ok, err := setupwizard.MCPStatus()
			if err != nil {
				return err
			}
			if !ok {
				fmt.Println("✗ ccmux-mcp is NOT registered in ~/.claude/settings.json")
				fmt.Println("  register it with: ccmux mcp register [--allow-mutate]")
				return nil
			}
			fmt.Printf("✓ ccmux-mcp is registered (%s)\n", mode)
			return nil
		},
	}
}
