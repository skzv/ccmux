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
		newMCPUnregisterCmd(),
		newMCPStatusCmd(),
	)
	return c
}

// unregisterMCP removes ccmux-mcp from Claude Code. A seam so tests of
// `ccmux uninstall`'s step never touch a real Claude config.
var unregisterMCP = setupwizard.UnregisterMCP

// newMCPUnregisterCmd is `ccmux mcp unregister`, the inverse of
// register. `ccmux uninstall` runs the same step.
func newMCPUnregisterCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unregister",
		Short: "Remove the ccmux-mcp MCP server from Claude Code",
		Long: `Removes the user-scope 'ccmux' MCP server from Claude Code — the equivalent of

  claude mcp remove --scope user ccmux

which is what runs when the claude CLI is on PATH. Without it, ccmux deletes
the entry from the top-level mcpServers object in ~/.claude.json directly
(every other server and setting is preserved; a timestamped backup is
written to ~/.claude/backups/ first).

Only an entry that runs ccmux-mcp is removed. Idempotent: with nothing
registered it changes nothing. ` + "`ccmux uninstall`" + ` does this for you.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			summary, err := unregisterMCP(context.Background())
			if err != nil {
				return fmt.Errorf("unregister ccmux-mcp from Claude Code: %w", err)
			}
			fmt.Println("✓ " + summary)
			return nil
		},
	}
}

// newMCPRegisterCmd is `ccmux mcp register [--allow-mutate]`. The
// non-wizard path to registering ccmux-mcp as a user-scope MCP server
// in Claude Code (~/.claude.json).
func newMCPRegisterCmd() *cobra.Command {
	var allowMutate bool
	c := &cobra.Command{
		Use:   "register",
		Short: "Register ccmux-mcp as a user-scope MCP server in Claude Code",
		Long: `Registers a user-scope 'ccmux' MCP server in Claude Code, pointed at the
ccmux-mcp binary — the equivalent of

  claude mcp add-json --scope user ccmux '{"type":"stdio","command":"ccmux-mcp","args":[]}'

which is what runs when the claude CLI is on PATH. Without it, ccmux edits
the top-level mcpServers object in ~/.claude.json directly (existing
servers and settings are preserved; a timestamped backup is written to
~/.claude/backups/ first). A stale entry older ccmux versions left in
~/.claude/settings.json — which Claude Code never reads — is removed.

Pass --allow-mutate to expose the mutating tools (spawn_session, send_keys,
kill_session). Read-only by default — safe to leave it on, the agent can
only see, not type.

Idempotent: re-running with the same mode changes nothing; running with
the other mode replaces the entry.`,
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
				fmt.Printf("✗ ccmux-mcp is NOT registered as a user-scope MCP server in %s\n", setupwizard.MCPUserConfigPath())
				fmt.Println("  register it with: ccmux mcp register [--allow-mutate]")
				return nil
			}
			fmt.Printf("✓ ccmux-mcp is registered in %s (%s)\n", setupwizard.MCPUserConfigPath(), mode)
			return nil
		},
	}
}
