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
// full moshi-hook install + pair + hooks-install + service-start flow,
// prompting interactively for the pairing token when not supplied via
// --token.
func newMoshiSetupCmd() *cobra.Command {
	var token string
	c := &cobra.Command{
		Use:   "moshi-setup",
		Short: "Install moshi-hook and pair it with the Moshi app",
		Long: `moshi-setup installs Moshi's agent-hook daemon, pairs it with the Moshi
app on your phone, wires it into Claude Code's settings.json, and starts
it as a background service.

You'll need a pairing token from the Moshi app first:
  Open Moshi → Settings → Pair host → copy the token.

Then run:
  ccmux moshi-setup --token <token>

Or run it without --token to be prompted.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runMoshiSetup(token)
		},
	}
	c.Flags().StringVar(&token, "token", "", "pairing token from the Moshi app (prompted if empty)")
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
	if !st.Paired {
		if token == "" {
			fmt.Println()
			fmt.Println("Open Moshi on your phone → Settings → Pair host → copy the token.")
			fmt.Print("Pairing token: ")
			var typed string
			_, _ = fmt.Scanln(&typed)
			token = strings.TrimSpace(typed)
		}
		if token == "" {
			return fmt.Errorf("no token provided; skipping pair step. Re-run with --token <token> when ready")
		}
		fmt.Println("→ moshi-hook pair")
		if err := moshi.Pair(ctx, token); err != nil {
			return fmt.Errorf("pair: %w", err)
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
	fmt.Printf("  connected    %v\n", final.Connected)
	fmt.Printf("  hooks wired  %v\n", final.HooksInstalled)
	fmt.Printf("  service up   %v\n", final.ServiceRunning)
	fmt.Println()
	fmt.Println("Next: in Moshi, add a connection with command")
	fmt.Println("  tmux new-session -A -s ccmux ccmux")
	fmt.Println("Then tap to land in the ccmux TUI from your phone.")
	return nil
}
