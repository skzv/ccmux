package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
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
	"github.com/skzv/ccmux/internal/sshsetup"
	"github.com/skzv/ccmux/internal/tmux"
)

// newAttachCmd: `ccmux attach [project]`
// Attaches to the named project's agent session (or the current
// directory if none is given). If the session doesn't exist, creates
// it via the agent's LaunchCmd(continue=true) — Claude by default, or
// whichever agent the project's .ccmux/agent sidecar records.
func newAttachCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "attach [project|path]",
		Short: "Attach to a project's agent session (creates one if missing)",
		Long: `Attach to a project's agent session, creating it if it isn't running.

A bare name (no "/") is a project under the projects root (~/Projects,
projects.root in config, or --projects), so ` + "`ccmux attach auth-redesign`" + `
works from any directory. Anything with a "/" (./scratch, ../x, /abs/x)
is a path. With no argument, the current directory is used. A directory
that doesn't exist is an error — the session is never started elsewhere.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			arg := ""
			if len(args) == 1 {
				arg = args[0]
			}
			cfg, _ := config.Load()
			root, err := cliProjectsRoot(cfg)
			if err != nil {
				return err
			}
			dir, found := resolveAttachDir(arg, root)
			session := tmux.SessionNameForPath(dir)

			ctx := context.Background()
			has, err := tmux.Has(ctx, session)
			if err != nil {
				return err
			}
			created := false
			if !has {
				// Never launch into a directory that doesn't exist:
				// tmux silently falls back to $HOME for a missing -c
				// dir, and `--continue` there resumes an unrelated
				// conversation.
				if !found {
					return missingAttachDirErr(arg, dir, root)
				}
				// Resolve the launch command from the project's
				// sidecar so an Antigravity-tagged project doesn't
				// silently boot into claude.
				launch := agent.LaunchCmd(project.ReadAgent(dir), true, cfg.AgentCommands())
				if err := tmux.New(ctx, session, dir, launch); err != nil {
					return err
				}
				created = true
			}
			// Replace this process with tmux attach, applying ccmux
			// chrome first so a CLI-spawned session looks the same as a
			// TUI/daemon-spawned one.
			return attachWithChrome(session, filepath.Base(dir), detachOthersForAttachIntent(created))
		},
	}
}

// resolveAttachDir maps `ccmux attach`'s argument to a directory.
//
// A bare name (no path separator) is a project name first: the README
// flow is `ccmux new auth-redesign` then `ccmux attach auth-redesign`,
// which must work from any CWD — resolving it against the CWD started
// the agent in the wrong directory. If <root>/<name> doesn't exist, a
// bare name falls back to a CWD-relative directory. Anything else (".",
// "./x", "../x", "/abs/x") is a path relative to the CWD; "" means ".".
//
// found reports whether dir exists. The session name is derived from
// dir either way, so a session that is already running stays
// attachable after its directory is gone.
func resolveAttachDir(arg, root string) (dir string, found bool) {
	if arg == "" {
		arg = "."
	}
	if isBareProjectName(arg) {
		if p := filepath.Join(root, arg); isDir(p) {
			return p, true
		}
	}
	abs, err := filepath.Abs(arg)
	if err != nil {
		return arg, false
	}
	return abs, isDir(abs)
}

// isBareProjectName reports whether arg names a project rather than a
// path: no separator, and not "." / "..".
func isBareProjectName(arg string) bool {
	return arg != "." && arg != ".." &&
		!strings.ContainsRune(arg, '/') && !strings.ContainsRune(arg, filepath.Separator)
}

func missingAttachDirErr(arg, dir, root string) error {
	if isBareProjectName(arg) {
		return fmt.Errorf("no project %q under %s (and no directory %s); create it with `ccmux new %s`", arg, root, dir, arg)
	}
	return fmt.Errorf("no such directory: %s", dir)
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
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
			cfg, _ := config.Load()
			if err := project.ValidateName(args[0]); err != nil {
				return err
			}
			// Create under the projects root (README: "Creates
			// ~/Projects/<name>"; --projects overrides it), not the
			// current directory.
			root, err := cliProjectsRoot(cfg)
			if err != nil {
				return err
			}
			opts := scaffold.Options{
				Name:     args[0],
				Dir:      filepath.Join(root, args[0]),
				Commands: cfg.AgentCommands(),
			}
			id, err := newCmdAgent(agentFlag, cfg.Agents.Default)
			if err != nil {
				return err
			}
			opts.Agent = id
			session, err := scaffold.StartSession(context.Background(), opts)
			if err != nil {
				return err
			}
			return attachWithChrome(session, args[0], false)
		},
	}
	c.Flags().StringVar(&agentFlag, "agent", "",
		"agent to launch: "+agentIDList()+" (default: agents.default from config, else claude)")
	return c
}

// newCmdAgent picks the agent `ccmux new` launches: --agent when given,
// else config's agents.default (as the TUI's new-project form does),
// else the zero ID, which scaffold treats as Claude. An unrecognised
// agents.default (e.g. "shell", which isn't an agent) falls through to
// that default rather than failing.
func newCmdAgent(flag, configDefault string) (agent.ID, error) {
	if flag != "" {
		id, ok := agent.ParseID(flag)
		if !ok {
			return "", fmt.Errorf("unknown agent %q (want %s)", flag, agentIDList())
		}
		return id, nil
	}
	if id, ok := agent.ParseID(configDefault); ok {
		return id, nil
	}
	return "", nil
}

// agentIDList renders every registered agent id for help and errors, so
// the list can't fall behind agent.All().
func agentIDList() string {
	ids := make([]string, 0, len(agent.All()))
	for _, a := range agent.All() {
		ids = append(ids, string(a.ID()))
	}
	return strings.Join(ids, ", ")
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
				// Always an array: a nil slice encodes as `null`, which
				// breaks `ccmux list --json | jq '.[]'` on an idle box.
				if sessions == nil {
					sessions = []daemon.SessionState{}
				}
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
			ctx := context.Background()
			name, err := resolveKillTarget(ctx, args[0], tmux.Has)
			if err != nil {
				return err
			}
			return tmux.Kill(ctx, name)
		},
	}
}

// resolveKillTarget maps `ccmux kill`'s argument to a session name. An
// argument that already IS a live session is killed as-is; otherwise
// it's a project name/path, mapped through the same sanitizer `ccmux
// attach` and the daemon use (tmux.SessionNameForPath).
//
// It used to guess from the "c-" prefix alone: for a project literally
// named `c-foo` (session c-c-foo), `kill c-foo` killed project foo's
// session instead, and a session without the prefix (`ccmux shell
// --name work`) was rewritten to c-work and could never be killed.
func resolveKillTarget(ctx context.Context, arg string, has func(context.Context, string) (bool, error)) (string, error) {
	exists, err := has(ctx, arg)
	if err != nil {
		return "", err
	}
	if exists {
		return arg, nil
	}
	mapped := tmux.SessionNameForPath(arg)
	if mapped != arg {
		if exists, err = has(ctx, mapped); err != nil {
			return "", err
		}
		if exists {
			return mapped, nil
		}
	}
	return "", fmt.Errorf("no session named %q, and no session %q for a project named %q", arg, mapped, arg)
}

// newSetupCmd: `ccmux setup` first-run wizard. Idempotent — re-running
// just verifies what's already done and prompts only for missing
// pieces.
func newSetupCmd() *cobra.Command {
	var assumeYes bool
	c := &cobra.Command{
		Use:   "setup",
		Short: "Interactive first-run setup wizard",
		Long: `Walk through deps, Tailscale, Moshi, the SSH key, config, and the
ccmuxd autostart service. Idempotent — safe to re-run.

Pass --yes (-y) to run non-interactively: every prompt takes its
recommended answer (install missing deps, generate the SSH key, install
the ccmuxd autostart service), and integrations that can't be scripted
(Moshi pairing, Tailscale/gh browser auth) are reported and skipped.
Handy for dotfiles and provisioning scripts.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return setupwizard.RunWithOptions(context.Background(), os.Stdout, setupwizard.Options{AssumeYes: assumeYes})
		},
	}
	c.Flags().BoolVarP(&assumeYes, "yes", "y", false, "non-interactive: take recommended answers, skip interactive integrations")
	return c
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
	case agent.IDGemini:
		return strings.TrimSpace(cfg.Agents.Gemini.Command)
	case agent.IDAntigravity:
		return strings.TrimSpace(cfg.Agents.Antigravity.Command)
	case agent.IDCursor:
		return strings.TrimSpace(cfg.Agents.Cursor.Command)
	case agent.IDPi:
		return strings.TrimSpace(cfg.Agents.Pi.Command)
	case agent.IDMuse:
		return strings.TrimSpace(cfg.Agents.Muse.Command)
	case agent.IDGrok:
		return strings.TrimSpace(cfg.Agents.Grok.Command)
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
	bad := runDoctorBinChecks(os.Stdout, []doctorBinCheck{
		{bin: "tmux", hint: hintFor("brew install tmux", "apt/dnf/pacman install tmux")},
		{bin: "mosh", hint: hintFor("brew install mosh", "apt/dnf/pacman install mosh")},
		{bin: "tailscale", hint: "https://tailscale.com/download"},
		{bin: "rg", hint: hintFor("brew install ripgrep — accelerates notes search", "apt install ripgrep — accelerates notes search"), optional: true},
	}, exec.LookPath)

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

	// SSH bootstrap status for every configured remote host. The
	// common failure modes are subtle ("key auth not set up yet",
	// "sshd off on the remote", "Tailscale not routing") and the
	// error you'd otherwise see is the raw openssh stderr — usually
	// "Permission denied (publickey)" with no remediation. We
	// classify and offer the one-line fix.
	if len(cfg.Hosts) > 0 {
		fmt.Println()
		fmt.Println("Configured SSH hosts:")
		for _, h := range cfg.Hosts {
			label := h.Name
			if h.Name == "" {
				label = h.Address
			}
			// Same target construction as `host setup-ssh` — honors
			// ssh_port instead of hardcoding 22.
			target := sshTargetForHost(h)
			// Per-host budget: with one shared deadline, every host
			// after a slow/unreachable one misreported as timeout.
			probeCtx, probeCancel := context.WithTimeout(context.Background(), 12*time.Second)
			res := sshsetup.Probe(probeCtx, target)
			probeCancel()
			line, healthy := probeResultLine(res, label, target)
			if !healthy {
				bad++
			}
			fmt.Println(line)
		}
	}

	if bad > 0 {
		os.Exit(bad)
	}
	return nil
}

// doctorBinCheck is one "is <bin> on PATH" doctor line.
type doctorBinCheck struct {
	bin, hint string
	// optional binaries are reported when missing but never count
	// toward doctor's failure exit code.
	optional bool
}

// runDoctorBinChecks prints one line per check and returns how many
// REQUIRED binaries are missing — doctor's exit code sums these. A
// missing optional binary (rg) used to count too, so a healthy machine
// without ripgrep made `ccmux doctor` exit 1.
func runDoctorBinChecks(w io.Writer, checks []doctorBinCheck, lookPath func(string) (string, error)) int {
	bad := 0
	for _, c := range checks {
		switch _, err := lookPath(c.bin); {
		case err == nil:
			fmt.Fprintf(w, "✓ %s\n", c.bin)
		case c.optional:
			fmt.Fprintf(w, "· %s not on PATH (optional) — %s\n", c.bin, c.hint)
		default:
			fmt.Fprintf(w, "✗ %s not on PATH — %s\n", c.bin, c.hint)
			bad++
		}
	}
	return bad
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
	case agent.IDGemini:
		return "npm i -g @google/gemini-cli"
	case agent.IDAntigravity:
		return "`curl -fsSL https://antigravity.google/cli/install.sh | bash` (or see antigravity docs)"
	case agent.IDCursor:
		return "`curl https://cursor.com/install -fsS | bash` (or see cursor docs)"
	case agent.IDPi:
		return "`curl -fsSL https://pi.dev/install.sh | sh` (or `npm i -g @earendil-works/pi-coding-agent`)"
	case agent.IDGrok:
		return "`curl -fsSL https://x.ai/cli/install.sh | bash` (or `npm i -g @xai-official/grok`)"
	case agent.IDOpenCode:
		return "`curl -fsSL https://opencode.ai/install | bash` (or `npm i -g opencode-ai`)"
	case agent.IDKimi:
		return "`npm i -g @moonshot/kimi-code` (or see Kimi Code docs)"
	case agent.IDDroid:
		return "`curl -fsSL https://app.factory.ai/cli | sh` (or see Factory docs)"
	case agent.IDCopilot:
		return "`npm i -g @github/copilot` (or see GitHub Copilot CLI docs)"
	case agent.IDQoder:
		return "`npm i -g @qoder/cli` (or see Qoder docs)"
	case agent.IDKilo:
		return "`npm i -g @kilocode/cli` (or see Kilo Code docs)"
	case agent.IDHermes:
		return "`uv tool install hermes-agent` (or see https://hermes-agent.nousresearch.com)"
	case agent.IDAmp:
		return "`npm i -g @sourcegraph/amp` (or see Amp docs at ampcode.com)"
	case agent.IDMuse:
		return "curl -fsSL https://dev.meta.ai/install.sh | bash  (macOS: brew install --cask muse-code); then muse login"
	case agent.IDKiro:
		return "see Kiro CLI install at https://kiro.dev/docs/cli"
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

// daemonStartDeps groups the side-effecting bits of `ccmux daemon
// start` so the command stays a thin wrapper and the start logic is
// unit-testable without pgrep'ing or spawning a real process.
type daemonStartDeps struct {
	// running reports whether a ccmuxd is already up, and its pid.
	// Checked first so `daemon start` never spawns a doomed second
	// daemon (the newcomer would just lose the socket race and exit,
	// but the user still saw a confusing "started pid N" line — and
	// against a *wedged* daemon it could even bind a fresh socket
	// and become a persistent rogue).
	running func() (pid int, ok bool)
	// spawn launches a detached ccmuxd and returns its pid.
	spawn func() (pid int, err error)
}

// defaultDaemonStartDeps wires the production pgrep + spawn.
func defaultDaemonStartDeps() daemonStartDeps {
	return daemonStartDeps{
		running: runningCcmuxdPID,
		spawn: func() (int, error) {
			bin, ok := daemonservice.CcmuxdBinary()
			if !ok {
				return 0, fmt.Errorf("ccmuxd binary not found next to ccmux or on PATH — reinstall ccmux")
			}
			// nocontext: the daemon is spawned detached and must outlive us.
			dCmd := exec.Command(bin)
			detachProcess(dCmd) // OS-specific: setsid on unix, DETACHED_PROCESS on windows
			if err := dCmd.Start(); err != nil {
				return 0, err
			}
			return dCmd.Process.Pid, nil
		},
	}
}

// runDaemonStart is the testable core of `ccmux daemon start`. It
// no-ops (exit 0) when a daemon is already running rather than
// spawning a duplicate — see daemonStartDeps.running for why.
func runDaemonStart(out io.Writer, deps daemonStartDeps) error {
	if pid, ok := deps.running(); ok {
		fmt.Fprintf(out, "ccmuxd already running (pid %d) — nothing to do\n", pid)
		return nil
	}
	pid, err := deps.spawn()
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "ccmuxd started (pid %d)\n", pid)
	return nil
}

// waitForDaemonHealth polls the local ccmuxd's /v1/health until it
// answers or `timeout` elapses, returning true on the first success.
// Used after a restart to confirm the daemon is actually serving (not
// just that a process exists), so callers report the settled state
// instead of a transient mid-restart gap. A health check — not a bare
// pgrep — because during the handoff the OLD process may still be
// alive but no longer listening; only the NEW one answers health.
func waitForDaemonHealth(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		cli, err := daemon.LocalClient()
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			_, herr := cli.Health(ctx)
			cancel()
			if herr == nil {
				return true
			}
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// runningCcmuxdPID returns the pid of a live ccmuxd via `pgrep -x`,
// or ok=false when none is running. `-x` matches the exact process
// name so it can't be confused by, say, `ccmux daemon start` itself
// (that process is "ccmux", not "ccmuxd"). When several match (a
// pre-existing rogue pair) we report the first — enough for the
// "already running" message.
func runningCcmuxdPID() (int, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "pgrep", "-U", strconv.Itoa(os.Getuid()), "-x", "ccmuxd").Output()
	if err != nil {
		return 0, false // non-zero exit = no match
	}
	for _, line := range strings.Fields(string(out)) {
		if pid, perr := strconv.Atoi(line); perr == nil && pid > 0 {
			return pid, true
		}
	}
	return 0, false
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
				return runDaemonStart(os.Stdout, defaultDaemonStartDeps())
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
				if svc.BinaryInstalled {
					fmt.Printf("ccmuxd binary:   %s\n", svc.BinaryPath)
				} else {
					fmt.Println("ccmuxd binary:   not found (looked next to ccmux + PATH) — reinstall ccmux")
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
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				out, err := exec.CommandContext(ctx, "pkill", "-U", strconv.Itoa(os.Getuid()), "-x", "ccmuxd").CombinedOutput()
				if err != nil {
					// pkill exits 1 when nothing matched — i.e. ccmuxd
					// isn't running. That's a successful no-op for
					// `stop`, not an error, so don't fail the command
					// (a non-zero exit breaks `ccmux daemon stop && …`
					// chains and scripted teardown).
					if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
						fmt.Println("ccmuxd is not running")
						return nil
					}
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
				// daemonservice.Restart() issues the bounce and returns
				// immediately, but the new daemon needs a beat to re-bind
				// the socket. Probing right away (as we used to) reported
				// "not running" mid-restart even though it was coming back
				// — the confusing message after `make install`. Poll the
				// IPC health endpoint so we report the SETTLED state: a
				// successful health check means it's actually serving.
				if waitForDaemonHealth(8 * time.Second) {
					fmt.Println("✓ ccmuxd restarted")
					return nil
				}
				if s.Running {
					// Process is up but not answering health yet — almost
					// certainly still warming up, not a failure.
					fmt.Println("✓ ccmuxd restarted (still warming up)")
					return nil
				}
				fmt.Println("note: ccmuxd is not running — start it with `ccmux daemon start` or `ccmux daemon install`")
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
				bin, _ := daemonservice.CcmuxdBinary()
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
				// Abort on a Load error instead of proceeding: Load
				// returns Defaults() alongside the error on a corrupt or
				// unreadable config.toml, and Save truncates the file —
				// so swallowing the error would wipe every other host and
				// all other settings on the next write.
				cfg, err := config.Load()
				if err != nil {
					return fmt.Errorf("load config (not modifying it): %w", err)
				}
				// Reject duplicate names instead of silently appending:
				// `host remove <name>` deletes every entry with that
				// name, so a duplicate add would make the eventual
				// remove wipe both — including the original.
				for _, h := range cfg.Hosts {
					if h.Name == args[0] {
						return fmt.Errorf("host %q already exists (address %s); run `ccmux host remove %s` first, or pick a different name", args[0], h.Address, args[0])
					}
				}
				cfg.Hosts = append(cfg.Hosts, config.Host{Name: args[0], Address: args[1], Mosh: true, Port: 7474})
				return config.Save(cfg)
			},
		},
		&cobra.Command{
			Use:   "remove <name>",
			Short: "Remove a remote ccmuxd host",
			Args:  cobra.ExactArgs(1),
			RunE: func(_ *cobra.Command, args []string) error {
				// Same guard as `host add`: never rewrite config.toml from
				// a Defaults()-on-error config — it would erase everything.
				cfg, err := config.Load()
				if err != nil {
					return fmt.Errorf("load config (not modifying it): %w", err)
				}
				out := make([]config.Host, 0, len(cfg.Hosts))
				for _, h := range cfg.Hosts {
					if h.Name != args[0] {
						out = append(out, h)
					}
				}
				// An unknown name is an error, and nothing is written:
				// this used to exit 0 (a typo looked like success) and
				// still rewrite config.toml.
				if len(out) == len(cfg.Hosts) {
					return fmt.Errorf("no host named %q (see `ccmux host list`)", args[0])
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
		newHostSetupSSHCmd(),
	)
	return c
}

// probeResultLine renders one `ccmux doctor` line for an SSH probe
// result, returning the line and whether the host is healthy (false
// increments doctor's bad count, and so its exit code).
//
// Extracted from the doctor loop so every branch's copy is testable
// without a live host. The rule the tests pin: any branch whose cause
// could be "we probed the wrong port" must name the port, because a
// host with a custom ssh_port is exactly where the user needs to see
// which port doctor actually tried.
func probeResultLine(res sshsetup.ProbeResult, label string, target sshsetup.Target) (string, bool) {
	switch res {
	case sshsetup.ProbeOK:
		return fmt.Sprintf("  ✓ %s (%s) — key auth ready", label, target.String()), true
	case sshsetup.ProbeAuthFailed:
		return fmt.Sprintf("  ✗ %s (%s) — key not installed; run `ccmux host setup-ssh %s`",
			label, target.String(), label), false
	case sshsetup.ProbeSshdDisabled:
		return fmt.Sprintf("  ✗ %s — sshd not running on %s. On macOS: System Settings → General → Sharing → Remote Login",
			label, target.Host), false
	case sshsetup.ProbeRefused:
		return fmt.Sprintf("  ✗ %s — port %d on %s closed; check sshd binding",
			label, target.Port, target.Host), false
	case sshsetup.ProbeTimeout:
		// Name the port here too: on a custom-port host a timeout is
		// ambiguous between "host unreachable" and "probing the wrong
		// port", and the refused/OK branches both report it — so
		// omitting it left silent the one case that most needs it.
		return fmt.Sprintf("  · %s — timeout reaching port %d on %s; is Tailscale connected on both ends?",
			label, target.Port, target.Host), false
	case sshsetup.ProbeNoNetwork:
		// Name resolution failed, so no port was ever dialed — naming
		// one here would be misleading noise.
		return fmt.Sprintf("  · %s — can't resolve %s; check MagicDNS or use the tailnet IP",
			label, target.Host), false
	case sshsetup.ProbeHostKeyMismatch:
		return fmt.Sprintf("  ✗ %s — ⚠ host key changed for %s; investigate (possible MITM) before re-adding",
			label, target.Host), false
	default:
		return fmt.Sprintf("  · %s — probe inconclusive (%s)", label, res.String()), true
	}
}
