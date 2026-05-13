// Command ccmux-stress drives load against a running ccmuxd to expose
// regressions before users do. It's a developer tool, not part of the
// shipped CLI; `make install` deliberately doesn't install it.
//
// Subcommands (per docs/01_Specs/03_Testing_And_CI.md, "Stress
// testing" workstream):
//
//	sessions       spawn N fake tmux sessions, watch daemon resources
//	                while the poll loop processes them. Stage 4.
//	notifications  burst N needs_input transitions, measure bell
//	                latency + duplicate-fire rate. Stage 5.
//	longhaul       slow cadence over hours, fail on the 150 MB / 3×
//	                RSS thresholds from the spec. Stage 5.
//
// All subcommands write a markdown report to
// docs/03_Agent_Logs/stress-<date>.md so the historical signal
// accumulates with the rest of the project's daily logs.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "ccmux-stress",
	Short: "Driver for stress / load testing ccmuxd against realistic profiles",
	Long: `ccmux-stress is a developer tool that exercises ccmuxd under the
load profiles described in docs/01_Specs/03_Testing_And_CI.md. It is
NOT distributed to end users.

Run against the local daemon. Each subcommand spawns its own tmux
sessions under a recognizable name prefix (c-stress-<runid>-N) and
cleans them up at exit. A markdown report lands in
docs/03_Agent_Logs/stress-<date>.md.`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func main() {
	rootCmd.AddCommand(
		newSessionsCmd(),
		newNotificationsCmd(),
		newLonghaulCmd(),
		newBareSessionsCmd(),
	)
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "ccmux-stress:", err)
		os.Exit(1)
	}
}
