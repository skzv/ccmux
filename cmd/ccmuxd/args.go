package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
)

// handleArgs processes ccmuxd's command line, which takes no arguments
// besides --version and --help. ccmuxd used to ignore os.Args
// entirely, so `ccmuxd --version` (which the brew smoke test runs)
// quietly started a full daemon instead of printing anything. Reports
// whether the process should exit now, and with which code; exit=false
// means "start the daemon".
func handleArgs(args []string, stdout, stderr io.Writer) (exit bool, code int) {
	fs := flag.NewFlagSet("ccmuxd", flag.ContinueOnError)
	fs.SetOutput(stderr)
	printVer := fs.Bool("version", false, "print version and exit")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: ccmuxd [--version]")
		fmt.Fprintln(stderr, "ccmuxd is the ccmux background daemon. Manage it with `ccmux daemon start|stop|status`.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return true, 0
		}
		return true, 2
	}
	if *printVer {
		fmt.Fprintln(stdout, "ccmuxd", version)
		return true, 0
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "ccmuxd: unexpected argument %q\n", fs.Arg(0))
		fs.Usage()
		return true, 2
	}
	return false, 0
}
