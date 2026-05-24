package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime/debug"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/tui"
)

// step is one action in a scenario.
type step struct {
	in     Input  // key or resize to send
	want   string // if non-empty: View() must contain this after the step
	absent string // if non-empty: View() must NOT contain this after the step
}

// scenario is a named sequence of steps.
type scenario struct {
	name  string
	steps []step
}

// allScenarios returns the complete list of named deterministic scenarios.
// Each scenario exercises a specific user-facing flow and asserts that
// the View output contains (or doesn't contain) expected strings.
//
// Adding a new scenario here is the standard way to extend deterministic
// crawl coverage when a new feature or screen ships.
func allScenarios() []scenario {
	return []scenario{
		{
			name: "all-screens",
			steps: []step{
				{in: keyRune('1'), want: "Home"},
				{in: keyRune('2'), want: "Conversations"},
				{in: keyRune('3'), want: "Projects"},
				{in: keyRune('4'), want: "Notes"},
				{in: keyRune('5'), want: "Agents"},
				{in: keyRune('6'), want: "Settings"},
				{in: keyRune('7'), want: "Network"},
				{in: keyRune('1'), want: "Home"},
			},
		},
		{
			name: "help-overlay",
			steps: []step{
				{in: keyRune('?'), want: "switch screens"},
				{in: keyType(tea.KeyEsc, "esc"), absent: "switch screens"},
			},
		},
		{
			name: "new-session-abandon",
			steps: []step{
				{in: keyRune('n'), want: "New session"},
				{in: keyType(tea.KeyEsc, "esc"), absent: "New session"},
			},
		},
		{
			name: "nav-and-back",
			steps: []step{
				{in: keyRune('3'), want: "Projects"},
				{in: keyRune('1'), want: "Home"},
				{in: keyRune('4'), want: "Notes"},
				{in: keyRune('1'), want: "Home"},
			},
		},
		{
			name: "resize-extremes",
			steps: []step{
				{in: resizeInput(1, 1)},
				{in: resizeInput(300, 100)},
				{in: resizeInput(1, 1)},
				{in: resizeInput(80, 24)},
				{in: resizeInput(120, 40)},
			},
		},
		{
			name: "session-list-nav",
			steps: []step{
				{in: keyType(tea.KeyDown, "down")},
				{in: keyType(tea.KeyDown, "down")},
				{in: keyType(tea.KeyUp, "up")},
				{in: keyType(tea.KeyUp, "up")},
				{in: keyType(tea.KeyEnter, "enter")},
				{in: keyType(tea.KeyEsc, "esc")},
			},
		},
	}
}

// allScreensPreamble returns the preamble steps for the all-screens scenario,
// used as the deterministic prefix for all-screens-random.
func allScreensPreamble() []Input {
	return []Input{
		keyRune('1'),
		keyRune('2'),
		keyRune('3'),
		keyRune('4'),
		keyRune('5'),
		keyRune('6'),
		keyRune('7'),
		keyRune('1'),
	}
}

// findScenario returns a pointer to the named scenario, or nil if not found.
// The returned pointer points into a locally-allocated slice; it is valid
// for the lifetime of the caller's frame.
func findScenario(name string) *scenario {
	all := allScenarios()
	for i := range all {
		if all[i].name == name {
			s := all[i]
			return &s
		}
	}
	return nil
}

// runScenario drives the TUI model through all steps in s, checking
// assertions after each step. It catches panics via recover() and
// returns them as errors.
//
// After each Update call, the returned tea.Cmd is executed synchronously
// up to cmdDrainDepth times, feeding each produced tea.Msg back into the
// model. This is necessary because many state transitions (e.g. closing a
// modal) happen through the Cmd→Msg channel rather than directly in
// Update. Commands that return nil stop the chain early.
func runScenario(s scenario) (err error) {
	cfg, _ := config.Load()
	// Skip the first-run tour: with Tour.Shown=false the tour overlay
	// auto-opens and intercepts every key, so scenarios that test post-
	// onboarding behavior (?, n, R, x …) never reach the screen they
	// target. Locally this is masked by the dev's own completed-tour
	// config; in CI the loaded config is empty so the bug surfaces.
	cfg.Tour.Shown = true
	app := tui.New(cfg, "crawl")
	_ = app.Init()

	var model tea.Model = app
	model, _ = model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v\n%s", r, debug.Stack())
		}
	}()

	for i, st := range s.steps {
		var cmd tea.Cmd
		model, cmd = model.Update(st.in.Msg)
		// Drain synchronous cmd chains. Each cmd is a plain func() tea.Msg;
		// those that produce state-machine transitions (like closing a modal)
		// return immediately. We cap the drain depth to avoid infinite loops
		// from cmds that produce further cmds (e.g. periodic ticks).
		drainScenarioCmd(&model, cmd)
		out := model.View()
		if st.want != "" && !strings.Contains(out, st.want) {
			return fmt.Errorf("step %d (%s): want %q in output\nView:\n%.500s", i, st.in.Label, st.want, out)
		}
		if st.absent != "" && strings.Contains(out, st.absent) {
			return fmt.Errorf("step %d (%s): want %q absent from output\nView:\n%.500s", i, st.in.Label, st.absent, out)
		}
	}
	return nil
}

// cmdDrainDepth is the maximum number of synchronous cmd→msg→update
// cycles drainScenarioCmd will execute. This prevents infinite loops
// from cmds that produce further cmds while still handling the single-
// level chains common in modal close/submit flows.
const cmdDrainDepth = 8

// drainScenarioCmd executes cmd and feeds the resulting tea.Msg back into
// model, repeating until cmd is nil, the msg is nil, or the depth limit
// is reached. Panics propagate to the caller's recover() handler.
func drainScenarioCmd(model *tea.Model, cmd tea.Cmd) {
	for i := 0; i < cmdDrainDepth && cmd != nil; i++ {
		msg := cmd()
		if msg == nil {
			return
		}
		var next tea.Cmd
		*model, next = (*model).Update(msg)
		cmd = next
	}
}

// newScenarioCmd builds the `ccmux-crawl scenario` subcommand.
//
// Default: run all scenarios (plus all-screens-random with --iters
// random iterations appended to the all-screens preamble).
// --scenario <name> runs only the named scenario.
// --iters N controls random iterations for all-screens-random (default 500).
func newScenarioCmd() *cobra.Command {
	var (
		scenarioName string
		iters        int
	)
	c := &cobra.Command{
		Use:   "scenario",
		Short: "Run named deterministic scripts against the TUI model",
		Long: `scenario runs named, deterministic input sequences against the
ccmux Bubble Tea model and asserts that the View output contains
(or doesn't contain) expected strings after each step.

Default: all scenarios are run in order.
--scenario <name>: run only that scenario.
--iters N: random iterations for all-screens-random (default 500).

Exit code 1 if any scenario fails.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runScenarioCmd(scenarioName, iters)
		},
	}
	c.Flags().StringVar(&scenarioName, "scenario", "", "run only this scenario by name")
	c.Flags().IntVar(&iters, "iters", 500, "random iterations for all-screens-random")
	return c
}

func runScenarioCmd(name string, iters int) error {
	scenarios := allScenarios()
	runAll := name == ""

	anyFail := false

	// Helper to run and report one named scenario.
	run := func(s scenario) {
		err := runScenario(s)
		if err == nil {
			fmt.Printf("✓ %s\n", s.name)
		} else {
			fmt.Printf("✗ %s: %v\n", s.name, err)
			anyFail = true
		}
	}

	if !runAll {
		s := findScenario(name)
		if s == nil {
			// Also check for the special all-screens-random scenario.
			if name == "all-screens-random" {
				runAllScreensRandom(iters, &anyFail)
			} else {
				return fmt.Errorf("unknown scenario %q (use --scenario without a value to list all)", name)
			}
		} else {
			run(*s)
		}
	} else {
		// Run all deterministic scenarios.
		for _, s := range scenarios {
			run(s)
		}
		// Run all-screens-random last (it tails off into random fuzzing).
		runAllScreensRandom(iters, &anyFail)
	}

	if anyFail {
		os.Exit(1)
	}
	return nil
}

// runAllScreensRandom runs the all-screens preamble then hands off to
// random fuzzing for iters iterations. Uses runCrawlWithPreamble so
// the random portion shares the same panic-capture machinery.
func runAllScreensRandom(iters int, anyFail *bool) {
	rng := rand.New(rand.NewSource(rand.Int63()))
	res := runCrawlWithPreamble(iters, allScreensPreamble(), func(iter int) Input {
		if rng.Intn(10) < 7 {
			return randomKey(rng)
		}
		return randomResize(rng)
	}, 120, 40)

	if res.Panic == nil {
		fmt.Printf("✓ all-screens-random (%d random iters)\n", iters)
	} else {
		fmt.Printf("✗ all-screens-random: panic at iter %d: %v\n", res.PanicIter, res.Panic)
		if res.Stack != "" {
			fmt.Printf("  stack:\n%s\n", res.Stack)
		}
		*anyFail = true
	}
}
