package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/clipboard"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/daemonservice"
	"github.com/skzv/ccmux/internal/ghauth"
	"github.com/skzv/ccmux/internal/moshi"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/scaffold"
	"github.com/skzv/ccmux/internal/setupwizard"
	"github.com/skzv/ccmux/internal/tmux"
)

// newAttachCmd: `ccmux attach [project]`
// Attaches to the named project's agent session (or the current
// directory if none is given). If the session doesn't exist, creates
// it via the agent's LaunchCmd(continue=true) — Claude by default, or
// whichever agent the project's .ccmux/agent sidecar records.
func newAttachCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "attach [project]",
		Short: "Attach to a project's agent session (creates one if missing)",
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
			created := false
			if !has {
				// Resolve the launch command from the project's
				// sidecar so an Antigravity-tagged project doesn't
				// silently boot into claude.
				cfg, _ := config.Load()
				launch := agent.LaunchCmd(project.ReadAgent(abs), true, cfg.AgentCommands())
				if err := tmux.New(ctx, session, abs, launch); err != nil {
					return err
				}
				created = true
			}
			// Replace this process with tmux attach, applying ccmux
			// chrome first so a CLI-spawned session looks the same as a
			// TUI/daemon-spawned one.
			return attachWithChrome(session, filepath.Base(abs), detachOthersForAttachIntent(created))
		},
	}
}

// attachDetachOthers loads the user's config and reports whether an
// attach should detach other clients ("exclusive" mode). A missing or
// unreadable config falls back to mirror mode (false) — the default —
// because that's the less-destructive choice when we can't be sure.
func attachDetachOthers() bool {
	cfg, err := config.Load()
	if err != nil {
		return false
	}
	return cfg.Sessions.DetachOthersOnAttach()
}

func detachOthersForAttachIntent(created bool) bool {
	if created {
		return false
	}
	return attachDetachOthers()
}

// newNewCmd: `ccmux new <name> [--agent <id>]` — create a project
// directory and start its agent session.
//
// It deliberately does NOT scaffold the project: no CLAUDE.md, no
// docs/ tree, no .gitignore, no git init. ccmux just makes the
// directory and launches the agent; run `/init`, `openspec`, or
// `git init` yourself inside the session.
func newNewCmd() *cobra.Command {
	var agentFlag string
	c := &cobra.Command{
		Use:   "new <name>",
		Short: "Create a project directory and start its agent session",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			opts := scaffold.Options{Name: args[0]}
			cfg, _ := config.Load()
			opts.Commands = cfg.AgentCommands()
			if agentFlag != "" {
				id, ok := agent.ParseID(agentFlag)
				if !ok {
					return fmt.Errorf("unknown agent %q (want claude, codex, antigravity, or cursor)", agentFlag)
				}
				opts.Agent = id
			}
			session, err := scaffold.StartSession(context.Background(), opts)
			if err != nil {
				return err
			}
			return attachWithChrome(session, args[0], false)
		},
	}
	c.Flags().StringVar(&agentFlag, "agent", "",
		"agent to launch: claude, codex, antigravity, or cursor (default claude)")
	return c
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
			// A bare project name/path is resolved to its session name
			// through the same sanitizer `ccmux attach` and the daemon
			// use (tmux.SessionNameForPath) — a weaker dots-only rewrite
			// here would build the wrong target for any project whose
			// name contains a space, colon, or other special character.
			if !strings.HasPrefix(name, "c-") {
				name = tmux.SessionNameForPath(name)
			}
			return tmux.Kill(context.Background(), name)
		},
	}
}

// newSetupCmd: `ccmux setup` first-run wizard. Idempotent — re-running
// just verifies what's already done and prompts only for missing
// pieces.
func newSetupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Interactive first-run setup wizard",
		RunE: func(_ *cobra.Command, _ []string) error {
			return setupwizard.Run(context.Background(), os.Stdout)
		},
	}
}

// printDoctorDetail writes a captured diagnostic (a command's stderr, a
// timeout note, etc.) under a doctor status line — each line indented
// and arrow-prefixed so a failure shows *why*, not just a bare "·".
func printDoctorDetail(detail string) {
	detail = strings.TrimSpace(detail)
	if detail == "" {
		return
	}
	for _, ln := range strings.Split(detail, "\n") {
		fmt.Println("      ↳ " + ln)
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

func configuredDoctorCommand(cfg config.Config, id agent.ID) string {
	switch id {
	case agent.IDClaude:
		return strings.TrimSpace(cfg.Agents.Claude.Command)
	case agent.IDCodex:
		return strings.TrimSpace(cfg.Agents.Codex.Command)
	case agent.IDAntigravity:
		return strings.TrimSpace(cfg.Agents.Antigravity.Command)
	case agent.IDCursor:
		return strings.TrimSpace(cfg.Agents.Cursor.Command)
	default:
		return ""
	}
}

func printAgentCommandDoctor(cfg config.Config, a agent.Agent, candidates []string) {
	configured := configuredDoctorCommand(cfg, a.ID())
	if configured != "" {
		fmt.Printf("      configured: %s\n", configured)
	}
	if len(candidates) > 0 {
		fmt.Printf("      PATH first:  %s\n", candidates[0])
	}
	if len(candidates) > 1 {
		fmt.Printf("      all %s commands:\n", a.DisplayName())
		for _, p := range candidates {
			fmt.Println("        - " + p)
		}
		if configured == "" {
			fmt.Printf("      ⚠ multiple %s installs found; run `ccmux setup` to pin one\n", a.DisplayName())
		}
	}
	if configured != "" {
		found := false
		for _, p := range candidates {
			if p == configured {
				found = true
				break
			}
		}
		if !found {
			fmt.Printf("      ⚠ configured %s command is not on this process PATH\n", a.DisplayName())
		}
	}
}

func runDoctor() error {
	// Windows runs ccmux inside WSL2 today (native tmux doesn't exist;
	// see docs/04_Guides/Windows.md). When the user runs `ccmux doctor`
	// on bare Windows, point them at WSL before we try shell-tool
	// checks that will all fail anyway.
	if runtime.GOOS == "windows" {
		fmt.Println("⚠ Native Windows is not currently supported. Recommended path:")
		fmt.Println("  1. Install WSL2:                   wsl --install")
		fmt.Println("  2. Inside Ubuntu (or your distro): sudo apt install tmux mosh git ripgrep")
		fmt.Println("  3. Then run `ccmux setup` inside WSL — it'll behave like Linux.")
		fmt.Println()
		fmt.Println("Tracking native Windows in docs/04_Guides/Windows.md.")
		return nil
	}
	hintFor := func(macos, linux string) string {
		if runtime.GOOS == "linux" {
			return linux
		}
		return macos
	}
	checks := []struct {
		bin, hint string
	}{
		{"tmux", hintFor("brew install tmux", "apt/dnf/pacman install tmux")},
		{"mosh", hintFor("brew install mosh", "apt/dnf/pacman install mosh")},
		{"tailscale", "https://tailscale.com/download"},
		{"rg", hintFor("brew install ripgrep (optional, accelerates notes search)", "apt install ripgrep (optional)")},
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

	// AI agents block. At least one must be installed for ccmux to
	// be useful — without an agent there's nothing to put in the tmux
	// pane the dashboard supervises. We don't require every agent; a
	// Claude-only user has every feature, and a Codex-only user has
	// the same with a different agent.
	fmt.Println()
	fmt.Println("AI agents (need at least one):")
	cfg, _ := config.Load()
	installedCount := 0
	for _, a := range agent.All() {
		candidates := agent.Candidates(a)
		configured := configuredDoctorCommand(cfg, a.ID())
		configuredExists := configured != "" && agent.Executable(configured)
		if len(candidates) == 0 && !configuredExists {
			fmt.Printf("  · %s (binary `%s` not on PATH) — %s\n",
				a.DisplayName(), a.Binary(), agentInstallHint(a.ID()))
		} else {
			fmt.Printf("  ✓ %s (%s)\n", a.DisplayName(), a.Binary())
			printAgentCommandDoctor(cfg, a, candidates)
			installedCount++
		}
	}
	if installedCount == 0 {
		bad++
		fmt.Println("  ⚠ no agents installed — install at least one above to use ccmux.")
	}

	// PATH check for ccmux itself. macOS-default PATH doesn't include
	// ~/.local/bin, so a fresh `make install` works but `ccmux` doesn't
	// resolve until the user adds it. This was a real onboarding bug.
	fmt.Println()
	fmt.Println("ccmux on PATH:")
	if _, err := exec.LookPath("ccmux"); err != nil {
		bad++
		home, _ := os.UserHomeDir()
		want := filepath.Join(home, ".local", "bin")
		fmt.Printf("  ✗ %s not on $PATH — run `ccmux setup` (it'll add a managed block to your shell rc) or add manually:\n", want)
		fmt.Println(`    echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshrc && source ~/.zshrc`)
	} else {
		fmt.Println("  ✓ ccmux resolves on $PATH")
	}

	// gh CLI block — recommended but not required. ccmux itself doesn't
	// touch GitHub, but agents lean on `gh` to create and push repos,
	// so an authed gh makes that smoother.
	fmt.Println()
	fmt.Println("GitHub CLI (recommended — agents use it to create/push repos):")
	gh := ghauth.Detect(context.Background())
	switch gh.State {
	case ghauth.StateAuthed:
		who := gh.User
		if who == "" {
			who = "(login parsed empty, but gh auth status is happy)"
		}
		fmt.Printf("  ✓ gh authenticated as %s\n", who)
	case ghauth.StateNotAuthed:
		fmt.Println("  · " + gh.Hint())
		printDoctorDetail(gh.Detail)
	case ghauth.StateMissing:
		fmt.Println("  · " + gh.Hint())
	case ghauth.StateUnknown:
		fmt.Println("  · gh auth couldn't be verified")
		printDoctorDetail(gh.Detail)
	}

	// Moshi / moshi-hook block (optional but the recommended mobile path).
	ms := moshi.Detect(context.Background())
	fmt.Println()
	fmt.Println("Moshi (mobile push notifications):")
	switch {
	case !ms.BinaryInstalled:
		fmt.Println("  · moshi-hook not installed — run `ccmux moshi-setup` to add it")
	case ms.StatusErr != nil:
		fmt.Println("  · moshi-hook installed but pairing couldn't be verified")
		printDoctorDetail(ms.StatusErr.Error())
	case !ms.Paired:
		fmt.Println("  · moshi-hook installed but not paired — `ccmux moshi-setup` to pair")
	case !ms.HooksInstalled:
		fmt.Println("  ⚠ moshi-hook paired but Claude Code hooks not wired — run `moshi-hook install`")
	case ms.ServiceErr != nil:
		fmt.Println("  ⚠ moshi-hook paired + wired, but the service check failed — couldn't verify")
		printDoctorDetail(ms.ServiceErr.Error())
	case !ms.ServiceRunning:
		fmt.Println("  ⚠ moshi-hook wired but not running as a service — `brew services start moshi-hook`")
	default:
		fmt.Println("  ✓ moshi-hook installed, paired, hooks wired, service running")
	}

	// Clipboard block — whether OSC 52 will round-trip between this
	// terminal and tmux. The common breaker is Terminal.app (no OSC 52
	// support) or iTerm2 with the "Applications may access clipboard"
	// box unchecked.
	fmt.Println()
	fmt.Println("Clipboard (cross-device copy/paste via OSC 52):")
	checkClipboardForDoctor()

	if bad > 0 {
		os.Exit(bad)
	}
	return nil
}

// agentInstallHint returns the recommended install command for an
// agent the user doesn't have yet. All three CLIs ship via npm today,
// which keeps the matrix simple — if any of them switch to a native
// installer (claude is contemplating one), update here.
func agentInstallHint(id agent.ID) string {
	switch id {
	case agent.IDClaude:
		return "https://docs.claude.com/claude-code or `npm i -g @anthropic-ai/claude-code`"
	case agent.IDCodex:
		return "`npm i -g @openai/codex` (or see codex docs)"
	case agent.IDAntigravity:
		return "`curl -fsSL https://antigravity.google/cli/install.sh | bash` (or see antigravity docs)"
	case agent.IDCursor:
		return "`curl https://cursor.com/install -fsS | bash` (or see cursor docs)"
	}
	return ""
}

// checkClipboardForDoctor prints the three lines of clipboard status
// (terminal compat, tmux set-clipboard, and a probe hint). Split out
// of runDoctor so it can be reused by the setup wizard.
func checkClipboardForDoctor() {
	ts := clipboard.DetectTerminal()
	switch {
	case ts.Supported && ts.NeedsToggle != "":
		fmt.Printf("  ✓ %s supports OSC 52 — make sure: %s\n", ts.Name, ts.NeedsToggle)
	case ts.Supported:
		fmt.Printf("  ✓ %s supports OSC 52\n", ts.Name)
	default:
		fmt.Printf("  ⚠ %s — %s\n", ts.Name, ts.Advice)
	}
	state, err := clipboard.TmuxClipboardState(context.Background())
	switch {
	case err != nil:
		fmt.Println("  · tmux not running yet; ccmuxd will enable set-clipboard on first session")
	case state == "on" || state == "external":
		fmt.Printf("  ✓ tmux set-clipboard=%s\n", state)
	default:
		fmt.Printf("  ⚠ tmux set-clipboard=%s — selections won't escape tmux; run `tmux set -s set-clipboard on`\n", state)
	}
}

// newDaemonCmd: `ccmux daemon ...` — start/stop, persistent install/
// uninstall, and status.
func newDaemonCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "daemon",
		Short: "Manage the ccmuxd background daemon",
	}
	c.AddCommand(
		&cobra.Command{
			Use:   "start",
			Short: "Start ccmuxd in the background for this login session",
			RunE: func(_ *cobra.Command, _ []string) error {
				bin, err := exec.LookPath("ccmuxd")
				if err != nil {
					return fmt.Errorf("ccmuxd not on PATH: %w (run `make install`?)", err)
				}
				dCmd := exec.Command(bin)
				detachProcess(dCmd) // OS-specific: setsid on unix, DETACHED_PROCESS on windows
				if err := dCmd.Start(); err != nil {
					return err
				}
				fmt.Printf("ccmuxd started (pid %d)\n", dCmd.Process.Pid)
				return nil
			},
		},
		&cobra.Command{
			Use:   "status",
			Short: "Print ccmuxd status and service registration state",
			RunE: func(_ *cobra.Command, _ []string) error {
				svc := daemonservice.Probe()
				switch svc.OS {
				case "darwin":
					fmt.Printf("service file:    %s (launchd plist)\n", svc.ServicePath)
				case "linux":
					fmt.Printf("service file:    %s (systemd-user unit)\n", svc.ServicePath)
				default:
					fmt.Printf("OS:              %s (no auto-install path)\n", svc.OS)
				}
				if svc.ServiceExists {
					fmt.Println("file exists:     yes")
				} else {
					fmt.Println("file exists:     no — run `ccmux daemon install` to persist across reboots")
				}
				if svc.ServiceEnabled {
					fmt.Println("autostart:       enabled")
				} else {
					fmt.Println("autostart:       disabled")
				}
				if svc.Running {
					fmt.Println("process alive:   yes")
				} else {
					fmt.Println("process alive:   no")
				}
				cli, err := daemon.LocalClient()
				if err != nil {
					return err
				}
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				h, err := cli.Health(ctx)
				if err != nil {
					fmt.Println("\nIPC: offline —", err)
					return nil
				}
				fmt.Printf("\nIPC: online (host=%s version=%s sessions=%d sleep_mode=%s)\n",
					h.Hostname, h.Version, h.Sessions, h.SleepMode)
				return nil
			},
		},
		&cobra.Command{
			Use:   "stop",
			Short: "Stop ccmuxd (this login session only — use `uninstall` to disable autostart too)",
			RunE: func(_ *cobra.Command, _ []string) error {
				out, err := exec.Command("pkill", "-x", "ccmuxd").CombinedOutput()
				if err != nil {
					return fmt.Errorf("%w (%s)", err, strings.TrimSpace(string(out)))
				}
				fmt.Println("ccmuxd stopped")
				return nil
			},
		},
		&cobra.Command{
			Use:   "restart",
			Short: "Restart ccmuxd so a newly-installed binary takes effect",
			Long: `Bounces the running daemon so a freshly-installed ccmuxd binary is
picked up. macOS uses ` + "`launchctl kickstart -k`" + `, Linux uses
` + "`systemctl --user restart`" + `; both preserve the autostart wiring
` + "`ccmux daemon install`" + ` set up.

Called automatically by ` + "`ccmux update`" + `, ` + "`make install`" + `, and the
Homebrew formula's post_install hook — so the common upgrade paths
all pick up new code without a logout. Run by hand after a manual
` + "`go build`" + ` if you've side-stepped those paths.

Idempotent. If the daemon isn't running yet, prints a note and exits 0
so install scripts can call it unconditionally.`,
			RunE: func(_ *cobra.Command, _ []string) error {
				s, err := daemonservice.Restart()
				if err != nil {
					return err
				}
				if s.Running {
					fmt.Println("✓ ccmuxd restarted")
				} else {
					fmt.Println("note: ccmuxd is not running — start it with `ccmux daemon start` or `ccmux daemon install`")
				}
				return nil
			},
		},
		&cobra.Command{
			Use:   "install",
			Short: "Install ccmuxd as a system service so it starts on login + restarts on crash",
			Long: `macOS: writes ~/Library/LaunchAgents/dev.ccmux.daemon.plist with
RunAtLoad + KeepAlive, then launchctl loads it.

Linux: writes ~/.config/systemd/user/ccmuxd.service with
Restart=on-failure, then systemctl --user daemon-reload &&
systemctl --user enable --now ccmuxd.

Either way, the daemon survives logout, reboot, and unexpected
crashes. Stdout/stderr (macOS) go to
~/.local/state/ccmux/ccmuxd.{stdout,stderr}.log; systemd routes
through journalctl.

Idempotent: re-running re-applies the service config, picking up
any binary-path changes.`,
			RunE: func(_ *cobra.Command, _ []string) error {
				s, err := daemonservice.Install()
				if err != nil {
					return err
				}
				fmt.Println("✓ service file written to", s.ServicePath)
				if s.ServiceEnabled {
					switch s.OS {
					case "darwin":
						fmt.Println("✓ ccmuxd is loaded under launchd; it will start automatically on every login.")
					case "linux":
						fmt.Println("✓ ccmuxd is enabled under systemd-user; it will start automatically on every login.")
					}
				}
				if s.Running {
					fmt.Println("✓ ccmuxd is running now (check `ccmux daemon status` for details)")
				}
				return nil
			},
		},
		&cobra.Command{
			Use:   "uninstall",
			Short: "Disable + remove the service file (does not remove the binary)",
			RunE: func(_ *cobra.Command, _ []string) error {
				if _, err := daemonservice.Uninstall(); err != nil {
					return err
				}
				fmt.Println("✓ service removed; ccmuxd will not start on next login")
				return nil
			},
		},
		&cobra.Command{
			Use:   "unit",
			Short: "Print the recommended systemd-user unit (Linux manual install)",
			RunE: func(_ *cobra.Command, _ []string) error {
				bin, err := exec.LookPath("ccmuxd")
				if err != nil {
					bin = "$HOME/.local/bin/ccmuxd"
				}
				fmt.Println("# Save to ~/.config/systemd/user/ccmuxd.service, then:")
				fmt.Println("#   systemctl --user daemon-reload")
				fmt.Println("#   systemctl --user enable --now ccmuxd")
				fmt.Println()
				fmt.Print(daemonservice.UnitFile(bin))
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
