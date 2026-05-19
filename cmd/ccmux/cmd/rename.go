package cmd

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/spf13/cobra"

	"github.com/skzv/ccmux/internal/tmux"
)

// validSessionName matches tmux session names: alphanumeric, hyphen, underscore, dot.
var validSessionName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// newRenameCmd: `ccmux rename <old-name> <new-name>` — renames a tmux session.
func newRenameCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rename <old-name> <new-name>",
		Short: "Rename a tmux session",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			oldName, newName := args[0], args[1]
			if !validSessionName.MatchString(newName) {
				return fmt.Errorf("invalid session name %q: use only letters, digits, hyphens, underscores, dots", newName)
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
