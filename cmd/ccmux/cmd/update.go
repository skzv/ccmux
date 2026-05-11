// `ccmux update` — pull the latest ccmux from its git checkout, rebuild,
// install to ~/.local/bin, and reload the daemon. The flow assumes the
// user installed via `git clone + make install`, which is the documented
// path; for binary-distribution we'd publish releases and swap this for a
// download step.
package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/skzv/ccmux/internal/daemonservice"
)

// newUpdateCmd: `ccmux update [--repo PATH] [--no-restart] [--dry-run]`.
func newUpdateCmd() *cobra.Command {
	var (
		repoFlag   string
		noRestart  bool
		dryRun     bool
		skipPull   bool
		runSetup   bool
		noSetupPrompt bool
	)
	c := &cobra.Command{
		Use:   "update",
		Short: "Pull latest, rebuild, install, and reload the daemon",
		Long: `Locates the ccmux git checkout (the running binary's repo, falling
back to ~/Projects/ccmux), runs git pull --ff-only, make install, then
restarts the daemon under launchd/systemd so the new binary takes effect.

After a successful update ccmux offers to re-run the setup wizard so
new config options introduced upstream (server mode toggle, new
prompts) can be reviewed. Pass --setup to skip the prompt and run
setup automatically, or --no-setup-prompt to skip the prompt and
NOT run setup.

Use --dry-run to preview the commands without executing them.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			repo, err := resolveRepo(repoFlag)
			if err != nil {
				return err
			}
			fmt.Printf("ccmux update: using checkout %s\n", repo)

			if !skipPull {
				if err := runStep(repo, dryRun, "git", "pull", "--ff-only"); err != nil {
					return err
				}
			}
			if err := runStep(repo, dryRun, "make", "install"); err != nil {
				return err
			}
			if noRestart {
				fmt.Println("✓ binaries updated; --no-restart skipped daemon reload")
				return nil
			}
			if dryRun {
				fmt.Println("[dry-run] would restart ccmuxd via daemonservice.Restart()")
				return nil
			}
			if _, err := daemonservice.Restart(); err != nil {
				fmt.Printf("warning: daemon restart failed: %v\n", err)
				fmt.Println("you can restart manually with `ccmux daemon install` (or launchctl/systemctl).")
				return nil
			}
			fmt.Println("✓ ccmuxd restarted")
			fmt.Println("✓ ccmux updated. Restart any open TUIs to pick up the new binary.")

			// Offer to re-run setup. New ccmux versions sometimes add
			// config prompts (e.g. server mode); the user who just
			// upgraded probably wants the chance to answer the new
			// questions without remembering they exist.
			if runSetup || (!noSetupPrompt && promptYesNo("Re-run `ccmux setup` to review any new options?")) {
				fmt.Println()
				ccmuxBin, err := os.Executable()
				if err != nil {
					ccmuxBin = "ccmux"
				}
				setup := exec.Command(ccmuxBin, "setup")
				setup.Stdin = os.Stdin
				setup.Stdout = os.Stdout
				setup.Stderr = os.Stderr
				if err := setup.Run(); err != nil {
					return fmt.Errorf("ccmux setup: %w", err)
				}
			}
			return nil
		},
	}
	c.Flags().StringVar(&repoFlag, "repo", "", "path to the ccmux git checkout (default: auto-detect)")
	c.Flags().BoolVar(&noRestart, "no-restart", false, "don't restart the daemon after install")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "print the commands that would run, without executing them")
	c.Flags().BoolVar(&skipPull, "skip-pull", false, "skip git pull (just rebuild and reinstall)")
	c.Flags().BoolVar(&runSetup, "setup", false, "run `ccmux setup` after a successful update (skips the prompt)")
	c.Flags().BoolVar(&noSetupPrompt, "no-setup-prompt", false, "don't ask about re-running setup")
	return c
}

// promptYesNo blocks on a single y/N answer. Defaults to no on empty
// input or non-interactive stdin so scripted `ccmux update` calls
// don't pause for input.
func promptYesNo(question string) bool {
	if fi, err := os.Stdin.Stat(); err != nil || (fi.Mode()&os.ModeCharDevice) == 0 {
		// Not a terminal — don't prompt.
		return false
	}
	fmt.Printf("\n? %s [y/N] ", question)
	var reply string
	_, _ = fmt.Scanln(&reply)
	return len(reply) > 0 && (reply[0] == 'y' || reply[0] == 'Y')
}

// resolveRepo finds the ccmux git checkout. Explicit --repo wins; then
// walk ancestors of the running binary looking for a .git directory;
// finally fall back to ~/Projects/ccmux. Returns an absolute path and a
// human-readable error if nothing is usable.
func resolveRepo(explicit string) (string, error) {
	if explicit != "" {
		return validateRepo(explicit)
	}
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		if root := findGitRoot(filepath.Dir(exe)); root != "" {
			return root, nil
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		guess := filepath.Join(home, "Projects", "ccmux")
		if p, err := validateRepo(guess); err == nil {
			return p, nil
		}
	}
	return "", errors.New("could not find ccmux git checkout; pass --repo PATH")
}

// validateRepo confirms `path` is an absolute directory that contains
// both a .git entry and a Makefile (so `make install` won't trip on
// something that's a checkout of a different project).
func validateRepo(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if !looksLikeCcmuxRepo(abs) {
		return "", fmt.Errorf("%s doesn't look like the ccmux checkout (no .git or no Makefile)", abs)
	}
	return abs, nil
}

func looksLikeCcmuxRepo(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, "Makefile")); err != nil {
		return false
	}
	return true
}

// findGitRoot walks up from `start` looking for the first directory
// that contains both a .git entry and a Makefile. Returns "" if none
// found before the filesystem root.
func findGitRoot(start string) string {
	dir := start
	for {
		if looksLikeCcmuxRepo(dir) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// runStep runs a single subcommand in `cwd`, streaming its output to
// the user's terminal so they see git/make progress live. Returns the
// command's error so the caller bails on the first failed step.
func runStep(cwd string, dryRun bool, name string, args ...string) error {
	display := name
	for _, a := range args {
		display += " " + a
	}
	if dryRun {
		fmt.Printf("[dry-run] (in %s) %s\n", cwd, display)
		return nil
	}
	fmt.Printf("→ (in %s) %s\n", cwd, display)
	cmd := exec.Command(name, args...)
	cmd.Dir = cwd
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", display, err)
	}
	return nil
}
