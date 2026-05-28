package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/skzv/ccmux/internal/conversations"
)

// newDeleteConversationCmd: `ccmux delete-conversation <id>` is the CLI
// mirror of the Conversations screen's `x` action. It removes a past
// conversation's transcript from disk — irreversible, so the id is a
// required positional argument (no "delete the most recent" shortcut,
// unlike `ccmux resume`).
//
// Per CLAUDE.md's feature-surface policy: the TUI delete needs a
// scriptable equivalent.
func newDeleteConversationCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:     "delete-conversation <id>",
		Aliases: []string{"rm-conversation"},
		Short:   "Delete a past conversation's transcript (irreversible)",
		Long: `Delete a past conversation's transcript file(s) from disk.

This is irreversible: the transcript is gone and the conversation can
no longer be resumed. Use ` + "`ccmux list-conversations`" + ` to find
the id.

Prints a confirmation prompt unless --force is given.`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			id := args[0]
			list, err := conversations.All(conversations.Options{})
			if err != nil {
				return fmt.Errorf("list conversations: %w", err)
			}
			target := pickByID(list, id)
			if target.ID == "" {
				return fmt.Errorf("no conversation with id %q (use `ccmux list-conversations` to list)", id)
			}

			if !force {
				fmt.Printf("Delete %s conversation %s?\n", target.Agent, target.ID)
				if target.Preview != "" {
					fmt.Printf("  %q\n", target.Preview)
				}
				fmt.Printf("  %s\n", target.Path)
				for _, path := range target.Paths {
					if path != target.Path {
						fmt.Printf("  %s\n", path)
					}
				}
				fmt.Print("This cannot be undone. Type 'yes' to confirm: ")
				var answer string
				_, _ = fmt.Scanln(&answer)
				if answer != "yes" {
					fmt.Println("Aborted.")
					return nil
				}
			}

			if err := conversations.Delete(target); err != nil {
				return err
			}
			fmt.Printf("Deleted %s conversation %s\n", target.Agent, target.ID)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "skip the confirmation prompt")
	return cmd
}
