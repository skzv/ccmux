package main

import (
	"fmt"
	"math/rand"
	"os"

	"github.com/spf13/cobra"
)

// newTUICmd builds the `ccmux-crawl tui` subcommand — the broadest
// crawl mode. Each iteration picks one of:
//
//   - A KeyMsg from the curated keyChoices alphabet (~70% of iters)
//   - A WindowSizeMsg with random dimensions (~30% of iters)
//
// The proportion is biased toward keys because keys exercise more
// state-machine surface; resizes mainly stress the render layer
// which has its own dedicated `resize` mode.
func newTUICmd() *cobra.Command {
	var (
		iters int
		seed  int64
	)
	c := &cobra.Command{
		Use:   "tui",
		Short: "Random navigation across every screen — finds panics in Update/View",
		Long: `Drive the ccmux Bubble Tea model with a mix of random KeyMsg
and WindowSizeMsg inputs. Every iteration calls both Update() and
View() so layout-time panics surface too.

Default 10k iterations. The seed is logged and embedded in any
crash report so a failure is deterministically reproducible.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runTUI(iters, seed)
		},
	}
	c.Flags().IntVar(&iters, "iters", 10000, "iterations to run")
	c.Flags().Int64Var(&seed, "seed", 0, "rng seed; 0 picks one from the clock")
	return c
}

func runTUI(iters int, seed int64) error {
	rng := newRNG(seed)
	fmt.Printf("ccmux-crawl tui: iters=%d seed=%d\n", iters, rng.seed)

	res := runCrawl(iters, func(iter int) Input {
		// 7-out-of-10 picks a key, the rest are resizes. Keep the
		// math simple — a more elaborate weighted-pick would buy
		// nothing.
		if rng.r.Intn(10) < 7 {
			return randomKey(rng.r)
		}
		return randomResize(rng.r)
	}, 120, 40)

	if res.Panic == nil {
		fmt.Printf("✓ %d iterations, no panic\n", res.Iters)
		return nil
	}
	path := reportCrash("tui", res)
	fmt.Printf("✗ panic at iter %d: %v\n", res.PanicIter, res.Panic)
	fmt.Printf("  crash report: %s\n", path)
	fmt.Printf("  seed for repro: %d\n", rng.seed)
	os.Exit(1)
	return nil
}

// rngSource wraps math/rand.Rand alongside its seed so the crash
// report can echo the seed back for reproducibility.
type rngSource struct {
	r    *rand.Rand
	seed int64
}

func newRNG(seed int64) *rngSource {
	if seed == 0 {
		seed = rand.Int63()
	}
	return &rngSource{r: rand.New(rand.NewSource(seed)), seed: seed}
}
