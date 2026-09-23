package cmd

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/spf13/cobra"

	"github.com/skzv/ccmux/internal/tmux"
)

// validSessionName matches the session names ccmux creates and the
// daemon accepts: letters, digits, "_" and "-", not starting with "-"
// (it would read as a flag in `ccmux kill <name>`). No "." — tmux
// versions differ on whether they keep it or rewrite it to "_", so a
// dotted rename could leave the session under a name nobody asked for,
// and the daemon rejects dotted names for the same reason.
var validSessionName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_-]*$`)

// newRenameCmd: `ccmux rename <old-name> <new-name>` — renames a tmux session.
func newRenameCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rename <old-name> <new-name>",
		Short: "Rename a tmux session",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			oldName, newName := args[0], args[1]
			if !validSessionName.MatchString(newName) {
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
