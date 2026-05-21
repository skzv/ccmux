package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

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
// the user can see what to attach to or resume. The actions stay in the
// existing verbs: `ccmux attach`, `ccmux resume`, `ccmux new`.
func newProjectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "project <name>",
		Short: "Show a project's running sessions and past conversations",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runProjectCmd(args[0])
		},
	}
}

func runProjectCmd(name string) error {
	cfg, _ := config.Load()
	root := cfg.Projects.Root
	if root == "" {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, "Projects")
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

	fmt.Printf("%s\n%s\n\n", target.Name, target.Path)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
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
		fmt.Fprintf(tw, "  %s\t%s\n", s.Name, attached)
		found++
	}
	tw.Flush()
	if found == 0 {
		fmt.Printf("  (none — `ccmux attach %s` starts one)\n", name)
	}

	// Past conversations recorded against the project folder.
	fmt.Println("\nPast conversations:")
	convs, _ := conversations.All(conversations.Options{})
	ctw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	cfound := 0
	for _, c := range convs {
		if c.Project != target.Path {
			continue
		}
		fmt.Fprintf(ctw, "  %s\t%s\t%s\n", agent.ByID(c.Agent).DisplayName(), c.ID, c.Preview)
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
