package cmd

import (
	"fmt"
	"math"
	"strconv"
	"strings"
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
		query           string
		limit           int
		since           sinceFlag
		jsonOut         bool
		includeHeadless bool
	)
	cmd := &cobra.Command{
		Use:   "list-conversations",
		Short: "List past conversations across supported coding agents",
		Long: `List past conversations every agent has had on this machine, regardless of
whether ccmux launched them. Sources:

  Claude       ~/.claude/projects/<encoded-cwd>/<uuid>.jsonl
  Codex        ~/.codex/sessions/<yyyy>/<mm>/<dd>/rollout-*.jsonl
  Antigravity  ~/.gemini/antigravity-cli/conversations/<uuid>.pb
  Muse Code    $XDG_DATA_HOME/muse/sessions/<yyyy>/<mm>/<dd>/<uuid>/session.jsonl
               (defaults to ~/.local/share/muse/sessions)

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
				Query:           query,
				Limit:           limit,
				Since:           time.Duration(since),
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
	cmd.Flags().StringVar(&query, "query", "", "search project paths, previews, or conversation IDs")
	cmd.Flags().IntVar(&limit, "limit", 0, "cap the output to N rows (default: no limit)")
	cmd.Flags().Var(&since, "since", "only conversations active within this duration (e.g. 24h, 7d, 1d12h)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit one JSON object per conversation on stdout (for scripting)")
	cmd.Flags().BoolVar(&includeHeadless, "include-headless", false, "include headless runs (claude -p / SDK, codex exec); hidden by default")
	return cmd
}

// sinceFlag is --since: a time.Duration that also accepts a day unit.
// The help text has always said "e.g. 24h, 7d", but a plain
// DurationVar rejected 7d — time.ParseDuration has no `d`.
type sinceFlag time.Duration

func (s *sinceFlag) String() string { return time.Duration(*s).String() }
func (s *sinceFlag) Type() string   { return "duration" }
func (s *sinceFlag) Set(v string) error {
	d, err := parseSince(v)
	if err != nil {
		return err
	}
	*s = sinceFlag(d)
	return nil
}

// parseSince parses a non-negative duration with an optional leading
// day count: "7d", "1.5d", "1d12h", or anything time.ParseDuration
// takes ("24h", "90m"). No Go duration unit contains a 'd', so the
// first 'd' can only be the day unit.
func parseSince(v string) (time.Duration, error) {
	s := strings.TrimSpace(v)
	bad := fmt.Errorf("invalid duration %q: want e.g. 24h, 7d or 1d12h", v)
	if s == "" {
		return 0, bad
	}
	var total time.Duration
	if i := strings.IndexByte(s, 'd'); i >= 0 {
		days, err := strconv.ParseFloat(s[:i], 64)
		if err != nil || days < 0 || math.IsInf(days, 0) || days > math.MaxInt64/float64(24*time.Hour) {
			return 0, bad
		}
		total = time.Duration(days * float64(24*time.Hour))
		s = s[i+1:]
	}
	if s != "" {
		d, err := time.ParseDuration(s)
		if err != nil || d < 0 || d > math.MaxInt64-total {
			return 0, bad
		}
		total += d
	}
	return total, nil
}

// printConversationsTable renders a compact table to stdout. Width
// budget tuned for 80-col terminals — the preview column is the
// flex space and gets truncated last.
func printConversationsTable(list []conversations.Conversation) {
	if len(list) == 0 {
		fmt.Println("No conversations found.")
		fmt.Println("Run claude / codex / agy / muse at least once to create transcripts.")
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
