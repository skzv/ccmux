package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/skzv/ccmux/internal/tmux"
)

// newRenameCmd: `ccmux rename <old-name> <new-name>` — renames a tmux session.
func newRenameCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rename <old-name> <new-name>",
		Short: "Rename a tmux session",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			oldName, newName := args[0], args[1]
			// The old name goes into a tmux target: the daemon's rule
			// (tmux.ValidTarget). `rename '$4' x` renamed whichever
			// session had ID $4.
			if err := tmux.CheckTarget(oldName); err != nil {
				return fmt.Errorf("refusing to rename %q: %w", oldName, err)
			}
			// Same rule as the TUI rename form (tmux.ValidSessionName),
			// which is stricter than ValidTarget.
			if !tmux.ValidSessionName(newName) {
				return fmt.Errorf("invalid session name %q: use only letters, digits, hyphens and underscores, not starting with a hyphen", newName)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := tmux.Rename(ctx, oldName, newName); err != nil {
				return err
			}
			fmt.Printf("renamed %s → %s\n", oldName, newName)
			return nil
		},
	}
}
