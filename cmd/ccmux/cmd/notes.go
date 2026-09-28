package cmd

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
)

// defaultTailnetPort is the ccmuxd HTTP port used when neither the host
// entry nor config.Daemon.TailnetPort specifies one. Mirrors the default
// the TUI's refresh fan-out applies.
const defaultTailnetPort = 7474

// resolveNotesAddr maps a `--host` value to a daemon address. An empty
// host means the local device (local=true, addr=""). A named host is
// looked up in cfg.Hosts and resolved to "<address>:<port>". Unknown
// names are an error so a typo doesn't silently fall back to local.
func resolveNotesAddr(cfg config.Config, host string) (addr string, local bool, err error) {
	if host == "" {
		return "", true, nil
	}
	for _, h := range cfg.Hosts {
		if h.Name == host {
			port := h.Port
			if port == 0 {
				port = cfg.Daemon.TailnetPort
			}
			if port == 0 {
				port = defaultTailnetPort
			}
			return fmt.Sprintf("%s:%d", h.Address, port), false, nil
		}
	}
	return "", false, fmt.Errorf("unknown host %q — configure it with `ccmux host add`", host)
}

// notesClientFor returns a daemon client for the given host, and
// whether it is this device's own ccmuxd.
func notesClientFor(cfg config.Config, host string) (*daemon.Client, bool, error) {
	addr, local, err := resolveNotesAddr(cfg, host)
	if err != nil {
		return nil, false, err
	}
	if local {
		cli, err := daemon.LocalClient()
		if err != nil {
			return nil, false, fmt.Errorf("local daemon: %w", err)
		}
		return cli, true, nil
	}
	return daemon.RemoteClient(addr), false, nil
}

// notesErr explains a failed notes call. When the local ccmuxd isn't
// running it says so and how to start it, as every other command does
// (it used to print the raw "connection refused"); anything else, and
// any error from a peer, is reported as is.
func notesErr(local bool, err error) error {
	if local {
		if down := daemonDownErr(err); down != nil {
			return down
		}
	}
	return err
}

// newNotesCmd: `ccmux notes {list,read,search}` — cross-device access to
// a project's markdown notes. The `--host` flag selects a configured
// peer; without it the command targets the local device. The CLI mirror
// of the TUI Notes screen's device toggle (feature-surface policy).
func newNotesCmd() *cobra.Command {
	var host string

	parent := &cobra.Command{
		Use:   "notes",
		Short: "Browse a project's notes on this or another device",
		Long: "List, read, and search a project's markdown notes. With --host, " +
			"operates against a configured remote ccmux device over the tailnet.",
	}
	parent.PersistentFlags().StringVar(&host, "host", "",
		"configured host name to query (default: this device)")

	list := &cobra.Command{
		Use:   "list <project>",
		Short: "List the markdown files in a project's notes vault",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			cfg, _ := config.Load()
			cli, local, err := notesClientFor(cfg, host)
			if err != nil {
				return err
			}
			entries, err := cli.Notes(ctx, args[0])
			if err != nil {
				return notesErr(local, err)
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "REL\tDIR\tMODIFIED")
			for _, e := range entries {
				// File names come from the (possibly remote) vault:
				// sanitize them (see safeprint.go).
				fmt.Fprintf(tw, "%s\t%s\t%s\n", safeField(e.Rel), safeField(e.Dir), e.Modified.Format("2006-01-02 15:04"))
			}
			return tw.Flush()
		},
	}

	read := &cobra.Command{
		Use:   "read <project> <file>",
		Short: "Print the contents of one note (project-relative path)",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			cfg, _ := config.Load()
			cli, local, err := notesClientFor(cfg, host)
			if err != nil {
				return err
			}
			nc, err := cli.NoteContent(ctx, args[0], args[1])
			if err != nil {
				return notesErr(local, err)
			}
			// The daemon strips control sequences from note bodies,
			// but a peer running an older ccmuxd doesn't.
			fmt.Print(safeText(nc.Content))
			return nil
		},
	}

	search := &cobra.Command{
		Use:   "search <project> <query>",
		Short: "Search a project's notes for literal text (case-insensitive)",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			cfg, _ := config.Load()
			cli, local, err := notesClientFor(cfg, host)
			if err != nil {
				return err
			}
			hits, err := cli.SearchNotes(ctx, args[0], args[1])
			if err != nil {
				return notesErr(local, err)
			}
			for _, h := range hits {
				fmt.Printf("%s:%d: %s\n", safeField(h.Rel), h.LineNum, safeField(h.Snippet))
			}
			return nil
		},
	}

	parent.AddCommand(list, read, search)
	return parent
}
