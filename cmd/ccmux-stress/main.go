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
// Subcommands write a markdown report — to docs/03_Agent_Logs/ when run
// from a ccmux checkout, else to the system temp dir, or wherever
// --report-dir says — and print its path.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "ccmux-stress",
	Short: "Driver for stress / load testing ccmuxd against realistic profiles",
	Long: `ccmux-stress is a developer tool that exercises ccmuxd under the
load profiles described in docs/01_Specs/03_Testing_And_CI.md. It is
NOT distributed to end users.

Run against the local daemon — the one behind
$HOME/.local/state/ccmux/ccmuxd.sock; its pid is read from that socket,
so point HOME at a sandbox to measure a sandbox daemon. Each subcommand
spawns its own tmux sessions under a recognizable name prefix
(c-stress-…-<runid>-N) and kills them when it exits, including on
Ctrl-C.

A markdown report, stress-<date>-<profile>-<runid>.md, lands in
--report-dir; by default that's docs/03_Agent_Logs/ when run from the
root of a ccmux checkout, else the system temp dir. The path is
printed.`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func main() {
	rootCmd.PersistentFlags().StringVar(&reportDirFlag, "report-dir", "",
		"directory for the markdown report (default: docs/03_Agent_Logs in a ccmux checkout, else the system temp dir)")
	rootCmd.AddCommand(
		newSessionsCmd(),
		newNotificationsCmd(),
		newLonghaulCmd(),
		newBareSessionsCmd(),
	)
	// Ctrl-C / SIGTERM cancel the run's context instead of killing the
	// process outright, so each profile's deferred cleanup still kills
	// the tmux sessions it spawned.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := rootCmd.ExecuteContext(ctx)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ccmux-stress:", err)
		os.Exit(1)
	}
}
