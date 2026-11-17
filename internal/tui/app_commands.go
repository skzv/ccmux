package tui

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/ccusage"
	"github.com/skzv/ccmux/internal/claudeauth"
	"github.com/skzv/ccmux/internal/conversations"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/moshi"
	"github.com/skzv/ccmux/internal/openrouterusage"
	"github.com/skzv/ccmux/internal/selfupdate"
	"github.com/skzv/ccmux/internal/tmux"
	"github.com/skzv/ccmux/internal/usage"
)

// checkForUpdateCmd runs selfupdate.Check off the UI goroutine and
// delivers the outcome as an updateCheckMsg. Bounded by a generous
// timeout — the embedded `git fetch` needs network, but a hung fetch
// must never wedge the TUI.
func checkForUpdateCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		res, err := selfupdate.Check(ctx)
		return updateCheckMsg{Result: res, Err: err}
	}
}

// tierDetectedMsg carries the result of the async `claude auth status`
// probe. Tier is the normalized ccmux tier, or "api" when undetectable.
type tierDetectedMsg struct{ Tier string }

// detectTierCmd runs the subscription-tier probe off the UI goroutine.
// It used to run synchronously in New(), which stalled the first frame
// for ~0.9s on every launch while the `claude` Node CLI booted —
// claudeauth's cache is per-process, so a fresh ccmux never hits it.
// Deferring it lets the TUI paint immediately; the quota bar's plan
// limit fills in a beat later when tierDetectedMsg lands.
func detectTierCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		st, err := claudeauth.Get(ctx)
		if err != nil {
			return tierDetectedMsg{Tier: "api"}
		}
		return tierDetectedMsg{Tier: st.Tier()}
	}
}

// moshiDetectedMsg carries the result of an async moshi.Detect probe.
type moshiDetectedMsg struct{ State moshi.Status }

// detectMoshiCmd runs the moshi/moshi-hook status probe off the UI
// goroutine. moshi.Detect shells out (launchctl/brew on macOS) and used
// to run synchronously in newSettings — a 2s startup stall on every
// launch — and again inline in settingsModel.Update every 30s, which
// froze the TUI mid-session. Both paths now go through this command.
func detectMoshiCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		return moshiDetectedMsg{State: moshi.Detect(ctx)}
	}
}

// usageTick fires every 15s — usage.WalkClaudeBlock scans the transcript
// tree which can be several MB, so we don't want it on every 2s heart-
// beat. The dashboard happily shows the previous value while the next
// walk runs in the background.
func usageTick() tea.Cmd {
	return tea.Tick(15*time.Second, func(t time.Time) tea.Msg { return usageTickMsg{At: t} })
}

func (a App) refreshUsageCmd() tea.Cmd {
	return func() tea.Msg {
		// Claude's rich panel reads the active 5-hour session block
		// (every field of it), through the same walk the daemon reports
		// to phones and `ccmux usage` as claude_block. The other agents'
		// rows cover a plain rolling window of the same length.
		const window = 5 * time.Hour
		agg, claudeErr := usage.WalkClaudeBlock()
		codex, _ := usage.WalkCodex(window)
		antigravity, _ := usage.WalkAntigravity(window)
		others := usage.WalkOthers(window)
		// Run ccusage alongside the transcript walk — it's an npx
		// invocation so it takes a second, but it's the most accurate
		// source for billing-block burn rate and projections.
		var blk *ccusageBlock
		if b, err := ccusage.CurrentBlock(context.Background()); err == nil {
			blk = &ccusageBlock{
				CostUSD:             b.CostUSD,
				BurnRateCostPerHour: b.BurnRateCostPerHour,
				ProjectedTotalCost:  b.ProjectedTotalCost,
				EndTime:             b.EndTime,
				IsActive:            b.IsActive,
			}
		}
		return usageLoadedMsg{Agg: agg, Codex: codex, Antigravity: antigravity, Others: others, CcusageBlock: blk, OpenRouter: a.openRouterSpend(), Err: claudeErr}
	}
}

// openRouterSpend fetches the configured OpenRouter account spend for
// the dashboard's usage panel. Returns a disabled (zero) value with no
// network call when [openrouter] isn't enabled — the common case. A
// configured-but-failing fetch returns Enabled=true with ErrMsg so the
// panel shows why the figure is missing rather than a silent blank.
func (a App) openRouterSpend() daemon.OpenRouterSpend {
	or := a.cfg.OpenRouter
	if !or.Enabled || strings.TrimSpace(or.APIKey) == "" {
		return daemon.OpenRouterSpend{Enabled: false}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	sp, err := openrouterusage.New(or.APIKey, or.BaseURL).Spend(ctx)
	if err != nil {
		return daemon.OpenRouterSpend{Enabled: true, ErrMsg: err.Error()}
	}
	return daemon.OpenRouterSpend{
		Enabled:    true,
		Usage:      sp.Usage,
		Limit:      sp.Limit,
		Remaining:  sp.Remaining,
		IsFreeTier: sp.IsFreeTier,
	}
}

// loadConvPreviewCmd reads the most recent ~30 messages from the
// selected conversation's transcript so the `p`-key preview overlay
// can render them. Antigravity short-circuits to an empty slice — the
// overlay surfaces "preview unavailable" itself.
func (a App) loadConvPreviewCmd(c conversations.Conversation) tea.Cmd {
	return func() tea.Msg {
		msgs, err := conversations.RecentMessages(c, previewMessageLimit)
		return conversationPreviewLoadedMsg{ID: c.ID, Messages: msgs, Err: err}
	}
}

// refreshConversationsCmd loads the full conversations list. Fired on
// Conversations-tab entry and on the Refresh keybind while the screen
// is focused. Walks ~/.claude, ~/.codex, ~/.gemini/antigravity-cli —
// can take a beat on machines with hundreds of transcripts, hence the
// loading-state placeholder in the screen.
//
// Headless / SDK rows are hidden unless the user has toggled them on
// (live, via H) or set conversations.show_headless=true in config.
// The live toggle takes priority over the config so a temporary peek
// at automation runs doesn't require a config edit.
//
// Each load is numbered: the walk can take a while, so two quick
// refreshes (pressing H twice) may finish out of order, and only the
// newest may be applied.
func (a *App) refreshConversationsCmd() tea.Cmd {
	a.convLoadGen++
	gen := a.convLoadGen
	exclude := !a.conversationsM.showHeadless
	return func() tea.Msg {
		list, err := conversations.All(conversations.Options{
			ExcludeHeadless: exclude,
		})
		return conversationsLoadedMsg{List: list, Err: err, Gen: gen}
	}
}

// editorReloadMsg is the message that refreshes the screen which opened
// $EDITOR, once the editor exits.
func editorReloadMsg(source string) tea.Msg {
	switch source {
	case "settings":
		return configReloadMsg{}
	case "agents":
		return agentsReloadMsg{}
	default:
		return notesReloadMsg{}
	}
}

// renameSessionCmd runs `tmux rename-session` on the LOCAL tmux server
// and returns the result. Remote rows go through renameSessionTargetCmd
// → renameRemoteSessionCmd instead. A package var so tests can observe
// routing without a live tmux.
var renameSessionCmd = func(oldName, newName string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		err := tmux.Rename(ctx, oldName, newName)
		return sessionRenamedMsg{OldName: oldName, NewName: newName, Err: err}
	}
}
