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

var rootCmd = &cobra.Command{
	Use:   "ccmux",
	Short: "Manage Claude Code sessions across tmux, Mosh, and Tailscale",
	Long: `ccmux is a TUI for starting, resuming, and supervising Claude Code
sessions on top of tmux, with optional remote-host support over Tailscale and
Mosh-friendly mobile workflow.

Run with no arguments to launch the TUI.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(c *cobra.Command, args []string) error {
		return tui.Run(versionString)
	},
}

func init() {
	rootCmd.Version = versionString
	rootCmd.AddCommand(
		newAttachCmd(),
		newNewCmd(),
		newUpgradeCmd(),
		newUpdateCmd(),
		newListCmd(),
		newKillCmd(),
		newSetupCmd(),
		newDoctorCmd(),
		newDaemonCmd(),
		newHostCmd(),
		newMoshiSetupCmd(),
		newUninstallCmd(),
	)
}
