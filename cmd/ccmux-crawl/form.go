package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
)

// newFormCmd builds the `ccmux-crawl form` subcommand — focused on
// the new-project modal. Every iteration is a key. The crawler
// pre-seeds the App into the form by sending `3` (Projects screen)
// then `n` (open modal) before the random sequence starts. From
// that point the form's keymap is what we're exercising — agent
// picker cycling, tab navigation between fields, text typing, esc.
func newFormCmd() *cobra.Command {
	var (
		iters int
		seed  int64
	)
	c := &cobra.Command{
		Use:   "form",
		Short: "Drive random keys into the new-project modal form",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runForm(iters, seed)
		},
	}
	c.Flags().IntVar(&iters, "iters", 10000, "iterations to run")
	c.Flags().Int64Var(&seed, "seed", 0, "rng seed; 0 picks one from the clock")
	return c
}

func runForm(iters int, seed int64) error {
	rng := newRNG(seed)
	fmt.Printf("ccmux-crawl form: iters=%d seed=%d\n", iters, rng.seed)

	res := runCrawlWithPreamble(iters,
		[]Input{
			keyRune('3'), // Projects screen
			keyRune('n'), // open new-project modal
		},
		func(iter int) Input {
			// Esc + open the form again every 200 iters so the
			// crawler doesn't get stuck on a dead-ended form after
			// a hypothetical submit. (Submit fires actual scaffold
			// work via a tea.Cmd; we don't execute commands here so
			// the form stays "open" indefinitely, but if a future
			// refactor changes that this hatch keeps the crawl
			// flowing.)
			if iter > 0 && iter%200 == 0 {
				if rng.r.Intn(2) == 0 {
					return keyType(tea.KeyEsc, "esc")
				}
				return keyRune('n')
			}
			return randomKey(rng.r)
		},
		120, 40,
	)

	if res.Panic == nil {
		fmt.Printf("✓ %d iterations, no panic\n", res.Iters)
		return nil
	}
	path := reportCrash("form", res)
	fmt.Printf("✗ panic at iter %d: %v\n", res.PanicIter, res.Panic)
	fmt.Printf("  crash report: %s\n", path)
	fmt.Printf("  seed for repro: %d\n", rng.seed)
	os.Exit(1)
	return nil
}
