package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	qrterminal "github.com/mdp/qrterminal/v3"
	"github.com/spf13/cobra"

	"github.com/skzv/ccmux/internal/daemon"
)

func init() {
	rootCmd.AddCommand(newPairCmd())
}

func newPairCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pair",
		Short: "Show a QR code to pair a mobile client with this host",
		Long: `Asks the running ccmuxd to generate a one-time pairing token and
prints a QR code. Scan it with a mobile client — it pairs
automatically: no typing, no manual authorized_keys edit. (Redeems via
POST /v1/pair — see docs/02_Architecture/05_HTTP_API.md.)

The token expires in 5 minutes. ccmuxd must be running with
listen_tailnet = true.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			cli, err := daemon.LocalClient()
			if err != nil {
				return fmt.Errorf("ccmuxd not running — start it with `ccmux daemon start`")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			resp, err := cli.CreatePairToken(ctx)
			if err != nil {
				return fmt.Errorf("pair token: %w", err)
			}
			fmt.Println("Scan with a mobile client (expires in 5 minutes):")
			fmt.Println()
			qrterminal.GenerateHalfBlock(resp.URL, qrterminal.L, os.Stdout)
			fmt.Println()
			fmt.Printf("  %s\n\n", resp.URL)
			return nil
		},
	}
}
