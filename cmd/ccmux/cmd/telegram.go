package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/telegram"
)

// newTelegramCmd is the `ccmux telegram` group: stand up and manage the
// Telegram bridge that lets you approve/deny and drive sessions from
// your phone or watch. Mirrors `ccmux mcp` in shape.
func newTelegramCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "telegram",
		Short: "Control ccmux from Telegram (approve/deny, drive agents, browse notes)",
		Long: `ccmux can run a Telegram bot that alerts you when an agent needs input and
lets you act on it — approve/deny, send the agent's own slash-commands, read
project notes — from your phone or watch. The bot reaches out to Telegram via
long polling, so it needs no open port and works behind NAT.

Setup is three steps:
  1. Create a bot with @BotFather and copy its token.
  2. ccmux telegram register --token <token>
  3. ccmux telegram pair   (then send the printed /start code to your bot)

Only chats you pair can drive the bot.`,
	}
	c.AddCommand(
		newTelegramRegisterCmd(),
		newTelegramPairCmd(),
		newTelegramStatusCmd(),
		newTelegramTestCmd(),
		newTelegramServeCmd(),
	)
	return c
}

func newTelegramRegisterCmd() *cobra.Command {
	var (
		token     string
		allowExec bool
		webViewer bool
	)
	c := &cobra.Command{
		Use:   "register",
		Short: "Set the bot token and enable the bridge",
		Long: `Saves the bot token to ~/.config/ccmux/config.toml and enables the bridge.

The token comes from @BotFather. Provide it with --token, or set
CCMUX_TELEGRAM_BOT_TOKEN in the environment to keep it out of shell history.

Restart the daemon afterward (ccmux daemon restart) so it picks up the bridge,
then run 'ccmux telegram pair'.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			if token == "" {
				token = strings.TrimSpace(os.Getenv("CCMUX_TELEGRAM_BOT_TOKEN"))
			}
			if token == "" {
				return fmt.Errorf("a bot token is required: pass --token or set CCMUX_TELEGRAM_BOT_TOKEN")
			}
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			cfg.Telegram.Enabled = true
			cfg.Telegram.BotToken = token
			cfg.Telegram.AllowExec = allowExec
			cfg.Telegram.WebViewer = webViewer
			if cfg.Telegram.PaneTailLines == 0 {
				cfg.Telegram.PaneTailLines = config.DefaultPaneTailLines
			}
			if err := config.Save(cfg); err != nil {
				return err
			}

			// Best-effort token validation so a typo surfaces now.
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			if me, err := telegram.NewClient(token).GetMe(ctx); err == nil {
				fmt.Printf("✓ Token accepted — bot is @%s\n", me.Username)
			} else {
				fmt.Printf("⚠ Saved, but couldn't validate the token now: %v\n", err)
			}
			fmt.Println("Next: restart the daemon (ccmux daemon restart), then run `ccmux telegram pair`.")
			if allowExec {
				fmt.Println("Note: the exec tier (/run) is ENABLED — the bot can run arbitrary input.")
			}
			return nil
		},
	}
	c.Flags().StringVar(&token, "token", "", "bot token from @BotFather (or set CCMUX_TELEGRAM_BOT_TOKEN)")
	c.Flags().BoolVar(&allowExec, "allow-exec", false, "enable the /run exec tier (off by default)")
	c.Flags().BoolVar(&webViewer, "web-viewer", false, "enable the optional tailnet markdown web viewer")
	return c
}

func newTelegramPairCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pair",
		Short: "Mint a one-time code to enroll your Telegram chat",
		Long: `Asks the running daemon's bridge for a single-use pairing code, then tells
you what to send the bot. The first chat to send /start <code> is added to the
allowlist. Codes expire after 10 minutes.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			cli, err := daemon.LocalClient()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			resp, err := cli.TelegramPairCode(ctx)
			if err != nil {
				return fmt.Errorf("couldn't get a pairing code (is the daemon running with the bridge enabled? try `ccmux telegram register` then `ccmux daemon restart`): %w", err)
			}
			fmt.Println("Send this to your bot to pair this machine:")
			fmt.Printf("\n    /start %s\n\n", resp.Code)
			if resp.BotUsername != "" {
				fmt.Printf("Open the bot: https://t.me/%s\n", resp.BotUsername)
			}
			fmt.Println("The code is single-use and expires in 10 minutes.")
			return nil
		},
	}
}

func newTelegramStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show bridge configuration (token redacted)",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			tg := cfg.Telegram
			fmt.Printf("enabled:      %v\n", tg.Enabled)
			fmt.Printf("bot token:    %s\n", redactToken(tg.BotToken))
			fmt.Printf("paired chats: %d\n", len(tg.AllowedChatIDs))
			fmt.Printf("exec tier:    %v\n", tg.AllowExec)
			fmt.Printf("web viewer:   %v\n", tg.WebViewer)
			fmt.Printf("alerts muted: %v\n", tg.MuteAlerts)
			if tg.Enabled && tg.BotToken != "" && len(tg.AllowedChatIDs) == 0 {
				fmt.Println("\nNo chats paired yet — run `ccmux telegram pair`.")
			}
			return nil
		},
	}
}

func newTelegramTestCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "test",
		Short: "Send a test message to every paired chat",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			tg := cfg.Telegram
			if tg.BotToken == "" {
				return fmt.Errorf("no bot token set — run `ccmux telegram register` first")
			}
			if len(tg.AllowedChatIDs) == 0 {
				return fmt.Errorf("no paired chats — run `ccmux telegram pair` first")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			bot := telegram.NewClient(tg.BotToken)
			sent := 0
			for _, id := range tg.AllowedChatIDs {
				if _, err := bot.SendMessage(ctx, telegram.SendMessageRequest{
					ChatID: id, Text: "ccmux: test message ✅ — the bridge is wired up.",
				}); err != nil {
					fmt.Printf("✗ chat %d: %v\n", id, err)
					continue
				}
				sent++
			}
			fmt.Printf("Sent to %d/%d paired chat(s).\n", sent, len(tg.AllowedChatIDs))
			return nil
		},
	}
}

func newTelegramServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve [on|off]",
		Short: "Toggle the optional tailnet markdown web viewer",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			switch {
			case len(args) == 0:
				fmt.Printf("web viewer: %v\n", cfg.Telegram.WebViewer)
				return nil
			case strings.EqualFold(args[0], "on"):
				cfg.Telegram.WebViewer = true
			case strings.EqualFold(args[0], "off"):
				cfg.Telegram.WebViewer = false
			default:
				return fmt.Errorf("usage: ccmux telegram serve [on|off]")
			}
			if err := config.Save(cfg); err != nil {
				return err
			}
			fmt.Printf("web viewer: %v (restart the daemon to apply)\n", cfg.Telegram.WebViewer)
			return nil
		},
	}
}

// redactToken masks a bot token, showing only the trailing 4 chars so a
// human can confirm which token is set without exposing it.
func redactToken(tok string) string {
	tok = strings.TrimSpace(tok)
	if tok == "" {
		return "(not set)"
	}
	if len(tok) <= 4 {
		return "set (••••)"
	}
	return "set (••••" + tok[len(tok)-4:] + ")"
}
