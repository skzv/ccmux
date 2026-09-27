// Command ccmux is the user-facing binary: the TUI plus its CLI subcommands.
// Run without arguments to launch the TUI.
package main

import (
	"fmt"
	"os"

	"github.com/skzv/ccmux/cmd/ccmux/cmd"
)

var version = "dev"

func main() {
	if err := cmd.Execute(version); err != nil {
		// Errors quote external text (daemon responses, file names,
		// tmux output) — print them terminal-safe.
		fmt.Fprintln(os.Stderr, "ccmux:", cmd.ErrorMessage(err))
		os.Exit(1)
	}
}
