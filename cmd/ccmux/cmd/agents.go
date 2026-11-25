package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/skzv/ccmux/internal/antigravityconfig"
	"github.com/skzv/ccmux/internal/claudeconfig"
	"github.com/skzv/ccmux/internal/claudemodels"
	"github.com/skzv/ccmux/internal/codexconfig"
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

The ccmuxd daemon keeps a catalog of Claude models. It asks your claude
CLI first (works for subscription and API users alike), then Anthropic's
Models API when ANTHROPIC_API_KEY is set in the daemon's environment, and
always merges in a curated list that ships with ccmux. The catalog is
refreshed weekly; ` + "`ccmux agents models --refresh`" + ` refreshes it now (at
most once every 10 minutes). Use the subcommands below to inspect the
catalog and pin a default model that ccmux passes as ANTHROPIC_MODEL on
every Claude session it launches.`,
	}
	c.AddCommand(newAgentsModelsCmd())
	c.AddCommand(newAgentsSetDefaultModelCmd())
	c.AddCommand(newAgentsConfigCmd())
	return c
}

type agentConfigSnapshot struct {
	Agent          string `json:"agent"`
	Model          string `json:"model,omitempty"`
	Effort         string `json:"effort,omitempty"`
	AlwaysThinking *bool  `json:"always_thinking,omitempty"`
	Yolo           bool   `json:"yolo"`
}

// newAgentsConfigCmd: `ccmux agents config [claude|codex|antigravity]`
// and `ccmux agents config set <agent> <key> <value>` — CLI parity with
// the Agents screen toggles (effort, always-thinking, yolo, model).
func newAgentsConfigCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   "config [claude|codex|antigravity]",
		Short: "Inspect or update agent-level settings (effort, always-thinking, yolo, model)",
		Long: `Show or update native agent configuration files (~/.claude/settings.json,
~/.codex/config.toml, ~/.gemini/antigravity-cli/settings.json). Mirrors
the toggles on the TUI Agents tab.

Examples:
  ccmux agents config
  ccmux agents config claude --json
  ccmux agents config set claude effort high
  ccmux agents config set claude always-thinking true
  ccmux agents config set codex yolo true
  ccmux agents config set antigravity effort medium`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			targets := []string{"claude", "codex", "antigravity"}
			if len(args) == 1 {
				want := strings.ToLower(strings.TrimSpace(args[0]))
				switch want {
				case "claude", "codex", "antigravity":
					targets = []string{want}
				default:
					return fmt.Errorf("unsupported agent %q (want claude, codex, or antigravity)", args[0])
				}
			}
			snaps := make([]agentConfigSnapshot, 0, len(targets))
			for _, a := range targets {
				snaps = append(snaps, readAgentConfigSnapshot(a))
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(snaps)
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "AGENT\tMODEL\tEFFORT\tALWAYS THINKING\tYOLO")
			for _, s := range snaps {
				at := "-"
				if s.AlwaysThinking != nil {
					at = strconv.FormatBool(*s.AlwaysThinking)
				}
				model := s.Model
				if model == "" {
					model = "(default)"
				}
				effort := s.Effort
				if effort == "" {
					effort = "(default)"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%v\n",
					s.Agent, safeField(model), safeField(effort), at, s.Yolo)
			}
			return tw.Flush()
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "output agent settings as JSON")
	c.AddCommand(newAgentsConfigSetCmd())
	return c
}

func readAgentConfigSnapshot(name string) agentConfigSnapshot {
	switch name {
	case "claude":
		model, _ := claudeconfig.EffectiveModel()
		effort, _ := claudeconfig.EffectiveEffortLevel()
		yolo, _ := claudeconfig.EffectiveYoloMode()
		at := false
		if s, err := claudeconfig.ReadSettings(); err == nil && s != nil {
			at = s.AlwaysThinkingEnabled
		}
		return agentConfigSnapshot{
			Agent: "claude", Model: model, Effort: effort, AlwaysThinking: &at, Yolo: yolo,
		}
	case "codex":
		effort, _ := codexconfig.EffectiveEffortLevel()
		yolo, _ := codexconfig.EffectiveYoloMode()
		model := ""
		if s, err := codexconfig.ReadSettings(); err == nil && s != nil {
			model = s.Model
		}
		return agentConfigSnapshot{Agent: "codex", Model: model, Effort: effort, Yolo: yolo}
	case "antigravity":
		effort, _ := antigravityconfig.EffectiveEffortLevel()
		yolo, _ := antigravityconfig.EffectiveYoloMode()
		model := ""
		if s, err := antigravityconfig.ReadSettings(); err == nil && s != nil {
			model = s.Model
		}
		return agentConfigSnapshot{Agent: "antigravity", Model: model, Effort: effort, Yolo: yolo}
	}
	return agentConfigSnapshot{Agent: name}
}

func newAgentsConfigSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <claude|codex|antigravity> <key> <value>",
		Short: "Set an agent-level knob (model, effort, always-thinking, yolo)",
		Long: `Update a single setting in the agent's native config file (with automatic backup):

  claude:       model, effort, always-thinking (true/false), yolo (true/false)
  codex:        effort, yolo (true/false)
  antigravity:  effort, yolo (true/false)`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			ag := strings.ToLower(strings.TrimSpace(args[0]))
			key := strings.ToLower(strings.TrimSpace(args[1]))
			val := strings.TrimSpace(args[2])
			switch ag {
			case "claude":
				switch key {
				case "model":
					if val == "default" || val == "clear" {
						val = ""
					}
					if _, err := claudeconfig.SetModel(val); err != nil {
						return fmt.Errorf("set claude model: %w", err)
					}
				case "effort":
					if val == "default" || val == "clear" {
						val = ""
					}
					if !validClaudeEffort(val) {
						return fmt.Errorf("invalid claude effort %q (want %s)", val, claudeEffortList())
					}
					if _, err := claudeconfig.SetEffortLevel(val); err != nil {
						return fmt.Errorf("set claude effort: %w", err)
					}
				case "always-thinking", "always_thinking":
					b, err := parseBoolOption(val)
					if err != nil {
						return fmt.Errorf("invalid boolean %q: %w", val, err)
					}
					if _, err := claudeconfig.SetAlwaysThinking(b); err != nil {
						return fmt.Errorf("set claude always-thinking: %w", err)
					}
				case "yolo":
					b, err := parseBoolOption(val)
					if err != nil {
						return fmt.Errorf("invalid boolean %q: %w", val, err)
					}
					if _, err := claudeconfig.SetYoloMode(b); err != nil {
						return fmt.Errorf("set claude yolo: %w", err)
					}
				default:
					return fmt.Errorf("unknown claude config key %q (want model, effort, always-thinking, yolo)", key)
				}
			case "codex":
				switch key {
				case "effort":
					if val == "default" || val == "clear" {
						val = ""
					}
					if !validCodexEffort(val) {
						return fmt.Errorf("invalid codex effort %q (want %s)", val, codexEffortList())
					}
					if _, err := codexconfig.SetEffortLevel(val); err != nil {
						return fmt.Errorf("set codex effort: %w", err)
					}
				case "yolo":
					b, err := parseBoolOption(val)
					if err != nil {
						return fmt.Errorf("invalid boolean %q: %w", val, err)
					}
					if _, err := codexconfig.SetYoloMode(b); err != nil {
						return fmt.Errorf("set codex yolo: %w", err)
					}
				default:
					return fmt.Errorf("unknown codex config key %q (want effort, yolo)", key)
				}
			case "antigravity":
				switch key {
				case "effort":
					if val == "default" || val == "clear" {
						val = ""
					}
					if !validAntigravityEffort(val) {
						return fmt.Errorf("invalid antigravity effort %q (want %s)", val, antigravityEffortList())
					}
					if _, err := antigravityconfig.SetEffortLevel(val); err != nil {
						return fmt.Errorf("set antigravity effort: %w", err)
					}
				case "yolo":
					b, err := parseBoolOption(val)
					if err != nil {
						return fmt.Errorf("invalid boolean %q: %w", val, err)
					}
					if _, err := antigravityconfig.SetYoloMode(b); err != nil {
						return fmt.Errorf("set antigravity yolo: %w", err)
					}
				default:
					return fmt.Errorf("unknown antigravity config key %q (want effort, yolo)", key)
				}
			default:
				return fmt.Errorf("unsupported agent %q (want claude, codex, or antigravity)", args[0])
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s → %s\n", ag, key, safeField(val))
			return nil
		},
	}
}

func parseBoolOption(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "on", "yes", "1":
		return true, nil
	case "false", "off", "no", "0":
		return false, nil
	default:
		return false, fmt.Errorf("want true/false (or on/off)")
	}
}

func validClaudeEffort(v string) bool {
	for _, opt := range claudeconfig.KnownEffortLevels() {
		if opt.Value == v {
			return true
		}
	}
	return false
}

func claudeEffortList() string {
	var vals []string
	for _, opt := range claudeconfig.KnownEffortLevels() {
		if opt.Value == "" {
			vals = append(vals, "default")
		} else {
			vals = append(vals, opt.Value)
		}
	}
	return strings.Join(vals, ", ")
}

func validCodexEffort(v string) bool {
	for _, opt := range codexconfig.KnownEffortLevels() {
		if opt.Value == v {
			return true
		}
	}
	return false
}

func codexEffortList() string {
	var vals []string
	for _, opt := range codexconfig.KnownEffortLevels() {
		if opt.Value == "" {
			vals = append(vals, "default")
		} else {
			vals = append(vals, opt.Value)
		}
	}
	return strings.Join(vals, ", ")
}

func validAntigravityEffort(v string) bool {
	for _, opt := range antigravityconfig.KnownEffortLevels() {
		if opt.Value == v {
			return true
		}
	}
	return false
}

func antigravityEffortList() string {
	var vals []string
	for _, opt := range antigravityconfig.KnownEffortLevels() {
		if opt.Value == "" {
			vals = append(vals, "default")
		} else {
			vals = append(vals, opt.Value)
		}
	}
	return strings.Join(vals, ", ")
}

// newAgentsModelsCmd: `ccmux agents models [--refresh] [--json]`
//
// Lists the catalog the daemon discovered. Tagged rows: `*` next to
// the current ccmux pin, and each model's source (claude-cli / api /
// fallback). --refresh forces a synchronous re-fetch on the daemon
// side; useful the day Anthropic ships a new model and you don't want
// to wait for the weekly refresh.
func newAgentsModelsCmd() *cobra.Command {
	var (
		refresh bool
		asJSON  bool
	)
	c := &cobra.Command{
		Use:   "models",
		Short: "List the Claude models the daemon discovered (or fallback list)",
		Long: `Print every model the daemon currently knows about.

The catalog comes from the first source that answers: your claude CLI
(a small ` + "`claude -p`" + ` query — works for subscription and API users), then
Anthropic's Models API (GET /v1/models, when ANTHROPIC_API_KEY is set in
the daemon's environment). It is cached on disk, refreshed weekly, and
always merged with a curated list that ships with every ccmux release,
so it's never empty — even offline.

Pass --refresh to re-run discovery now instead of waiting for the weekly
refresh; the daemon runs it at most once every 10 minutes and answers
repeats from that run. Without a running ccmuxd the cached catalog is
shown and --refresh has no effect. Pass --json for machine-parsable
output (the same shape /v1/models returns).`,
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
				// The catalog comes from `claude -p` / the Models API
				// via the daemon: sanitize (see safeprint.go).
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
					safeField(m.ID), safeField(m.DisplayName), formatTokens(m.MaxInput), formatTokens(m.MaxOutput), safeField(string(m.Source)), marker)
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
				fmt.Fprintf(os.Stdout, "Current pin: %s. Use `ccmux agents set-default-model <id>` to change (empty to clear).\n", safeField(currentDefault))
			}
			return nil
		},
	}
	c.Flags().BoolVar(&refresh, "refresh", false,
		"make the daemon re-run model discovery now (claude CLI, then the Models API); at most once per 10 minutes")
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
