package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/skzv/ccmux/internal/moshi"
)

// newMoshiSetupCmd: `ccmux moshi-setup [--token X]` walks through the
// full moshi-hook install + pair + hooks-install + service-start flow.
//
// Default UX is interactive Easy Pair: `moshi-hook host setup` prints a
// QR code in the terminal, the user scans it with the Moshi iOS app,
// pairing completes. No token to copy.
//
// --token <X> bypasses the QR code and uses the token-paste path
// (Moshi → Settings → Integrations → pairing token), useful for
// headless / scripted setups where you can't see a QR code in the
// terminal.
func newMoshiSetupCmd() *cobra.Command {
	var token string
	c := &cobra.Command{
		Use:   "moshi-setup",
		Short: "Install moshi-hook and pair it with the Moshi app",
		Long: `moshi-setup installs Moshi's agent-hook daemon, pairs it with the Moshi
app on your phone (Easy Pair, QR code in the terminal), wires it into
Claude Code's settings.json, and starts it as a background service.

By default it runs the QR-code Easy Pair flow — no token to copy.
For headless setups, pass --token to use the token-paste path
(Moshi app → Settings → Integrations → pairing token).`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runMoshiSetup(token)
		},
	}
	c.Flags().StringVar(&token, "token", "",
		"use the headless token-paste pairing path instead of QR-code Easy Pair")
	return c
}

func runMoshiSetup(token string) error {
	ctx := context.Background()
	st := moshi.Detect(ctx)

	// Step 1: install via Homebrew if missing.
	if !st.BinaryInstalled {
		if _, err := exec.LookPath("brew"); err != nil {
			return fmt.Errorf("brew not on PATH; install Homebrew first, then re-run")
		}
		fmt.Println("→ brew tap rjyo/moshi && brew install moshi-hook")
		for _, args := range moshi.InstallCmds() {
			cmd := exec.Command(args[0], args[1:]...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("%s: %w", strings.Join(args, " "), err)
			}
		}
		st = moshi.Detect(ctx)
		if !st.BinaryInstalled {
			return fmt.Errorf("moshi-hook still not on PATH after install — try opening a fresh shell")
		}
	} else {
		fmt.Printf("✓ moshi-hook already installed: %s\n", st.BinaryPath)
	}

	// Step 2: pairing.
	//
	// Default: Easy Pair via QR code (`moshi-hook host setup`). The
	// command renders a QR code in the terminal and prompts in-process;
	// we pass stdio through so the user can scan + interact. Known
	// recoverable errors (e.g. Remote Login disabled) get a targeted
	// hint pointing at the fix command — same UX as the setup wizard.
	//
	// If --token is passed, fall back to the headless token-paste
	// path (`moshi-hook pair --token <X>`).
	if !st.Paired {
		if token != "" {
			fmt.Println("→ moshi-hook pair (headless / token path)")
			if err := moshi.Pair(ctx, token); err != nil {
				return fmt.Errorf("pair: %w", err)
			}
		} else {
			fmt.Println("→ moshi-hook host setup (Easy Pair — scan the QR code below with the Moshi app on your phone)")
			fmt.Println()
			output, err := moshi.HostSetup(ctx)
			if err != nil {
				if fix, ok := moshi.DetectFix(output); ok {
					fmt.Println()
					fmt.Printf("✗ %s\n", fix.Problem)
					fmt.Printf("  Fix: %s %s\n", fix.Command, strings.Join(fix.Args, " "))
					if fix.SettingsURL != "" {
						fmt.Printf("  Or:  open '%s'\n", fix.SettingsURL)
					}
					fmt.Println("  Then re-run `ccmux moshi-setup`.")
				}
				return fmt.Errorf("host setup: %w", err)
			}
		}
	} else {
		fmt.Println("✓ moshi-hook already paired")
	}

	// Step 3: install Claude Code (etc.) hooks if not done.
	if !st.HooksInstalled {
		fmt.Println("→ moshi-hook install (wires Claude Code hooks)")
		if err := moshi.InstallHooks(ctx); err != nil {
			return fmt.Errorf("install hooks: %w", err)
		}
	} else {
		fmt.Println("✓ Claude Code hooks already wired")
	}

	// Step 4: start the service.
	if !st.ServiceRunning {
		fmt.Println("→ brew services start moshi-hook")
		if err := moshi.StartService(ctx); err != nil {
			// Non-fatal: user might be on Linux without brew. Print guidance.
			fmt.Printf("⚠ could not start as a brew service (%v). On Linux: `moshi-hook serve` in a systemd-user unit.\n", err)
		}
	} else {
		fmt.Println("✓ moshi-hook is already running as a service")
	}

	// Final status.
	final := moshi.Detect(ctx)
	fmt.Println()
	fmt.Println("Done. Final state:")
	fmt.Printf("  binary       %v (%s)\n", final.BinaryInstalled, final.BinaryPath)
	fmt.Printf("  paired       %v\n", final.Paired)
	fmt.Printf("  hooks wired  %v\n", final.HooksInstalled)
	fmt.Printf("  service up   %v\n", final.ServiceRunning)
	fmt.Println()
	fmt.Println("Next: in Moshi, add a connection with command")
	fmt.Println("  tmux new-session -A -s ccmux ccmux")
	fmt.Println("Then tap to land in the ccmux TUI from your phone.")
	return nil
}
