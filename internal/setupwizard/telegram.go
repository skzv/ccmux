package setupwizard

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/charmbracelet/huh"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemonservice"
	"github.com/skzv/ccmux/internal/telegram"
)

// stepTelegram offers to set up the Telegram bridge — the channel that
// lets the user approve/deny and drive sessions from a phone or watch.
// Idempotent: a re-run on an already-configured install just reports
// status. Discoverability is the point; most users won't know the bridge
// exists otherwise.
func stepTelegram(ctx context.Context, out io.Writer) error {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(out, "  %s couldn't read config: %v\n", stWarn.Render("⚠"), err)
		return nil
	}

	if cfg.Telegram.Enabled && strings.TrimSpace(cfg.Telegram.BotToken) != "" {
		fmt.Fprintf(out, "  %s Telegram bridge already configured (%d chat(s) paired)\n",
			stOK.Render("✓"), len(cfg.Telegram.AllowedChatIDs))
		if len(cfg.Telegram.AllowedChatIDs) == 0 {
			fmt.Fprintln(out, stMuted.Render("    pair a chat with `ccmux telegram pair`"))
		}
		return nil
	}

	fmt.Fprintln(out, stMuted.Render("  ccmux can run a Telegram bot so you can approve/deny and drive"))
	fmt.Fprintln(out, stMuted.Render("  sessions from your phone or watch. Needs a token from @BotFather."))

	setup, err := confirm(ctx, false,
		"Set up the Telegram bridge?",
		"Create a bot with @BotFather, then paste its token. Stored in ~/.config/ccmux/config.toml; only chats you pair can drive it.",
		"Yes, set it up",
		"No, skip")
	if err != nil {
		return err
	}
	if !setup {
		fmt.Fprintln(out, stMuted.Render("  skipped — set it up later with `ccmux telegram register`"))
		return nil
	}
	if assumeYes(ctx) {
		// Non-interactive run can't safely prompt for a secret.
		fmt.Fprintln(out, stMuted.Render("  non-interactive — set it later: `ccmux telegram register --token <token>`"))
		return nil
	}

	var token string
	in := huh.NewInput().
		Title("Bot token from @BotFather").
		EchoMode(huh.EchoModePassword).
		Value(&token)
	if err := huh.NewForm(huh.NewGroup(in)).Run(); err != nil {
		return err
	}
	token = strings.TrimSpace(token)
	if token == "" {
		fmt.Fprintln(out, stMuted.Render("  no token entered — skipped"))
		return nil
	}

	vctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if me, gerr := telegram.NewClient(token).GetMe(vctx); gerr == nil {
		fmt.Fprintf(out, "  %s token accepted — bot is @%s\n", stOK.Render("✓"), me.Username)
	} else {
		fmt.Fprintf(out, "  %s saved, but couldn't validate the token now: %v\n", stWarn.Render("⚠"), gerr)
	}

	cfg.Telegram.Enabled = true
	cfg.Telegram.BotToken = token
	if cfg.Telegram.PaneTailLines == 0 {
		cfg.Telegram.PaneTailLines = config.DefaultPaneTailLines
	}
	if err := config.Save(cfg); err != nil {
		return err
	}
	// The bridge reads config at daemon startup, so a daemon that's
	// already running needs a bounce to pick up the token. Restart it
	// here so setup is seamless; on a fresh machine where the daemon
	// isn't up yet, the autostart step below starts it with the token
	// already in config.
	switch restarted, rerr := daemonservice.RestartIfRunning(); {
	case rerr != nil:
		fmt.Fprintf(out, "  %s saved, but couldn't restart the daemon: %v — run `ccmux daemon restart`\n", stWarn.Render("⚠"), rerr)
	case restarted:
		fmt.Fprintln(out, stOK.Render("  ✓")+" saved + restarted the daemon — the bridge is starting.")
	default:
		fmt.Fprintln(out, stMuted.Render("  saved — the bridge starts with the daemon (the autostart step handles it)."))
	}
	fmt.Fprintln(out, stMuted.Render("  Then run `ccmux telegram pair` to enroll your chat."))
	return nil
}
