package cmd

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/conversations"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tmux"
)

// newProjectCmd: `ccmux project <name>` — the CLI mirror of pressing
// Enter on a project in the TUI Projects screen. It prints the
// project's running tmux sessions and its past agent conversations, so
// the user can see what to attach to or resume. Also exposes
// `ccmux project set-agent <project> <agent>` to mirror the Projects
// tab's `a` agent-switch binding.
func newProjectCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "project <name>",
		Short: "Show a project's running sessions and past conversations, or switch its agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runProjectCmd(args[0])
		},
	}
	c.AddCommand(newProjectSetAgentCmd())
	return c
}

// newProjectSetAgentCmd: `ccmux project set-agent <project> <agent>`
// writes <project>/.ccmux/agent atomically so subsequent launches of
// that project use the chosen coding agent.
func newProjectSetAgentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set-agent <project> <agent>",
		Short: "Set the coding agent recorded for a project (.ccmux/agent)",
		Long:  "Record which coding agent ccmux launches for <project> (agents: " + agentIDList() + "). Mirrors pressing 'a' on the Projects screen.",
		Args:  cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			root, err := cliProjectsRoot(cfg)
			if err != nil {
				return err
			}
			id, ok := agent.ParseID(args[1])
			if !ok {
				return fmt.Errorf("unknown agent %q (want %s)", args[1], agentIDList())
			}
			dir, found := resolveAttachDir(args[0], root)
			if !found {
				return fmt.Errorf("no project named %q under %s", args[0], root)
			}
			if err := project.SetAgent(dir, id); err != nil {
				return fmt.Errorf("set project agent: %w", err)
			}
			fmt.Fprintf(c.OutOrStdout(), "%s: agent → %s (%s)\n", safeField(args[0]), id, agent.ByID(id).DisplayName())
			return nil
		},
	}
}

func runProjectCmd(name string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	// Same root as `new`/`attach`: --projects wins, and a configured
	// "~/code" is expanded (the old inline fallback didn't).
	root, err := cliProjectsRoot(cfg)
	if err != nil {
		return err
	}
	projects, err := project.Discover(root)
	if err != nil {
		return fmt.Errorf("discover projects under %s: %w", root, err)
	}
	var target project.Project
	for _, p := range projects {
		if p.Name == name {
			target = p
			break
		}
	}
	if target.Path == "" {
		return fmt.Errorf("no project named %q under %s", name, root)
	}

	// Directory, session and conversation names are all external text:
	// sanitize before printing (see safeprint.go).
	fmt.Printf("%s\n%s\n\n", safeField(target.Name), safeField(target.Path))

	ctx, cancel := context.WithTimeout(context.Background(), listTmuxTimeout)
	defer cancel()

	// Running sessions whose working directory is the project folder.
	fmt.Println("Running sessions:")
	sessions, _ := tmux.List(ctx)
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	found := 0
	for _, s := range sessions {
		if s.Path != target.Path {
			continue
		}
		attached := ""
		if s.Attached {
			attached = "(attached)"
		}
		fmt.Fprintf(tw, "  %s\t%s\n", safeField(s.Name), attached)
		found++
	}
	tw.Flush()
	if found == 0 {
		// Quote the name: a project called "with space" printed a
		// hint that attached to project "with".
		fmt.Printf("  (none — `ccmux attach %s` starts one)\n", shellWord(name))
	}

	// Past conversations recorded against the project folder.
	// Honor the same headless-visibility default as `ccmux
	// list-conversations` so automation runs don't clutter the
	// per-project picker.
	fmt.Println("\nPast conversations:")
	showHeadless := cfg.Conversations.ShowHeadless
	convs, _ := conversations.All(conversations.Options{
		ExcludeHeadless: !showHeadless,
	})
	ctw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	cfound := 0
	for _, c := range convs {
		if c.Project != target.Path {
			continue
		}
		fmt.Fprintf(ctw, "  %s\t%s\t%s\n", agent.ByID(c.Agent).DisplayName(), safeField(c.ID), safeField(c.Preview))
		cfound++
	}
	ctw.Flush()
	if cfound == 0 {
		fmt.Println("  (none)")
	} else {
		fmt.Println("\nResume one with: ccmux resume <id>")
	}
	return nil
}
