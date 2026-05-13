// Command ccmux-crawl is the TUI monkey-tester. It drives the ccmux
// Bubble Tea model with random key sequences + random window sizes,
// catches panics, and writes crash reports for review.
//
// Three modes, per docs/01_Specs/03_Testing_And_CI.md crawl plan:
//
//	tui     — random navigation across every screen; periodic resizes
//	form    — focused on the new-project modal form
//	resize  — pure WindowSizeMsg permutations across degenerate widths
//
// Implementation note: we don't use charmbracelet/x/exp/teatest because
// its only ccmux-relevant value (snapshot diffing against golden files)
// isn't what we want here. The crawl tester only needs "does
// Update(msg) panic for this sequence?" We drive the App's Update
// method directly, which is simpler, deps-free, and lets the crash
// report carry the exact input sequence that reproduced the panic.
//
// This binary is a developer tool — NOT installed by `make install`,
// same shape as cmd/ccmux-stress.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "ccmux-crawl",
	Short: "Random-input monkey tester for the ccmux TUI",
	Long: `ccmux-crawl drives the ccmux Bubble Tea model with random
tea.Msg inputs (KeyMsg, WindowSizeMsg) for N iterations, catching
panics from any model under test. Crash reports land under
docs/03_Agent_Logs/crawl-<date>-<mode>-<runid>.md with the exact
input sequence that triggered the panic so it's reproducible.

This is a developer tool, not part of the shipped CLI.`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func main() {
	rootCmd.AddCommand(
		newTUICmd(),
		newFormCmd(),
		newResizeCmd(),
	)
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "ccmux-crawl:", err)
		os.Exit(1)
	}
}
