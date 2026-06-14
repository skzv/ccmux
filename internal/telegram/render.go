package telegram

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/skzv/ccmux/internal/daemon"
)

const (
	// telegramMaxMessageChars is the Bot API per-message text limit.
	telegramMaxMessageChars = 4096
	// maxInlineCodeChars is the threshold past which a code body is sent
	// as a document instead of an (HTML) message, to stay clear of the
	// per-message limit even after escaping.
	maxInlineCodeChars = 3500
)

func errUnknownHost(host string) error { return fmt.Errorf("unknown host %q", host) }

// clampPlain truncates plain text to a rune budget, appending an ellipsis
// marker when it cuts. Operates on rune boundaries so it never splits a
// multibyte character.
func clampPlain(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	const tail = "\n…(truncated)"
	keep := limit - utf8.RuneCountInString(tail)
	if keep < 0 {
		keep = 0
	}
	runes := []rune(s)
	return string(runes[:keep]) + tail
}

// stateGlyph maps a session state to a one-rune status marker.
func stateGlyph(state string) string {
	switch state {
	case "needs_input":
		return "🔔"
	case "active":
		return "⚙️"
	case "idle":
		return "✅"
	case "error":
		return "❗"
	default:
		return "•"
	}
}

func stateLabel(state string) string {
	switch state {
	case "needs_input":
		return "needs input"
	case "":
		return "unknown"
	default:
		return state
	}
}

// formatSessions renders the fan-out session list, one row per session,
// host-qualified so a peer session is unambiguous.
func formatSessions(ss []daemon.SessionState) string {
	var sb strings.Builder
	sb.WriteString("Sessions:\n")
	for _, s := range ss {
		t := Target{Host: s.Host, Session: s.Name}
		sb.WriteString(stateGlyph(s.State) + " " + t.String())
		meta := []string{}
		if s.Project != "" {
			meta = append(meta, s.Project)
		}
		if s.Agent != "" {
			meta = append(meta, s.Agent)
		}
		if len(meta) > 0 {
			sb.WriteString("  " + strings.Join(meta, "/"))
		}
		sb.WriteString("  — " + stateLabel(s.State) + "\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// formatUsage renders the per-agent usage card compactly.
func formatUsage(u daemon.AgentUsage) string {
	var sb strings.Builder
	sb.WriteString("Usage (rolling window):\n")
	rows := []struct {
		name string
		s    daemon.UsageSummary
	}{
		{"claude", u.Claude},
		{"codex", u.Codex},
		{"antigravity", u.Antigravity},
	}
	any := false
	for _, r := range rows {
		if !r.s.HasData {
			continue
		}
		any = true
		fmt.Fprintf(&sb, "• %s: %d prompts, %dk in / %dk out, $%.2f\n",
			r.name, r.s.Prompts, r.s.InputTokens/1000, r.s.OutputTokens/1000, r.s.EstimatedCost)
	}
	for _, o := range u.Others {
		if !o.Usage.HasData {
			continue
		}
		any = true
		fmt.Fprintf(&sb, "• %s: %d prompts, %dk in / %dk out\n",
			o.Agent, o.Usage.Prompts, o.Usage.InputTokens/1000, o.Usage.OutputTokens/1000)
	}
	if u.OpenRouter.Enabled {
		any = true
		if u.OpenRouter.Limit > 0 {
			fmt.Fprintf(&sb, "• OpenRouter: $%.2f / $%.2f\n", u.OpenRouter.Usage, u.OpenRouter.Limit)
		} else {
			fmt.Fprintf(&sb, "• OpenRouter: $%.2f spent\n", u.OpenRouter.Usage)
		}
	}
	if !any {
		return "No usage recorded in the current window."
	}
	return strings.TrimRight(sb.String(), "\n")
}

// codeMessage formats a header plus a monospaced body as an HTML message
// (Telegram renders <pre> as a code block). Everything is HTML-escaped so
// arbitrary pane content can't break the parse.
func codeMessage(header, body string) (text, parseMode string) {
	return escapeHTMLMin(header) + "\n<pre>" + escapeHTMLMin(body) + "</pre>", "HTML"
}

// escapeHTMLMin escapes the three characters Telegram's HTML parse mode
// is sensitive to. Order matters: & first.
func escapeHTMLMin(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// tailString returns the last n non-empty-trimmed lines of s, preserving
// order. Used to cap pane content before it leaves the machine.
func tailString(s string, n int) string {
	if n <= 0 {
		return s
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) <= n {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}
