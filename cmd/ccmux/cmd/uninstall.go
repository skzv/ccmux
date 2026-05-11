package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/skzv/ccmux/internal/daemonservice"
	"github.com/skzv/ccmux/internal/tmux"
	"github.com/skzv/ccmux/internal/tmuxchrome"
)

// newUninstallCmd: `ccmux uninstall [--yes] [--keep-config] [--keep-chrome]`
// removes everything ccmux installed: binaries, daemon state, config,
// caches. Deliberately leaves alone:
//
//   - User project directories under the configured projects root.
//     Anything you scaffolded with `ccmux new` is your work, not ours.
//   - Notes under <project>/docs/. Same reason.
//   - The ~/.claude/ directory. moshi-hook lives there and is a separate
//     product — uninstall it via `brew uninstall moshi-hook` if you
//     want to clear it.
//   - The shell-level zsh aliases (cc / mkproj / upgrade-proj). If you
//     added shim functions pointing at ccmux, remove them by hand.
//
// What we DO remove:
//
//   - Running ccmuxd process (SIGTERM via pkill)
//   - $HOME/.local/bin/ccmux and ccmuxd
//   - $HOME/.local/state/ccmux/* (sockets, logs, pid)
//   - $HOME/.local/share/ccmux/* (snapshots, daemon db)
//   - $HOME/.config/ccmux/* (config.toml) — unless --keep-config
//   - tmux chrome overrides on every c-* session — unless --keep-chrome
func newUninstallCmd() *cobra.Command {
	var (
		yes         bool
		keepConfig  bool
		keepChrome  bool
		dryRun      bool
	)
	c := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove ccmux and its state; never touches user projects or notes",
		RunE: func(_ *cobra.Command, _ []string) error {
			plan, err := buildUninstallPlan(keepConfig, keepChrome)
			if err != nil {
				return err
			}
			fmt.Println("ccmux uninstall plan:")
			for _, step := range plan.steps {
				fmt.Println("  •", step)
			}
			fmt.Println()
			fmt.Println("Left alone (delete manually if you want):")
			for _, item := range plan.leftAlone {
				fmt.Println("  ·", item)
			}
			fmt.Println()

			if dryRun {
				fmt.Println("(dry run — pass without --dry-run to execute)")
				return nil
			}
			if !yes {
				fmt.Print("Proceed? [y/N] ")
				var answer string
				_, _ = fmt.Scanln(&answer)
				answer = strings.TrimSpace(strings.ToLower(answer))
				if answer != "y" && answer != "yes" {
					fmt.Println("Cancelled.")
					return nil
				}
			}
			return runUninstall(plan)
		},
	}
	c.Flags().BoolVar(&yes, "yes", false, "skip the y/N confirmation")
	c.Flags().BoolVar(&keepConfig, "keep-config", false, "leave ~/.config/ccmux in place")
	c.Flags().BoolVar(&keepChrome, "keep-chrome", false, "leave the styled tmux status bar on attached sessions")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "print what would be removed without doing anything")
	return c
}

// uninstallPlan is the list of paths/actions we'll do, used both for
// the "preview" block before confirmation and the execute loop.
type uninstallPlan struct {
	steps      []string
	paths      []string
	resetTmux  bool
	keepConfig bool
	leftAlone  []string
}

func buildUninstallPlan(keepConfig, keepChrome bool) (*uninstallPlan, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	p := &uninstallPlan{keepConfig: keepConfig, resetTmux: !keepChrome}

	if plist := daemonservice.PlistPathOrEmpty(); plist != "" {
		if _, err := os.Stat(plist); err == nil {
			p.paths = append(p.paths, plist)
			p.steps = append(p.steps, "unload + remove "+plist+" (launchd agent)")
		}
	}

	binDir := filepath.Join(home, ".local", "bin")
	for _, bin := range []string{"ccmux", "ccmuxd"} {
		full := filepath.Join(binDir, bin)
		if _, err := os.Stat(full); err == nil {
			p.paths = append(p.paths, full)
			p.steps = append(p.steps, "remove "+full)
		}
	}
	for _, dir := range []string{
		filepath.Join(home, ".local", "state", "ccmux"),
		filepath.Join(home, ".local", "share", "ccmux"),
	} {
		if _, err := os.Stat(dir); err == nil {
			p.paths = append(p.paths, dir)
			p.steps = append(p.steps, "remove "+dir+" (state, logs, snapshots)")
		}
	}
	if !keepConfig {
		cfg := filepath.Join(home, ".config", "ccmux")
		if _, err := os.Stat(cfg); err == nil {
			p.paths = append(p.paths, cfg)
			p.steps = append(p.steps, "remove "+cfg+" (config.toml, hosts.toml)")
		}
	}

	if !keepChrome {
		// We'll resolve the list of c-* sessions at run-time too, but
		// we mention the action in the preview.
		p.steps = append(p.steps, "reset tmux status-bar chrome on every c-* session")
	}
	p.steps = append([]string{"stop running ccmuxd (SIGTERM)"}, p.steps...)

	p.leftAlone = []string{
		filepath.Join(home, "Projects") + " (your project directories — never deleted by ccmux)",
		filepath.Join(home, ".claude") + " (Claude Code's own state, plus moshi-hook's settings.json entries)",
		"~/.zshrc shims for cc / mkproj / upgrade-proj — remove by hand if you added them",
		"`brew uninstall moshi-hook` if you want to uninstall the agent-hook daemon",
	}
	if keepConfig {
		p.leftAlone = append(p.leftAlone, filepath.Join(home, ".config", "ccmux")+" (kept via --keep-config)")
	}
	return p, nil
}

func runUninstall(plan *uninstallPlan) error {
	var firstErr error
	report := func(msg string, err error) {
		if err != nil {
			fmt.Printf("  ✗ %s: %v\n", msg, err)
			if firstErr == nil {
				firstErr = err
			}
		} else {
			fmt.Printf("  ✓ %s\n", msg)
		}
	}

	// Unload the launchd agent first so it doesn't relaunch the daemon
	// we're about to kill. Idempotent — quiet no-op when not installed.
	if _, err := daemonservice.Uninstall(); err == nil {
		report("unloaded launchd agent (if any)", nil)
	}

	// Stop the daemon (in case it was started manually).
	if err := exec.Command("pkill", "-TERM", "-x", "ccmuxd").Run(); err == nil {
		report("stopped ccmuxd", nil)
		time.Sleep(300 * time.Millisecond)
	} else {
		// pkill exits 1 when no matching process; not a failure.
		report("ccmuxd was not running", nil)
	}

	if plan.resetTmux {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		sessions, _ := tmux.List(ctx)
		reset := 0
		for _, s := range sessions {
			if !strings.HasPrefix(s.Name, "c-") {
				continue
			}
			_ = tmuxchrome.Reset(ctx, s.Name)
			reset++
		}
		report(fmt.Sprintf("reset tmux chrome on %d session(s)", reset), nil)
	}

	for _, path := range plan.paths {
		if err := os.RemoveAll(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			report("remove "+path, err)
		} else {
			report("removed "+path, nil)
		}
	}

	fmt.Println()
	if firstErr != nil {
		fmt.Println("Uninstall completed with errors. See above.")
	} else {
		fmt.Println("Uninstall complete.")
	}
	fmt.Println()
	fmt.Println("To finish removing related tools (optional):")
	fmt.Println("  • moshi-hook:   brew services stop moshi-hook && brew uninstall moshi-hook && brew untap rjyo/moshi")
	fmt.Println("  • zsh aliases:  remove `cc()`, `mkproj()`, `upgrade-proj()` shims from ~/.zshrc")
	fmt.Println("  • repo clone:   rm -rf " + repoCloneHint())
	return firstErr
}

// repoCloneHint guesses where the user probably cloned the repo so the
// "rm -rf <repo>" line in the post-uninstall hint isn't an empty
// placeholder. Defaults to ~/Projects/ccmux since that's our convention.
func repoCloneHint() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "<your ccmux clone>"
	}
	guess := filepath.Join(home, "Projects", "ccmux")
	if _, err := os.Stat(guess); err == nil {
		return guess
	}
	return "<your ccmux clone>"
}
