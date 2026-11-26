package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tmux"
	"github.com/skzv/ccmux/internal/worktree"
)

// newWorktreeCmd: `ccmux worktree new|list|remove` — manage parallel
// Git worktrees under <project>/.worktrees/<branch> so Claude, Codex,
// and Antigravity can work on isolated branches of the same repo.
func newWorktreeCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "worktree",
		Short: "Manage Git worktrees under <project>/.worktrees/<branch> for parallel agent sessions",
	}
	c.AddCommand(
		newWorktreeNewCmd(),
		newWorktreeListCmd(),
		newWorktreeRemoveCmd(),
	)
	return c
}

func resolveWorktreeProjectDir(cfg config.Config, target string) (string, string, error) {
	if info, err := os.Stat(target); err == nil && info.IsDir() {
		abs, err := filepath.Abs(target)
		if err != nil {
			return "", "", err
		}
		return filepath.Base(abs), abs, nil
	}
	root, err := cliProjectsRoot(cfg)
	if err != nil {
		return "", "", err
	}
	dir, found := resolveAttachDir(target, root)
	if !found {
		return "", "", fmt.Errorf("no project named %q under %s", target, root)
	}
	return filepath.Base(dir), dir, nil
}

func newWorktreeNewCmd() *cobra.Command {
	var agentFlag, fromRef string
	var noAttach bool
	c := &cobra.Command{
		Use:   "new <project> <branch>",
		Short: "Create a Git worktree at <project>/.worktrees/<branch> and start an agent session",
		Args:  cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			projName, projDir, err := resolveWorktreeProjectDir(cfg, args[0])
			if err != nil {
				return err
			}
			branch := strings.TrimSpace(args[1])

			var agentID agent.ID
			if strings.TrimSpace(agentFlag) != "" {
				parsed, ok := agent.ParseID(agentFlag)
				if !ok {
					return fmt.Errorf("unknown agent %q (want %s)", agentFlag, agentIDList())
				}
				agentID = parsed
			} else {
				agentID = project.ReadAgent(projDir)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			wt, err := worktree.Create(ctx, projDir, branch, fromRef)
			if err != nil {
				return err
			}
			_ = project.SetAgent(wt.Path, agentID)

			session := worktree.SessionName(projName, wt.Branch)
			has, err := tmux.Has(ctx, session)
			if err != nil {
				return err
			}
			if !has {
				launch := agent.LaunchCmd(agentID, false, cfg.AgentCommands())
				if err := tmux.NewWithAgent(ctx, session, wt.Path, launch, string(agentID)); err != nil {
					return err
				}
			}

			fmt.Fprintf(c.OutOrStdout(), "worktree %s → %s (session %s, agent %s)\n",
				safeField(wt.Branch), safeField(wt.Path), safeField(session), agentID)
			if noAttach || !stdinIsTerminal() {
				return nil
			}
			cmd := tmux.AttachCmd(session, false)
			if os.Getenv("TMUX") != "" {
				cmd = tmux.SwitchClientCmd(session)
			}
			cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
			return cmd.Run()
		},
	}
	c.Flags().StringVar(&agentFlag, "agent", "", "agent to launch in the worktree (defaults to the project's agent)")
	c.Flags().StringVar(&fromRef, "from", "", "optional base ref/commit when creating a new branch")
	c.Flags().BoolVar(&noAttach, "no-attach", false, "create the worktree and session without attaching")
	return c
}

func newWorktreeListCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   "list <project>",
		Short: "List Git worktrees for a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			_, projDir, err := resolveWorktreeProjectDir(cfg, args[0])
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			list, err := worktree.List(ctx, projDir)
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(c.OutOrStdout())
				enc.SetEscapeHTML(false)
				return enc.Encode(list)
			}
			tw := tabwriter.NewWriter(c.OutOrStdout(), 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "BRANCH\tROLE\tPATH")
			for _, w := range list {
				role := "worktree"
				if w.Main {
					role = "main"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\n", safeField(w.Branch), role, safeField(w.Path))
			}
			return tw.Flush()
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "output as JSON")
	return c
}

func newWorktreeRemoveCmd() *cobra.Command {
	var force bool
	var keepSession bool
	c := &cobra.Command{
		Use:   "remove <project> <branch>",
		Short: "Remove a linked Git worktree and kill its tmux session",
		Args:  cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			projName, projDir, err := resolveWorktreeProjectDir(cfg, args[0])
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			removed, err := worktree.Remove(ctx, projDir, args[1], force)
			if err != nil {
				return err
			}
			if !keepSession {
				session := worktree.SessionName(projName, removed.Branch)
				if has, _ := tmux.Has(ctx, session); has {
					_ = tmux.Kill(ctx, session)
				}
			}
			fmt.Fprintf(c.OutOrStdout(), "removed worktree %s (%s)\n", safeField(removed.Branch), safeField(removed.Path))
			return nil
		},
	}
	c.Flags().BoolVar(&force, "force", false, "force removal even with uncommitted changes")
	c.Flags().BoolVar(&keepSession, "keep-session", false, "leave any matching tmux session running")
	return c
}
