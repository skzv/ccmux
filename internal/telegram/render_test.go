package telegram

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/skzv/ccmux/internal/daemon"
)

func TestClampPlain(t *testing.T) {
	if got := clampPlain("short", 100); got != "short" {
		t.Errorf("under-limit text should pass through, got %q", got)
	}
	long := strings.Repeat("a", 5000)
	got := clampPlain(long, telegramMaxMessageChars)
	if utf8.RuneCountInString(got) > telegramMaxMessageChars {
		t.Errorf("clamped text still over limit: %d", utf8.RuneCountInString(got))
	}
	if !strings.HasSuffix(got, "truncated)") {
		t.Errorf("clamp should mark truncation")
	}
}

func TestTailString(t *testing.T) {
	in := "a\nb\nc\nd\ne"
	if got := tailString(in, 2); got != "d\ne" {
		t.Errorf("tailString last 2 = %q, want d\\ne", got)
	}
	if got := tailString(in, 10); got != in {
		t.Errorf("tailString more than available should return all")
	}
}

func TestCodeMessage_BalancedAndEscaped(t *testing.T) {
	text, mode := codeMessage("Preview x:y", "a < b & c > d")
	if mode != "HTML" {
		t.Errorf("mode = %q, want HTML", mode)
	}
	if !strings.Contains(text, "&lt;") || !strings.Contains(text, "&amp;") || !strings.Contains(text, "&gt;") {
		t.Errorf("special chars not escaped: %q", text)
	}
	if strings.Count(text, "<pre>") != 1 || strings.Count(text, "</pre>") != 1 {
		t.Errorf("unbalanced <pre> tags: %q", text)
	}
}

// TestPreview_LongOutputAttachedAsDocument is the output-limit safety:
// a pane bigger than one message is attached, never sent as an
// over-limit message.
func TestPreview_LongOutputAttachedAsDocument(t *testing.T) {
	big := strings.Repeat("x", 5000)
	local := &fakeDaemon{previews: map[string]string{"build": big}}
	b, bot := newTestBridge(Options{PaneTailLines: 1000}, local, nil)

	b.handleUpdate(context.Background(), msgUpdate(7, "/preview local:build 1000"))
	if len(bot.docs) != 1 {
		t.Fatalf("oversized preview should be attached as a document, got %d docs", len(bot.docs))
	}
	for _, s := range bot.sent {
		if utf8.RuneCountInString(s.Text) > telegramMaxMessageChars {
			t.Fatalf("sent an over-limit message (%d chars)", utf8.RuneCountInString(s.Text))
		}
	}
}

func TestFormatSessions_HostQualified(t *testing.T) {
	ss := []daemon.SessionState{
		{Name: "build", Host: "local", State: "needs_input", Project: "ccmux", Agent: "claude"},
		{Name: "api", Host: "mini", State: "active"},
	}
	got, mode := formatSessions(ss)
	if mode != "HTML" {
		t.Errorf("formatSessions should return HTML mode, got %q", mode)
	}
	if !strings.Contains(got, "local:build") || !strings.Contains(got, "mini:api") {
		t.Errorf("sessions should be host-qualified: %q", got)
	}
	if !strings.Contains(got, "needs input") {
		t.Errorf("state label missing: %q", got)
	}
	if !strings.Contains(got, "<code>") {
		t.Errorf("session ids should be monospaced: %q", got)
	}
}

func TestQuoteMessage_ExpandableAndEscaped(t *testing.T) {
	text, mode := quoteMessage("🔔 "+htmlBold("local:build")+" needs input", "exit code <1> & done")
	if mode != "HTML" {
		t.Errorf("mode = %q, want HTML", mode)
	}
	if !strings.Contains(text, "<blockquote expandable>") || !strings.Contains(text, "</blockquote>") {
		t.Errorf("want an expandable blockquote: %q", text)
	}
	// Body special chars escaped; the header's <b> tags are preserved.
	if !strings.Contains(text, "&lt;1&gt;") || !strings.Contains(text, "&amp;") {
		t.Errorf("body not escaped: %q", text)
	}
	if !strings.Contains(text, "<b>local:build</b>") {
		t.Errorf("header bold should survive: %q", text)
	}
}

func TestStripHTML(t *testing.T) {
	cases := map[string]string{
		"<b>hi</b>":                "hi",
		"a &amp; b":                "a & b",
		"<code>x &lt;y&gt;</code>": "x <y>",
		"plain":                    "plain",
	}
	for in, want := range cases {
		if got := stripHTML(in); got != want {
			t.Errorf("stripHTML(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSend_ParseErrorFallsBackToPlain proves rich formatting degrades
// gracefully: if Telegram rejects the HTML entities, the message is
// resent as clean, tag-free plain text rather than lost.
func TestSend_ParseErrorFallsBackToPlain(t *testing.T) {
	b, bot := newTestBridge(Options{}, &fakeDaemon{}, nil)
	bot.sendHook = func(req SendMessageRequest) (*Message, error) {
		if req.ParseMode == "HTML" {
			return nil, &APIError{Method: "sendMessage", Code: 400, Description: "Bad Request: can't parse entities"}
		}
		return nil, nil // the plain retry succeeds
	}

	b.send(context.Background(), SendMessageRequest{
		ChatID: 7, Text: "<b>a &amp; b</b> <code>x</code>", ParseMode: "HTML",
	})

	if len(bot.sent) != 2 {
		t.Fatalf("want HTML attempt + plain retry = 2 sends, got %d", len(bot.sent))
	}
	retry := bot.sent[1]
	if retry.ParseMode != "" {
		t.Errorf("retry should drop parse_mode, got %q", retry.ParseMode)
	}
	if strings.ContainsAny(retry.Text, "<>") {
		t.Errorf("retry text should be tag-free: %q", retry.Text)
	}
	if !strings.Contains(retry.Text, "a & b") || !strings.Contains(retry.Text, "x") {
		t.Errorf("retry should preserve content: %q", retry.Text)
	}
}
