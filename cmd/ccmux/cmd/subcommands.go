package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/scaffold"
	"github.com/skzv/ccmux/internal/tmux"
)

// newAttachCmd: `ccmux attach [project]`
// Attaches to the Claude session for the named project (or the current
// directory if none is given). If the session doesn't exist, creates it
// with `claude --continue`, falling back to fresh `claude`.
func newAttachCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "attach [project]",
		Short: "Attach to a project's Claude session (creates one if missing)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			path := "."
			if len(args) == 1 {
				path = args[0]
			}
			abs, err := filepath.Abs(path)
			if err != nil {
				return err
			}
			session := tmux.SessionNameForPath(abs)

			ctx := context.Background()
			has, err := tmux.Has(ctx, session)
			if err != nil {
				return err
			}
			if !has {
				// Match the existing cc() function's fallback chain.
				if err := tmux.New(ctx, session, abs, `claude --continue || claude || zsh`); err != nil {
					return err
				}
			}
			// Replace this process with tmux attach.
			return tmux.Attach(session)
		},
	}
}

// newNewCmd: `ccmux new <name> [-d description]` — successor to mkproj.
// Scaffolds the project, starts Claude in tmux, sends a single composite
// prompt that asks Claude to /init (cleanly — CLAUDE.md doesn't exist yet)
// and engage the user about the concept. No second consent for "overwrite
// CLAUDE.md."
func newNewCmd() *cobra.Command {
	var description string
	c := &cobra.Command{
		Use:   "new <name>",
		Short: "Scaffold a new project + start its Claude session",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			opts := scaffold.Options{Name: args[0], Description: description}
			session, err := scaffold.StartSession(context.Background(), opts)
			if err != nil {
				return err
			}
			return tmux.Attach(session)
		},
	}
	c.Flags().StringVarP(&description, "description", "d", "",
		"one-line description of what you're building (sent to Claude as the first prompt)")
	return c
}

// newUpgradeCmd: `ccmux upgrade` — inject the ccmux project structure into
// the current directory non-destructively.
func newUpgradeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "upgrade",
		Short: "Inject the ccmux project structure into the current directory",
		RunE: func(_ *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			opts := scaffold.Options{
				Name:    filepath.Base(cwd),
				Dir:     cwd,
				SkipGit: true, // existing repo: don't reinit
			}
			return scaffold.Scaffold(&opts)
		},
	}
}

// newListCmd: `ccmux list [--json]` — list sessions.
func newListCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   "list",
		Short: "List Claude sessions",
		RunE: func(_ *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			var sessions []daemon.SessionState
			if cli, err := daemon.LocalClient(); err == nil {
				if ss, e := cli.Sessions(ctx); e == nil {
					sessions = ss
				}
			}
			if sessions == nil {
				ts, err := tmux.List(ctx)
				if err != nil {
					return err
				}
				for _, t := range ts {
					sessions = append(sessions, daemon.SessionState{
						Name: t.Name, Host: "local", Path: t.Path, Windows: t.Windows, Attached: t.Attached,
						Created: t.Created, LastChange: t.LastAttach,
					})
				}
			}
			if asJSON {
				return json.NewEncoder(os.Stdout).Encode(sessions)
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tHOST\tSTATE\tPATH")
			for _, s := range sessions {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", s.Name, s.Host, s.State, s.Path)
			}
			return tw.Flush()
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "output JSON instead of a table")
	return c
}

// newKillCmd: `ccmux kill <project>`
func newKillCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "kill <project|session>",
		Short: "Kill a session by project name or full session name",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			name := args[0]
			if !strings.HasPrefix(name, "c-") {
				name = "c-" + strings.ReplaceAll(name, ".", "_")
			}
			return tmux.Kill(context.Background(), name)
		},
	}
}

// newSetupCmd: `ccmux setup` first-run wizard.
func newSetupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Interactive first-run setup wizard",
		RunE: func(_ *cobra.Command, _ []string) error {
			fmt.Println("ccmux setup wizard")
			fmt.Println("(Huh-form-based wizard arrives in the next milestone; see docs/02_Architecture/02_iOS_Mobile_Setup.md for the manual flow it will automate.)")
			return runDoctor()
		},
	}
}

// newDoctorCmd: `ccmux doctor` — non-interactive health check.
func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check that every dependency ccmux needs is installed and reachable",
		RunE: func(_ *cobra.Command, _ []string) error {
			return runDoctor()
		},
	}
}

func runDoctor() error {
	checks := []struct {
		bin, hint string
	}{
		{"tmux", "brew install tmux"},
		{"mosh", "brew install mosh"},
		{"tailscale", "https://tailscale.com/download"},
		{"claude", "https://docs.claude.com/claude-code"},
		{"rg", "brew install ripgrep (optional, accelerates notes search)"},
	}
	bad := 0
	for _, c := range checks {
		if _, err := exec.LookPath(c.bin); err != nil {
			fmt.Printf("✗ %s not on PATH — %s\n", c.bin, c.hint)
			bad++
		} else {
			fmt.Printf("✓ %s\n", c.bin)
		}
	}
	if bad > 0 {
		os.Exit(bad)
	}
	return nil
}

// newDaemonCmd: `ccmux daemon start|stop|status` — convenience over launchctl.
func newDaemonCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "daemon",
		Short: "Manage the ccmuxd background daemon",
	}
	c.AddCommand(
		&cobra.Command{
			Use:   "start",
			Short: "Start ccmuxd in the background",
			RunE: func(_ *cobra.Command, _ []string) error {
				bin, err := exec.LookPath("ccmuxd")
				if err != nil {
					return fmt.Errorf("ccmuxd not on PATH: %w (run `make install`?)", err)
				}
				dCmd := exec.Command(bin)
				dCmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
				if err := dCmd.Start(); err != nil {
					return err
				}
				fmt.Printf("ccmuxd started (pid %d)\n", dCmd.Process.Pid)
				return nil
			},
		},
		&cobra.Command{
			Use:   "status",
			Short: "Print ccmuxd status",
			RunE: func(_ *cobra.Command, _ []string) error {
				cli, err := daemon.LocalClient()
				if err != nil {
					return err
				}
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				h, err := cli.Health(ctx)
				if err != nil {
					fmt.Println("ccmuxd: offline —", err)
					os.Exit(1)
				}
				fmt.Printf("ccmuxd: online (host=%s version=%s sessions=%d sleep_mode=%s)\n",
					h.Hostname, h.Version, h.Sessions, h.SleepMode)
				return nil
			},
		},
		&cobra.Command{
			Use:   "stop",
			Short: "Stop ccmuxd",
			RunE: func(_ *cobra.Command, _ []string) error {
				out, err := exec.Command("pkill", "-x", "ccmuxd").CombinedOutput()
				if err != nil {
					return fmt.Errorf("%w (%s)", err, strings.TrimSpace(string(out)))
				}
				fmt.Println("ccmuxd stopped")
				return nil
			},
		},
	)
	return c
}

// newHostCmd: `ccmux host add|remove|list` — manage remote ccmuxd targets.
func newHostCmd() *cobra.Command {
	c := &cobra.Command{Use: "host", Short: "Manage remote ccmuxd hosts"}

	c.AddCommand(
		&cobra.Command{
			Use:   "add <name> <address>",
			Short: "Add a remote ccmuxd host",
			Args:  cobra.ExactArgs(2),
			RunE: func(_ *cobra.Command, args []string) error {
				cfg, _ := config.Load()
				cfg.Hosts = append(cfg.Hosts, config.Host{Name: args[0], Address: args[1], Mosh: true, Port: 7474})
				return config.Save(cfg)
			},
		},
		&cobra.Command{
			Use:   "remove <name>",
			Short: "Remove a remote ccmuxd host",
			Args:  cobra.ExactArgs(1),
			RunE: func(_ *cobra.Command, args []string) error {
				cfg, _ := config.Load()
				out := cfg.Hosts[:0]
				for _, h := range cfg.Hosts {
					if h.Name != args[0] {
						out = append(out, h)
					}
				}
				cfg.Hosts = out
				return config.Save(cfg)
			},
		},
		&cobra.Command{
			Use:   "list",
			Short: "List configured remote hosts",
			RunE: func(_ *cobra.Command, _ []string) error {
				cfg, _ := config.Load()
				tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
				fmt.Fprintln(tw, "NAME\tADDRESS\tUSER\tMOSH")
				for _, h := range cfg.Hosts {
					fmt.Fprintf(tw, "%s\t%s\t%s\t%v\n", h.Name, h.Address, h.User, h.Mosh)
				}
				return tw.Flush()
			},
		},
	)
	return c
}

// Scaffolding logic now lives in internal/scaffold so the TUI and the CLI
// share one implementation.
