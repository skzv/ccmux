package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/skzv/ccmux/internal/claudemodels"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
)

// newAgentsCmd is the parent for agent-level commands. Today it
// holds the two model-management subcommands (list discovered
// models, pin a default for ccmux-launched sessions); future
// per-agent toggles (effort, thinking) will hang off this same
// node so the user has one consistent surface.
func newAgentsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "agents",
		Short: "Configure agents and pick a default model",
		Long: `Manage agent-level settings ccmux applies when launching sessions.

The ccmuxd daemon discovers the Claude model catalog from Anthropic's
Models API every 24h (when ANTHROPIC_API_KEY is set on the daemon's
environment) and falls back to a curated in-binary list otherwise.
Use the subcommands below to inspect the catalog and pin a default
model that ccmux passes as ANTHROPIC_MODEL on every Claude session
it launches.`,
	}
	c.AddCommand(newAgentsModelsCmd())
	c.AddCommand(newAgentsSetDefaultModelCmd())
	c.AddCommand(newAgentsCommandsCmd())
	return c
}

// newAgentsModelsCmd: `ccmux agents models [--refresh] [--json]`
//
// Lists the catalog the daemon discovered. Tagged rows: `[default]`
// next to the current ccmux pin, `[api]` vs `[fallback]` source.
// --refresh forces a synchronous re-fetch on the daemon side; useful
// the day Anthropic ships a new model and you don't want to wait for
// the next 24h tick.
func newAgentsModelsCmd() *cobra.Command {
	var (
		refresh bool
		asJSON  bool
	)
	c := &cobra.Command{
		Use:   "models",
		Short: "List the Claude models the daemon discovered (or fallback list)",
		Long: `Print every model the daemon currently knows about.

When ANTHROPIC_API_KEY is set on the daemon's environment, the catalog
comes from a live call to GET /v1/models on api.anthropic.com,
refreshed every 24h and merged with a curated fallback list. Without
a key, the catalog is the curated list alone — still useful, and
auto-grows with every ccmux release.

Pass --refresh to force a re-fetch right now instead of waiting for
the next 24h tick. Pass --json for machine-parsable output (the same
shape /v1/models returns).`,
		RunE: func(_ *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			// Try the daemon first — that's the source of truth for
			// the live catalog. If ccmuxd isn't running, fall back to
			// reading the cache file directly and merging with the
			// curated list. Same shape either way; we just lose the
			// "--refresh" effect if the daemon isn't there.
			var cat claudemodels.Catalog
			if cli, err := daemon.LocalClient(); err == nil {
				if c, err := cli.Models(ctx, refresh); err == nil {
					cat = c
				}
			}
			if len(cat.Models) == 0 {
				if path, err := claudemodels.CachePath(); err == nil {
					if c, err := (claudemodels.Cache{Path: path}).Read(); err == nil {
						cat = c
					}
				}
				cat.Models = claudemodels.Merge(cat.Models, claudemodels.Fallback())
				if cat.Source == "" {
					cat.Source = claudemodels.SourceFallback
				}
				claudemodels.Sort(cat.Models)
			}

			// Read the user's pinned default so we can tag it.
			currentDefault := ""
			if cfg, err := config.Load(); err == nil {
				currentDefault = cfg.Claude.DefaultModel
			}

			if asJSON {
				return json.NewEncoder(os.Stdout).Encode(cat)
			}

			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tDISPLAY NAME\tCONTEXT\tMAX OUT\tSOURCE\tDEFAULT")
			// Stable order: aliases first (none in API today, but
			// fallback may add some), then opus → sonnet → haiku.
			// Sort already enforces this; just iterate.
			sorted := append([]claudemodels.Model(nil), cat.Models...)
			sort.SliceStable(sorted, func(i, j int) bool { return false }) // preserve existing order
			for _, m := range sorted {
				marker := ""
				if m.ID == currentDefault {
					marker = "*"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
					m.ID, m.DisplayName, formatTokens(m.MaxInput), formatTokens(m.MaxOutput), m.Source, marker)
			}
			if err := tw.Flush(); err != nil {
				return err
			}

			// Footer: a one-line hint pointing at the sibling
			// subcommand. Saves the user a `--help` round-trip when
			// they came to "list models" but wanted "set the default".
			fmt.Fprintln(os.Stdout)
			if currentDefault == "" {
				fmt.Fprintln(os.Stdout, "No model pinned. Use `ccmux agents set-default-model <id>` to pin one.")
			} else {
				fmt.Fprintf(os.Stdout, "Current pin: %s. Use `ccmux agents set-default-model <id>` to change (empty to clear).\n", currentDefault)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&refresh, "refresh", false,
		"force the daemon to re-fetch the catalog from the Anthropic API")
	c.Flags().BoolVar(&asJSON, "json", false,
		"output the raw catalog as JSON (same shape as /v1/models)")
	return c
}

// newAgentsSetDefaultModelCmd: `ccmux agents set-default-model <id>`
//
// Writes the picked model to ~/.config/ccmux/config.toml's [claude]
// default_model. Empty string clears the pin (the picker's
// "(no pin)" sentinel). Mirrors the TUI's M-keybinding picker so
// scripts and muscle-memory users have parity.
func newAgentsSetDefaultModelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set-default-model [model-id]",
		Short: "Pin a default Claude model for ccmux-launched sessions",
		Long: `Write [claude] default_model in ~/.config/ccmux/config.toml.

ccmuxd passes the value as ANTHROPIC_MODEL when it launches a Claude
session, so the pick survives the claude → claude → shell fallback
chain. Pass an alias ("opus" / "sonnet" / "haiku" / "opusplan") for
auto-tracking Anthropic's current bindings, or a concrete model ID
("claude-opus-4-8") to pin a specific version. Omit the argument to
clear the pin.

The daemon re-reads the config on each session launch, so the change
takes effect immediately — no daemon restart needed.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			model := ""
			if len(args) == 1 {
				model = strings.TrimSpace(args[0])
			}
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			cfg.Claude.DefaultModel = model
			if err := config.Save(cfg); err != nil {
				return fmt.Errorf("save config: %w", err)
			}
			if model == "" {
				fmt.Fprintln(os.Stdout, "ccmux default model: cleared (inherits Claude Code's setting).")
			} else {
				fmt.Fprintf(os.Stdout, "ccmux default model set to %q. Takes effect on the next session you launch from ccmux.\n", model)
			}
			return nil
		},
	}
}

// formatTokens renders a token count compactly for the table:
// 1000000 → "1M", 200000 → "200K". Empty for zero so unknown values
// don't show as a misleading "0".
func formatTokens(n int) string {
	switch {
	case n == 0:
		return ""
	case n >= 1_000_000:
		return fmt.Sprintf("%dM", n/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%dK", n/1_000)
	}
	return fmt.Sprintf("%d", n)
}
