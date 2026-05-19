package cmd

import (
	"context"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/skzv/ccmux/internal/clipboard"
)

// newClipboardPipeCmd registers `ccmux clipboard-pipe` — a hidden
// internal command tmux's copy-pipe-no-clear binding shells out to on
// each mouse-drag-end. It reads the selection from stdin and decides
// per-invocation whether to forward it to the OS's native clipboard
// (pbcopy / wl-copy / xclip) based on whether any local tmux client is
// currently attached.
//
// The dispatch logic and rationale live in internal/clipboard/pipe.go;
// this Cobra shim is just the entry point so the tmux binding has a
// stable path to call. Hidden because the user never runs this
// directly — like `ccmux daemon stop`, it's an implementation detail
// of the feature it powers (the clipboard route, surfaced in `ccmux
// doctor` and the setup wizard).
func newClipboardPipeCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "clipboard-pipe",
		Short:  "(internal) route tmux selection to local clipboard when a local client is attached",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			// Time-bound the whole operation: even if tmux query or
			// pbcopy hangs, we shouldn't hold tmux's copy-pipe shell
			// hostage. 3s is generous for what should take <50ms.
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			return clipboard.PipeSelection(ctx, os.Stdin, clipboard.Deps{})
		},
	}
}
