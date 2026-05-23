package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/conversations"
)

// newListConversationsCmd: `ccmux list-conversations` prints a flat
// table of past agent conversations (Claude + Codex + Antigravity)
// sorted by recency. This is the CLI mirror of the Conversations
// TUI screen — same data source, same row order — useful for
// scripting and as the end-to-end smoke test of the data layer.
//
// Surface note: the eventual user-facing entry point is the TUI
// screen + a `ccmux resume` command that picks one to launch. This
// `list-conversations` command stays for `--json` scripting and
// remote-host probing.
func newListConversationsCmd() *cobra.Command {
	var (
		limit           int
		since           time.Duration
		jsonOut         bool
		includeHeadless bool
	)
	cmd := &cobra.Command{
		Use:   "list-conversations",
		Short: "List past agent conversations across Claude, Codex, and Antigravity",
		Long: `List past conversations every agent has had on this machine, regardless of
whether ccmux launched them. Sources:

  Claude       ~/.claude/projects/<encoded-cwd>/<uuid>.jsonl
  Codex        ~/.codex/sessions/<yyyy>/<mm>/<dd>/rollout-*.jsonl
  Antigravity  ~/.gemini/antigravity-cli/conversations/<uuid>.pb

Antigravity transcripts are opaque protobuf, so the preview column is
empty for those rows. ID and last-activity are always populated.

Headless runs are hidden by default — that's Claude ` + "`claude -p`" + ` / SDK
invocations (entrypoint=sdk-cli) and Codex ` + "`codex exec`" + ` runs
(originator=codex_exec). Antigravity transcripts carry no headless tag
so those rows are always shown. Pass --include-headless to see them,
or set conversations.show_headless=true in config.

Default ordering is most-recent first.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			// Resolve the "show headless" preference: CLI flag wins, then
			// config, then default (hide). Treating the flag as a hard
			// override matches the TUI's H toggle — both surfaces let
			// the user override config for one invocation.
			showHeadless := includeHeadless
			if !showHeadless {
				if cfg, err := config.Load(); err == nil {
					showHeadless = cfg.Conversations.ShowHeadless
				}
			}
			list, err := conversations.All(conversations.Options{
				Limit:           limit,
				Since:           since,
				ExcludeHeadless: !showHeadless,
			})
			if err != nil {
				return err
			}
			if jsonOut {
				return printConversationsJSON(list)
			}
			printConversationsTable(list)
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0, "cap the output to N rows (default: no limit)")
	cmd.Flags().DurationVar(&since, "since", 0, "only conversations active within this duration (e.g. 24h, 7d)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit one JSON object per conversation on stdout (for scripting)")
	cmd.Flags().BoolVar(&includeHeadless, "include-headless", false, "include headless runs (claude -p / SDK, codex exec); hidden by default")
	return cmd
}

// printConversationsTable renders a compact table to stdout. Width
// budget tuned for 80-col terminals — the preview column is the
// flex space and gets truncated last.
func printConversationsTable(list []conversations.Conversation) {
	if len(list) == 0 {
		fmt.Println("No conversations found.")
		fmt.Println("Run claude / codex / agy at least once to create transcripts.")
		return
	}
	const (
		agentW = 12
		whenW  = 16
		idW    = 12
	)
	fmt.Printf("%-*s  %-*s  %-*s  %s\n", agentW, "AGENT", whenW, "LAST ACTIVE", idW, "ID", "PREVIEW / PROJECT")
	fmt.Printf("%s\n", repeat("-", 70))
	for _, c := range list {
		when := relativeTime(c.LastActivity)
		idShort := c.ID
		if len(idShort) > idW {
			idShort = idShort[:idW-1] + "…"
		}
		preview := c.Preview
		if preview == "" {
			preview = "(" + c.Project + ")"
		}
		fmt.Printf("%-*s  %-*s  %-*s  %s\n", agentW, c.Agent, whenW, when, idW, idShort, preview)
	}
}

// printConversationsJSON dumps the slice as a JSON array on stdout.
// Useful for scripting ("which conversation did I have yesterday on
// the auth project?"). Keys match the struct's exported fields.
func printConversationsJSON(list []conversations.Conversation) error {
	// json.Encoder writes a trailing newline; that's what most CLI
	// tools expect.
	enc := newStdoutEncoder()
	for _, c := range list {
		if err := enc.Encode(c); err != nil {
			return err
		}
	}
	return nil
}

// relativeTime formats a timestamp as "5m ago", "2h ago", "3d ago",
// etc. Good enough for a CLI list — the user can drill in with --json
// for absolute timestamps.
func relativeTime(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}

func repeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}
