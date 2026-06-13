// Package cmd defines the Cobra command tree for `ccmux`. With no subcommand,
// `ccmux` launches the TUI; subcommands provide scripting hooks.
package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/setupwizard"
	"github.com/skzv/ccmux/internal/tui"
)

var versionString string

// Execute is the entrypoint called from cmd/ccmux/main.go.
func Execute(version string) error {
	versionString = version
	return rootCmd.Execute()
}

var projectsRootFlag string
var expandNotesFlag bool

var rootCmd = &cobra.Command{
	Use:   "ccmux [projects-dir]",
	Short: "Manage Claude Code sessions across tmux, Mosh, and Tailscale",
	Long: `ccmux is a TUI for starting, resuming, and supervising Claude Code
sessions on top of tmux, with optional remote-host support over Tailscale and
Mosh-friendly mobile workflow.

Run with no arguments to launch the TUI with the default projects directory
(~/Projects, or whatever's in ~/.config/ccmux/config.toml).

Pass a directory argument or --projects PATH to scope this session to a
different projects root without rewriting config:

  ccmux ~/code              # one-shot override
  ccmux --projects ~/work   # equivalent`,
	Args:          cobra.MaximumNArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(c *cobra.Command, args []string) error {
		override := projectsRootFlag
		if override == "" && len(args) == 1 {
			override = args[0]
		}
		maybeNudgeSetup()
		return tui.Run(versionString, override, expandNotesFlag)
	},
}

// shouldNudgeSetup decides whether to offer the first-run setup prompt:
// only on an interactive terminal, only before setup has completed, not
// after the user dismissed the offer, and not for someone who has already
// used ccmux (Tour.Shown) — so an existing, pre-feature user isn't told
// they "aren't set up yet."
func shouldNudgeSetup(cfg config.Config, interactive bool) bool {
	return interactive && !cfg.Setup.Completed && !cfg.Setup.NudgeDismissed && !cfg.Tour.Shown
}

// maybeNudgeSetup offers to run `ccmux setup` the first time someone
// launches ccmux on an unconfigured machine. It never blocks: declining
// (or any error) just continues to the TUI, and a decline is remembered
// so repeat launches aren't nagged. Skipped entirely under a
// non-interactive stdin (scripts, pipes).
func maybeNudgeSetup() {
	// Escape hatch for CI, scripts, and anyone who never wants the prompt
	// (also how the e2e harness keeps the TUI tests nudge-free).
	if os.Getenv("CCMUX_NO_SETUP_NUDGE") != "" {
		return
	}
	cfg, err := config.Load()
	if err != nil {
		return
	}
	if !shouldNudgeSetup(cfg, stdinIsTerminal()) {
		return
	}
	fmt.Println()
	fmt.Println("ccmux isn't set up yet. The wizard configures Tailscale, your agent")
	fmt.Println("CLIs, and the ccmuxd background service — idempotent and safe to re-run.")
	if promptYesNoDefaultYes("Run `ccmux setup` now?") {
		_ = setupwizard.Run(context.Background(), os.Stdout)
		// If setup didn't actually complete (user aborted, or an
		// unsupported platform that bails early), remember not to re-nag
		// on every launch.
		if c, err := config.Load(); err == nil && !c.Setup.Completed && !c.Setup.NudgeDismissed {
			c.Setup.NudgeDismissed = true
			_ = config.Save(c)
		}
		return
	}
	cfg.Setup.NudgeDismissed = true
	_ = config.Save(cfg)
	fmt.Println(`Skipping — run "ccmux setup" whenever you're ready.`)
	fmt.Println()
}

// stdinIsTerminal reports whether stdin is an interactive terminal, so we
// don't prompt in scripts or pipes.
func stdinIsTerminal() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// promptYesNoDefaultYes blocks on a single y/N answer that defaults to
// YES (empty input counts as yes).
func promptYesNoDefaultYes(question string) bool {
	fmt.Printf("%s [Y/n] ", question)
	var reply string
	_, _ = fmt.Scanln(&reply)
	reply = strings.TrimSpace(strings.ToLower(reply))
	return reply == "" || reply == "y" || reply == "yes"
}

func init() {
	rootCmd.Version = versionString
	rootCmd.PersistentFlags().StringVar(&projectsRootFlag, "projects", "",
		"override the projects directory for this run (defaults to ~/Projects or config.toml)")
	rootCmd.Flags().BoolVar(&expandNotesFlag, "expand-notes", false,
		"open the Notes folder tree fully expanded (default: folders start collapsed)")
	rootCmd.AddCommand(
		newAttachCmd(),
		newNewCmd(),
		newShellCmd(),
		newUpdateCmd(),
		newListCmd(),
		newProjectCmd(),
		newKillCmd(),
		newSetupCmd(),
		newDoctorCmd(),
		newDaemonCmd(),
		newHostCmd(),
		newMCPCmd(),
		newMoshiSetupCmd(),
		newUninstallCmd(),
		newRenameCmd(),
		newClipboardPipeCmd(),
		newListConversationsCmd(),
		newResumeCmd(),
		newDeleteConversationCmd(),
		newNotesCmd(),
		newAgentsCmd(),
	)
}
