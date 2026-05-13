package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// newResizeCmd builds the `ccmux-crawl resize` subcommand — pure
// WindowSizeMsg permutations. The dashboard has both narrow-mode
// (<80 cols) and wide-mode layouts; degenerate sizes (1×1, 80×1,
// 1×100) put lipgloss's clamp/truncate paths through their paces.
func newResizeCmd() *cobra.Command {
	var (
		iters int
		seed  int64
	)
	c := &cobra.Command{
		Use:   "resize",
		Short: "Rapid-fire WindowSizeMsg across degenerate dimensions",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runResize(iters, seed)
		},
	}
	c.Flags().IntVar(&iters, "iters", 1000, "iterations to run")
	c.Flags().Int64Var(&seed, "seed", 0, "rng seed; 0 picks one from the clock")
	return c
}

func runResize(iters int, seed int64) error {
	rng := newRNG(seed)
	fmt.Printf("ccmux-crawl resize: iters=%d seed=%d\n", iters, rng.seed)

	res := runCrawl(iters, func(iter int) Input {
		return randomResize(rng.r)
	}, 80, 24)

	if res.Panic == nil {
		fmt.Printf("✓ %d iterations, no panic\n", res.Iters)
		return nil
	}
	path := reportCrash("resize", res)
	fmt.Printf("✗ panic at iter %d: %v\n", res.PanicIter, res.Panic)
	fmt.Printf("  crash report: %s\n", path)
	fmt.Printf("  seed for repro: %d\n", rng.seed)
	os.Exit(1)
	return nil
}
