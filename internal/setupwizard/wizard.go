// Package setupwizard is the interactive first-run flow invoked by
// `ccmux setup`. It walks the user through dependency installation,
// Tailscale verification, Moshi pairing, SSH key generation, and basic
// config — using Huh forms for the interactive bits and plain prints
// for the status lines between them.
//
// Each step is idempotent: re-running the wizard skips steps that are
// already done (with a "✓ already configured" line) and only prompts
// for what's missing.
package setupwizard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"github.com/skzv/ccmux/internal/claudeauth"
	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemonservice"
	"github.com/skzv/ccmux/internal/ghauth"
	"github.com/skzv/ccmux/internal/moshi"
)

// Theme styles for the printed (non-Huh) status lines so the chrome
// matches the rest of ccmux.
var (
	stTitle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#cba6f7")).Bold(true)
	stOK       = lipgloss.NewStyle().Foreground(lipgloss.Color("#a6e3a1"))
	stWarn     = lipgloss.NewStyle().Foreground(lipgloss.Color("#f9e2af"))
	stErr      = lipgloss.NewStyle().Foreground(lipgloss.Color("#f38ba8"))
	stMuted    = lipgloss.NewStyle().Foreground(lipgloss.Color("#7f849c"))
	stEmphasis = lipgloss.NewStyle().Foreground(lipgloss.Color("#cdd6f4")).Bold(true)
)

// Run executes the full wizard. Each step is independent; we collect
// soft failures (couldn't install brew package, user declined a step)
// in `softErrs` but don't bail — the final summary lists them.
//
// `out` is where we print the conversational chrome. Tests can swap it
// with a buffer; the binary passes os.Stdout.
func Run(ctx context.Context, out io.Writer) error {
	if out == nil {
		out = os.Stdout
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, stTitle.Render("ccmux setup wizard"))
	fmt.Fprintln(out, stMuted.Render("Walk through deps, Tailscale, Moshi, SSH key, and config. Idempotent — safe to re-run."))
	fmt.Fprintln(out)

	steps := []struct {
		name string
		fn   func(context.Context, io.Writer) error
	}{
		{"Dependencies", stepDeps},
		{"Tailscale", stepTailscale},
		{"GitHub CLI", stepGitHubAuth},
		{"Moshi (mobile push)", stepMoshi},
		{"SSH key for phone", stepSSHKey},
		{"ccmux config", stepConfig},
		{"ccmuxd autostart", stepDaemonService},
	}

	for i, s := range steps {
		fmt.Fprintf(out, "%s %s\n",
			stMuted.Render(fmt.Sprintf("[%d/%d]", i+1, len(steps))),
			stEmphasis.Render(s.name),
		)
		if err := s.fn(ctx, out); err != nil {
			fmt.Fprintf(out, "  %s %v\n\n", stErr.Render("✗"), err)
		}
		fmt.Fprintln(out)
	}

	fmt.Fprintln(out, stTitle.Render("Done."))
	fmt.Fprintln(out, "Next: " + stEmphasis.Render("ccmux") + " to launch the TUI.")
	return nil
}

// stepDeps: detect which CLI deps are installed, offer to brew install
// missing ones in one go.
func stepDeps(ctx context.Context, out io.Writer) error {
	checks := []depCheck{
		{bin: "tmux", brew: "tmux"},
		{bin: "mosh", brew: "mosh"},
		{bin: "tailscale", brew: "tailscale"},
		{bin: "claude", brew: ""}, // installed via Anthropic's installer
		{bin: "rg", brew: "ripgrep", optional: true},
		{bin: "moshi-hook", brew: "", optional: true}, // installed via tap in stepMoshi
	}
	missing := []string{}
	for _, c := range checks {
		if _, err := exec.LookPath(c.bin); err != nil {
			tag := stErr.Render("✗ missing")
			if c.optional {
				tag = stWarn.Render("· not installed (optional)")
			}
			fmt.Fprintf(out, "  %s  %s\n", c.bin, tag)
			if c.brew != "" && !c.optional {
				missing = append(missing, c.brew)
			}
		} else {
			fmt.Fprintf(out, "  %s  %s\n", c.bin, stOK.Render("✓"))
		}
	}

	if len(missing) == 0 {
		return nil
	}
	if _, err := exec.LookPath("brew"); err != nil {
		return fmt.Errorf("brew not on PATH; install Homebrew first, then re-run")
	}

	var install bool
	err := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().
			Title(fmt.Sprintf("Install %d missing package(s) via Homebrew?", len(missing))).
			Description("brew install " + strings.Join(missing, " ")).
			Affirmative("Install").
			Negative("Skip").
			Value(&install),
	)).Run()
	if err != nil {
		return err
	}
	if !install {
		fmt.Fprintln(out, stMuted.Render("  (skipped)"))
		return nil
	}
	args := append([]string{"install"}, missing...)
	cmd := exec.CommandContext(ctx, "brew", args...)
	cmd.Stdout = out
	cmd.Stderr = out
	return cmd.Run()
}

// stepGitHubAuth: gh is recommended (not required) for `ccmux new` to
// auto-create a private GitHub repo. We never block the wizard on this
// — just nudge.
func stepGitHubAuth(ctx context.Context, out io.Writer) error {
	s := ghauth.Detect(ctx)
	switch s.State {
	case ghauth.StateAuthed:
		who := s.User
		if who == "" {
			who = "(authed)"
		}
		fmt.Fprintf(out, "  %s  gh authenticated as %s\n", stOK.Render("✓"), who)
		return nil
	case ghauth.StateMissing:
		fmt.Fprintln(out, stWarn.Render("  gh not installed"))
		fmt.Fprintln(out, "  "+stMuted.Render("Optional — used by `ccmux new` to create a private GitHub repo for new projects."))
		var install bool
		if _, err := exec.LookPath("brew"); err == nil {
			if err := huh.NewConfirm().
				Title("Install gh via Homebrew?").
				Description("brew install gh — you'll still need to run `gh auth login` after.").
				Value(&install).Run(); err == nil && install {
				cmd := exec.CommandContext(ctx, "brew", "install", "gh")
				cmd.Stdout, cmd.Stderr = out, out
				if err := cmd.Run(); err != nil {
					fmt.Fprintf(out, "  %s brew install gh: %v\n", stErr.Render("✗"), err)
					return nil
				}
				fmt.Fprintln(out, "  "+stEmphasis.Render("Next: gh auth login")+"  (opens a browser).")
				return nil
			}
		} else {
			fmt.Fprintln(out, "  Install yourself: "+stEmphasis.Render("brew install gh")+", then "+stEmphasis.Render("gh auth login"))
		}
		return nil
	case ghauth.StateNotAuthed:
		fmt.Fprintln(out, stWarn.Render("  gh installed but not signed in"))
		fmt.Fprintln(out, "  Run "+stEmphasis.Render("gh auth login")+" in another terminal (opens a browser), then re-run "+stEmphasis.Render("ccmux setup")+" to verify.")
		return nil
	}
	return nil
}

// stepTailscale: verify the daemon is running and we're signed into a
// tailnet.
func stepTailscale(_ context.Context, out io.Writer) error {
	if _, err := exec.LookPath("tailscale"); err != nil {
		fmt.Fprintln(out, stWarn.Render("  tailscale not on PATH — skipped"))
		return nil
	}
	if out2, err := exec.Command("tailscale", "ip", "-4").Output(); err == nil && strings.TrimSpace(string(out2)) != "" {
		ip := strings.TrimSpace(string(out2))
		fmt.Fprintf(out, "  %s  signed in, tailnet IP: %s\n", stOK.Render("✓"), ip)
		return nil
	}
	fmt.Fprintln(out, stWarn.Render("  not signed in to a tailnet"))
	fmt.Fprintln(out, "  Run "+stEmphasis.Render("tailscale up")+" in another terminal (opens a browser to authenticate),")
	fmt.Fprintln(out, "  then re-run "+stEmphasis.Render("ccmux setup")+" to verify.")
	return nil
}

// stepMoshi: detect moshi-hook state and offer to install/pair/start.
// Delegates the actual brew tap + brew install dance to the moshi
// package; we just orchestrate consent.
func stepMoshi(ctx context.Context, out io.Writer) error {
	s := moshi.Detect(ctx)
	if s.BinaryInstalled && s.Paired && s.HooksInstalled && s.ServiceRunning {
		fmt.Fprintf(out, "  %s  installed, paired, hooks wired, service running\n", stOK.Render("✓"))
		return nil
	}
	fmt.Fprintln(out, stMuted.Render("  Moshi gives you categorized push notifications on iOS/Android when"))
	fmt.Fprintln(out, stMuted.Render("  Claude needs your input. Get the app at getmoshi.app."))

	var doSetup bool
	if err := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().
			Title("Set up moshi-hook now?").
			Description("Runs `ccmux moshi-setup` — installs, pairs, wires Claude hooks, starts service.").
			Affirmative("Set up").
			Negative("Later").
			Value(&doSetup),
	)).Run(); err != nil {
		return err
	}
	if !doSetup {
		fmt.Fprintln(out, stMuted.Render("  (skipped — run `ccmux moshi-setup` whenever you're ready)"))
		return nil
	}

	// Install if missing.
	if !s.BinaryInstalled {
		if _, err := exec.LookPath("brew"); err != nil {
			return errors.New("brew required for moshi-hook install")
		}
		for _, args := range moshi.InstallCmds() {
			fmt.Fprintln(out, stMuted.Render("  → "+strings.Join(args, " ")))
			c := exec.CommandContext(ctx, args[0], args[1:]...)
			c.Stdout = out
			c.Stderr = out
			if err := c.Run(); err != nil {
				return err
			}
		}
		s = moshi.Detect(ctx)
	}

	// Pair if needed. Uses Moshi's Easy Pair flow: moshi-hook prints
	// a QR code in the terminal and the user scans it with the Moshi
	// iOS app to complete pairing. No token to copy. We pass stdio
	// straight through so the QR renders and any moshi-hook prompts
	// reach the user.
	if !s.Paired {
		fmt.Fprintln(out, "  Open the Moshi app on your phone and tap "+stEmphasis.Render("Add Host → Scan QR")+".")
		fmt.Fprintln(out, "  A QR code will appear below — point your phone at it.")
		fmt.Fprintln(out)
		if err := moshi.HostSetup(ctx); err != nil {
			return fmt.Errorf("moshi-hook host setup: %w", err)
		}
	}

	// Install hooks + start service.
	if !s.HooksInstalled {
		if err := moshi.InstallHooks(ctx); err != nil {
			return fmt.Errorf("moshi-hook install: %w", err)
		}
	}
	if !s.ServiceRunning {
		if err := moshi.StartService(ctx); err != nil {
			fmt.Fprintf(out, "  %s start service: %v (on Linux: `moshi-hook serve` under systemd-user)\n", stWarn.Render("⚠"), err)
		}
	}
	final := moshi.Detect(ctx)
	if final.SuppressBell() {
		fmt.Fprintln(out, stOK.Render("  ✓ moshi-hook ready"))
	}
	return nil
}

// stepSSHKey: ensure ~/.ssh/id_ed25519 exists. The phone's Moshi app
// will store its own SSH key in iOS Keychain; this step is about the
// host's outbound key (to push to GitHub, etc.).
func stepSSHKey(_ context.Context, out io.Writer) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	keyPath := filepath.Join(home, ".ssh", "id_ed25519")
	if _, err := os.Stat(keyPath); err == nil {
		fmt.Fprintf(out, "  %s  %s exists\n", stOK.Render("✓"), keyPath)
		return nil
	}

	var gen bool
	if err := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().
			Title("Generate a new SSH key (ed25519)?").
			Description("Writes ~/.ssh/id_ed25519 with no passphrase.").
			Affirmative("Generate").
			Negative("Skip").
			Value(&gen),
	)).Run(); err != nil {
		return err
	}
	if !gen {
		fmt.Fprintln(out, stMuted.Render("  (skipped)"))
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return err
	}
	cmd := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-f", keyPath, "-C", "ccmux-generated")
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return err
	}
	fmt.Fprintln(out, stOK.Render("  ✓ key generated"))
	return nil
}

// stepConfig: confirm the projects root + subscription tier and write
// ~/.config/ccmux/config.toml. Auto-detects the Claude plan via
// claudeauth so we don't make the user pick from a list of strings
// they may not have memorized.
func stepConfig(ctx context.Context, out io.Writer) error {
	cfg, _ := config.Load()
	if cfg.Projects.Root == "" {
		home, _ := os.UserHomeDir()
		cfg.Projects.Root = filepath.Join(home, "Projects")
	}

	detectedTier := ""
	if s, err := claudeauth.Get(ctx); err == nil {
		detectedTier = s.Tier()
		fmt.Fprintf(out, "  detected Claude plan: %s\n", stEmphasis.Render(detectedTier))
	}
	if (cfg.Subscription.Tier == "" || cfg.Subscription.Tier == "api") && detectedTier != "" && detectedTier != "api" {
		cfg.Subscription.Tier = detectedTier
	}

	root := cfg.Projects.Root
	tier := cfg.Subscription.Tier
	if tier == "" {
		tier = "api"
	}

	if err := huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title("Projects root").
			Description("Where `ccmux new` scaffolds projects and where Projects tab scans.").
			Value(&root),
		huh.NewSelect[string]().
			Title("Claude subscription tier").
			Description("Drives the dashboard's quota bar — auto-detected from `claude auth status`.").
			Options(
				huh.NewOption("api (no subscription / API-key billing)", "api"),
				huh.NewOption("pro (~45 prompts / 5h)", "pro"),
				huh.NewOption("max 5x (~225 prompts / 5h)", "max5x"),
				huh.NewOption("max 20x (~900 prompts / 5h)", "max20x"),
			).
			Value(&tier),
	)).Run(); err != nil {
		return err
	}

	cfg.Projects.Root = strings.TrimSpace(root)
	cfg.Subscription.Tier = strings.TrimSpace(tier)
	if err := config.Save(cfg); err != nil {
		return err
	}
	p, _ := config.Path()
	fmt.Fprintf(out, "  %s  wrote %s\n", stOK.Render("✓"), p)
	return nil
}

// stepDaemonService: install (or update) the OS service that keeps
// ccmuxd running across logouts/reboots. Works on macOS (launchd) and
// Linux (systemd-user). Idempotent — re-running re-applies the service
// config so any binary-path changes get picked up.
func stepDaemonService(_ context.Context, out io.Writer) error {
	s := daemonservice.Probe()
	if s.OS != "darwin" && s.OS != "linux" {
		fmt.Fprintf(out, "  %s  auto-install not supported on %s — start ccmuxd manually with `ccmux daemon start`.\n",
			stWarn.Render("⚠"), s.OS)
		return nil
	}
	if s.ServiceEnabled && s.Running {
		fmt.Fprintf(out, "  %s  already installed and running (%s)\n",
			stOK.Render("✓"), s.ServicePath)
		return nil
	}
	if !s.BinaryInstalled {
		fmt.Fprintf(out, "  %s  ccmuxd not at %s — run `make install` first\n",
			stWarn.Render("⚠"), s.BinaryPath)
		return nil
	}

	var (
		title, desc, doneEnabledMsg string
	)
	switch s.OS {
	case "darwin":
		title = "Install ccmuxd as a launchd agent?"
		desc = "Writes ~/Library/LaunchAgents/dev.ccmux.daemon.plist with RunAtLoad+KeepAlive, then launchctl loads it. ccmuxd then starts at every login and restarts on crash."
		doneEnabledMsg = "loaded via launchctl; ccmuxd will start on every login"
	case "linux":
		title = "Install ccmuxd as a systemd-user service?"
		desc = "Writes ~/.config/systemd/user/ccmuxd.service with Restart=on-failure, then `systemctl --user daemon-reload && systemctl --user enable --now ccmuxd`. ccmuxd then starts at every login and restarts on crash."
		doneEnabledMsg = "enabled under systemd-user; ccmuxd will start on every login"
	}

	var doInstall bool
	if err := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().
			Title(title).
			Description(desc).
			Affirmative("Install").
			Negative("Skip").
			Value(&doInstall),
	)).Run(); err != nil {
		return err
	}
	if !doInstall {
		fmt.Fprintln(out, stMuted.Render("  (skipped — run `ccmux daemon install` whenever you're ready)"))
		return nil
	}
	final, err := daemonservice.Install()
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "  %s  service file written to %s\n", stOK.Render("✓"), final.ServicePath)
	if final.ServiceEnabled {
		fmt.Fprintln(out, "  "+stOK.Render("✓")+"  "+doneEnabledMsg)
	}
	if final.Running {
		fmt.Fprintln(out, "  "+stOK.Render("✓")+"  ccmuxd is running right now")
	}
	return nil
}

// depCheck is one row in the dependency table.
type depCheck struct {
	bin      string
	brew     string
	optional bool
}
