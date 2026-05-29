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

// notesClientFor returns a daemon client for the given host plus the
// project-host label to report in output ("local" or the host name).
func notesClientFor(cfg config.Config, host string) (*daemon.Client, string, error) {
	addr, local, err := resolveNotesAddr(cfg, host)
	if err != nil {
		return nil, "", err
	}
	if local {
		cli, err := daemon.LocalClient()
		if err != nil {
			return nil, "", fmt.Errorf("local daemon: %w", err)
		}
		return cli, "local", nil
	}
	return daemon.RemoteClient(addr), host, nil
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
			cli, _, err := notesClientFor(cfg, host)
			if err != nil {
				return err
			}
			entries, err := cli.Notes(ctx, args[0])
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "REL\tDIR\tMODIFIED")
			for _, e := range entries {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", e.Rel, e.Dir, e.Modified.Format("2006-01-02 15:04"))
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
			cli, _, err := notesClientFor(cfg, host)
			if err != nil {
				return err
			}
			nc, err := cli.NoteContent(ctx, args[0], args[1])
			if err != nil {
				return err
			}
			fmt.Print(nc.Content)
			return nil
		},
	}

	search := &cobra.Command{
		Use:   "search <project> <query>",
		Short: "Search a project's notes for a query",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			cfg, _ := config.Load()
			cli, _, err := notesClientFor(cfg, host)
			if err != nil {
				return err
			}
			hits, err := cli.SearchNotes(ctx, args[0], args[1])
			if err != nil {
				return err
			}
			for _, h := range hits {
				fmt.Printf("%s:%d: %s\n", h.Rel, h.LineNum, h.Snippet)
			}
			return nil
		},
	}

	parent.AddCommand(list, read, search)
	return parent
}
