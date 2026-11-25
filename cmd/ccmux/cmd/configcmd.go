package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/i18n"
)

type configFieldSpec struct {
	key string
	get func(*config.Config) string
	set func(*config.Config, string) error
}

func configFieldSpecs() []configFieldSpec {
	tierSpec := func(key, agentID string, allowed []string) configFieldSpec {
		valid := map[string]bool{"": true}
		for _, a := range allowed {
			valid[strings.ToLower(a)] = true
		}
		return configFieldSpec{
			key: key,
			get: func(c *config.Config) string { return c.Subscription.TierFor(agentID) },
			set: func(c *config.Config, raw string) error {
				v := strings.ToLower(strings.TrimSpace(raw))
				if v == "clear" || v == "unset" {
					v = ""
				}
				if !valid[v] {
					return fmt.Errorf("invalid %s %q (want %s)", key, raw, strings.Join(allowed, ", "))
				}
				c.Subscription.SetTierFor(agentID, v)
				return nil
			},
		}
	}
	return []configFieldSpec{
		{
			key: "projects.root",
			get: func(c *config.Config) string { return c.Projects.Root },
			set: func(c *config.Config, raw string) error {
				raw = strings.TrimSpace(raw)
				if raw == "" {
					return fmt.Errorf("projects.root cannot be empty")
				}
				c.Projects.Root = raw
				return nil
			},
		},
		{
			key: "agents.default",
			get: func(c *config.Config) string { return c.Agents.Default },
			set: func(c *config.Config, raw string) error {
				v := strings.ToLower(strings.TrimSpace(raw))
				if v == "" || v == "shell" {
					c.Agents.Default = v
					return nil
				}
				id, ok := agent.ParseID(v)
				if !ok {
					return fmt.Errorf("unknown agent %q (want %s, or shell)", raw, agentIDList())
				}
				c.Agents.Default = string(id)
				return nil
			},
		},
		{
			key: "claude.default_model",
			get: func(c *config.Config) string { return c.Claude.DefaultModel },
			set: func(c *config.Config, raw string) error {
				v := strings.TrimSpace(raw)
				if v == "clear" {
					v = ""
				}
				c.Claude.DefaultModel = v
				return nil
			},
		},
		{
			key: "sessions.default_dir",
			get: func(c *config.Config) string { return c.Sessions.DefaultDir },
			set: func(c *config.Config, raw string) error {
				c.Sessions.DefaultDir = strings.TrimSpace(raw)
				return nil
			},
		},
		{
			key: "sessions.attach_mode",
			get: func(c *config.Config) string {
				if c.Sessions.AttachMode == "" {
					return "mirror"
				}
				return c.Sessions.AttachMode
			},
			set: func(c *config.Config, raw string) error {
				switch strings.ToLower(strings.TrimSpace(raw)) {
				case "", "mirror":
					c.Sessions.AttachMode = "mirror"
					return nil
				case "exclusive":
					c.Sessions.AttachMode = "exclusive"
					return nil
				default:
					return fmt.Errorf("invalid sessions.attach_mode %q (want mirror or exclusive)", raw)
				}
			},
		},
		{
			key: "conversations.show_headless",
			get: func(c *config.Config) string { return strconv.FormatBool(c.Conversations.ShowHeadless) },
			set: func(c *config.Config, raw string) error {
				b, err := parseBoolOption(raw)
				if err != nil {
					return err
				}
				c.Conversations.ShowHeadless = b
				return nil
			},
		},
		{
			key: "daemon.listen_tailnet",
			get: func(c *config.Config) string { return strconv.FormatBool(c.Daemon.ListenTailnet) },
			set: func(c *config.Config, raw string) error {
				b, err := parseBoolOption(raw)
				if err != nil {
					return err
				}
				c.Daemon.ListenTailnet = b
				return nil
			},
		},
		{
			key: "daemon.tailnet_port",
			get: func(c *config.Config) string { return strconv.Itoa(c.Daemon.TailnetPort) },
			set: func(c *config.Config, raw string) error {
				p, err := strconv.Atoi(strings.TrimSpace(raw))
				if err != nil || p < 1 || p > 65535 {
					return fmt.Errorf("invalid port %q (want 1..65535)", raw)
				}
				c.Daemon.TailnetPort = p
				return nil
			},
		},
		{
			key: "daemon.poll_interval_seconds",
			get: func(c *config.Config) string { return strconv.Itoa(c.Daemon.PollIntervalSeconds) },
			set: func(c *config.Config, raw string) error {
				n, err := strconv.Atoi(strings.TrimSpace(raw))
				if err != nil || n < 1 || n > 60 {
					return fmt.Errorf("invalid poll_interval_seconds %q (want 1..60)", raw)
				}
				c.Daemon.PollIntervalSeconds = n
				return nil
			},
		},
		{
			key: "sleep.mode",
			get: func(c *config.Config) string { return c.Sleep.Mode },
			set: func(c *config.Config, raw string) error {
				switch strings.ToLower(strings.TrimSpace(raw)) {
				case "safe", "dangerous", "very_dangerous":
					c.Sleep.Mode = strings.ToLower(strings.TrimSpace(raw))
					return nil
				default:
					return fmt.Errorf("invalid sleep.mode %q (want safe, dangerous, or very_dangerous)", raw)
				}
			},
		},
		{
			key: "notifications.bell",
			get: func(c *config.Config) string { return strconv.FormatBool(c.Notifications.Bell) },
			set: func(c *config.Config, raw string) error {
				b, err := parseBoolOption(raw)
				if err != nil {
					return err
				}
				c.Notifications.Bell = b
				return nil
			},
		},
		{
			key: "notifications.sound",
			get: func(c *config.Config) string { return c.Notifications.Sound },
			set: func(c *config.Config, raw string) error {
				c.Notifications.Sound = strings.TrimSpace(raw)
				return nil
			},
		},
		{
			key: "notes.expand_folders",
			get: func(c *config.Config) string { return strconv.FormatBool(c.Notes.ExpandFolders) },
			set: func(c *config.Config, raw string) error {
				b, err := parseBoolOption(raw)
				if err != nil {
					return err
				}
				c.Notes.ExpandFolders = b
				return nil
			},
		},
		{
			key: "update.auto_check",
			get: func(c *config.Config) string { return strconv.FormatBool(c.Update.AutoCheck) },
			set: func(c *config.Config, raw string) error {
				b, err := parseBoolOption(raw)
				if err != nil {
					return err
				}
				c.Update.AutoCheck = b
				return nil
			},
		},
		{
			key: "theme",
			get: func(c *config.Config) string { return c.Theme },
			set: func(c *config.Config, raw string) error {
				v := strings.TrimSpace(raw)
				if v == "" {
					return fmt.Errorf("theme cannot be empty")
				}
				c.Theme = v
				return nil
			},
		},
		{
			key: "editor",
			get: func(c *config.Config) string { return c.Editor },
			set: func(c *config.Config, raw string) error {
				c.Editor = strings.TrimSpace(raw)
				return nil
			},
		},
		{
			key: "lang",
			get: func(c *config.Config) string { return c.Lang },
			set: func(c *config.Config, raw string) error {
				v := strings.TrimSpace(raw)
				if v == "" || v == "auto" {
					c.Lang = ""
					return nil
				}
				code, ok := i18n.Parse(v)
				if !ok {
					return fmt.Errorf("unsupported language %q (want %s, or auto)", raw, strings.Join(i18n.Codes(), ", "))
				}
				c.Lang = string(code)
				return nil
			},
		},
		tierSpec("subscription.claude", "claude", []string{"api", "pro", "max5x", "max20x"}),
		tierSpec("subscription.codex", "codex", []string{"api", "free", "plus", "pro", "team"}),
		tierSpec("subscription.antigravity", "antigravity", []string{"api", "free", "ai-pro", "ai-ultra"}),
		tierSpec("subscription.cursor", "cursor", []string{"free", "pro", "pro+", "ultra", "teams"}),
	}
}

func findConfigSpec(key string) (configFieldSpec, bool) {
	k := strings.ToLower(strings.TrimSpace(key))
	for _, s := range configFieldSpecs() {
		if s.key == k {
			return s, true
		}
	}
	return configFieldSpec{}, false
}

// newConfigCmd: `ccmux config {get,set,list}` — CLI parity with the TUI Settings screen.
func newConfigCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "config",
		Short: "Read or update ccmux settings in ~/.config/ccmux/config.toml",
		Long:  "Inspect and edit ccmux preferences from the command line, preserving existing comments in config.toml. Mirrors the TUI Settings screen.",
	}
	var listJSON bool
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "Print all configurable keys and their current values",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return printConfigValues(cmd, listJSON)
		},
	}
	listCmd.Flags().BoolVar(&listJSON, "json", false, "output key/value pairs as JSON")

	var getJSON bool
	getCmd := &cobra.Command{
		Use:   "get [key]",
		Short: "Print one config setting (or all settings if no key is given)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return printConfigValues(cmd, getJSON)
			}
			spec, ok := findConfigSpec(args[0])
			if !ok {
				return fmt.Errorf("unknown config key %q (run `ccmux config list` to see supported keys)", args[0])
			}
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			val := spec.get(&cfg)
			if getJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]string{spec.key: val})
			}
			fmt.Fprintln(cmd.OutOrStdout(), safeField(val))
			return nil
		},
	}
	getCmd.Flags().BoolVar(&getJSON, "json", false, "output as JSON")

	setCmd := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Update one setting in ~/.config/ccmux/config.toml",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, ok := findConfigSpec(args[0])
			if !ok {
				return fmt.Errorf("unknown config key %q (run `ccmux config list` to see supported keys)", args[0])
			}
			cfg, err := config.Update(func(cfg *config.Config) error {
				return spec.set(cfg, args[1])
			})
			if err != nil {
				return fmt.Errorf("set %s: %w", spec.key, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s = %s\n", spec.key, safeField(spec.get(&cfg)))
			return nil
		},
	}

	c.AddCommand(getCmd, setCmd, listCmd)
	return c
}

func printConfigValues(cmd *cobra.Command, asJSON bool) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	specs := configFieldSpecs()
	if asJSON {
		m := make(map[string]string, len(specs))
		for _, s := range specs {
			m[s.key] = s.get(&cfg)
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(m)
	}
	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "KEY\tVALUE")
	for _, s := range specs {
		fmt.Fprintf(tw, "%s\t%s\n", s.key, safeField(s.get(&cfg)))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if p, err := config.Path(); err == nil {
		fmt.Fprintf(os.Stderr, "\n# %s\n", p)
	}
	return nil
}
