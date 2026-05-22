// Package cmd defines the Cobra command tree for `ccmux`. With no subcommand,
// `ccmux` launches the TUI; subcommands provide scripting hooks.
package cmd

import (
	"github.com/spf13/cobra"

	"github.com/skzv/ccmux/internal/tui"
)

var versionString string

// Execute is the entrypoint called from cmd/ccmux/main.go.
func Execute(version string) error {
	versionString = version
	return rootCmd.Execute()
}

var projectsRootFlag string

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
		return tui.Run(versionString, override)
	},
}

func init() {
	rootCmd.Version = versionString
	rootCmd.PersistentFlags().StringVar(&projectsRootFlag, "projects", "",
		"override the projects directory for this run (defaults to ~/Projects or config.toml)")
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
		newMoshiSetupCmd(),
		newUninstallCmd(),
		newRenameCmd(),
		newClipboardPipeCmd(),
		newListConversationsCmd(),
		newResumeCmd(),
		newDeleteConversationCmd(),
	)
}
