package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/project"
)

// newAdoptCmd: `ccmux adopt <name-or-path>` — registers an existing
// directory with ccmux so the dashboard surfaces it. CLI mirror of the
// Projects-screen `A` key.
//
// `name-or-path` may be either a bare directory name (resolved against
// the configured projects root, e.g. `ccmux adopt qc` → `~/Projects/qc`)
// or an absolute / explicit relative path (e.g. `ccmux adopt ./scratch`
// or `/srv/work/legacy-app`). The bare-name form is the common case and
// matches how the TUI's modal labels orphans.
func newAdoptCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "adopt <name-or-path>",
		Short: "Register an existing directory under your projects root as a ccmux project",
		Long: `Register an existing directory so ccmux surfaces it.

ccmux normally only discovers directories that contain a .git, a CLAUDE.md,
or a previously-written .ccmux/ marker. Use adopt for directories that
fit none of those but should still appear in the Projects screen — for
example, a scratch directory or an extracted tarball.

The argument can be a bare project name (looked up under the configured
projects root) or any explicit path.`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runAdoptCmd(args[0])
		},
	}
}

func runAdoptCmd(target string) error {
	path, err := resolveAdoptTarget(target)
	if err != nil {
		return err
	}
	if err := project.Adopt(path); err != nil {
		return err
	}
	// Re-inspect so the printed summary matches what Discover would
	// surface — saves the user from having to launch the TUI just to
	// confirm the marker landed.
	if p, ok := project.Lookup(path); ok {
		fmt.Printf("adopted %s\n  path:    %s\n", p.Name, p.Path)
		if p.HasGit || p.HasCM {
			// The directory already qualified via .git or CLAUDE.md;
			// adoption is a no-op for the dashboard but still
			// creates the marker (idempotently).
			fmt.Println("  note:    already a project — .ccmux/ marker re-written for completeness")
		}
		return nil
	}
	fmt.Printf("adopted %s\n", path)
	return nil
}

// resolveAdoptTarget turns the user's argument into an absolute path.
// A path-like argument (contains a separator or starts with `.`/`~`) is
// resolved as-is; a bare name is looked up under the configured projects
// root. Mirrors the TUI's "type a name in the modal" UX.
func resolveAdoptTarget(target string) (string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", errors.New("adopt: name or path is required")
	}
	if filepath.IsAbs(target) || strings.ContainsRune(target, os.PathSeparator) ||
		strings.HasPrefix(target, ".") || strings.HasPrefix(target, "~") {
		expanded, err := expandTildePath(target)
		if err != nil {
			return "", err
		}
		return filepath.Abs(expanded)
	}
	cfg, _ := config.Load()
	root := cfg.Projects.Root
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("adopt: locate home dir: %w", err)
		}
		root = filepath.Join(home, "Projects")
	}
	root, err := expandTildePath(root)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, target), nil
}

// expandTildePath handles a leading "~/" so users can pass
// `ccmux adopt ~/work/legacy` without the shell expanding nothing.
func expandTildePath(p string) (string, error) {
	if !strings.HasPrefix(p, "~") {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("adopt: locate home dir: %w", err)
	}
	if p == "~" {
		return home, nil
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:]), nil
	}
	// `~user` style isn't supported; fall through unchanged so a path
	// like `~unknown` surfaces the natural "no such directory" later.
	return p, nil
}
