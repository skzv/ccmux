package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/tui/styles"
)

func TestQuickReplyForm_SubmitAndEmptyValidation(t *testing.T) {
	f := newQuickReplyForm(styles.Default(), "mini", "c-api")
	// Empty submit keeps form open with inline error.
	f, cmd := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("empty quick reply should not emit submit cmd")
	}
	if f.err == "" {
		t.Fatal("expected inline error for empty quick reply")
	}

	// Non-empty submit emits quickReplySubmitMsg carrying host + session + text.
	f.input.SetValue("  yes, go ahead  ")
	f, cmd = f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected submit cmd")
	}
	msg, ok := cmd().(quickReplySubmitMsg)
	if !ok {
		t.Fatalf("got %T, want quickReplySubmitMsg", msg)
	}
	if msg.Host != "mini" || msg.Name != "c-api" || msg.Text != "yes, go ahead" {
		t.Fatalf("unexpected submit payload: %+v", msg)
	}
}

func TestSanitizePreviewANSI_KeepsSGRStripsControl(t *testing.T) {
	in := "\x1b[32mgreen\x1b[0m \x1b]0;evil-title\x07\x1b[2Jplain\x07\r\nnext"
	got := sanitizePreviewANSI(in)
	want := "\x1b[32mgreen\x1b[0m plain\nnext"
	if got != want {
		t.Fatalf("sanitizePreviewANSI = %q, want %q", got, want)
	}
}

func TestSlicePreviewLines_ScrollUpAndLiveTail(t *testing.T) {
	src := "l1\nl2\nl3\nl4\nl5\nl6"
	if got := slicePreviewLines(src, 3, 20, 0); got != "l4\nl5\nl6" {
		t.Fatalf("live tail = %q, want l4..l6", got)
	}
	if got := slicePreviewLines(src, 3, 20, 2); got != "l2\nl3\nl4" {
		t.Fatalf("scrolled up 2 = %q, want l2..l4", got)
	}
	// Excessive scroll clamps cleanly to top window.
	if got := slicePreviewLines(src, 3, 20, 99); got != "l1\nl2\nl3" {
		t.Fatalf("clamped top = %q, want l1..l3", got)
	}
}

func TestSessions_MuteToggleAndPreviewScrollKeys(t *testing.T) {
	st := styles.Default()
	m := newSessions(st, DefaultKeymap())
	m.SetSessions([]daemon.SessionState{{Name: "c-app", Host: "local", State: "active", Muted: false}})

	// Idle spinner must not re-arm when preview isn't loading.
	if _, cmd := m.Update(spinner.TickMsg{}); cmd != nil {
		t.Fatal("idle sessionsModel re-armed preview spinner")
	}

	// Pressing `m` emits sessionMuteToggleMsg with Muted=true.
	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	if cmd == nil {
		t.Fatal("expected mute toggle cmd")
	}
	toggle, ok := cmd().(sessionMuteToggleMsg)
	if !ok || toggle.Name != "c-app" || !toggle.Muted {
		t.Fatalf("unexpected mute msg: %T %+v", toggle, toggle)
	}

	// SetSessionMuted updates row rendering with muted badge.
	m.SetSessionMuted("local", "c-app", true)
	row := renderSessionLine(st, m.sessions[0], 80)
	if !strings.Contains(row, "⊘") {
		t.Errorf("muted session row missing ⊘ badge: %q", row)
	}

	// Scroll keys adjust previewScroll while preview is open, G resets to live tail.
	m.showPreview = true
	m.preview = strings.Repeat("line\n", 30)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	if m.previewScroll != 10 {
		t.Fatalf("after ctrl+u previewScroll = %d, want 10", m.previewScroll)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	if m.previewScroll != 0 {
		t.Fatalf("after G previewScroll = %d, want 0", m.previewScroll)
	}
}

func TestApp_QuickReplyAndMuteRouteToRemoteHost(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := New(config.Defaults(), "test")
	a.hosts = []hostStatus{{Name: "mini", Address: "100.64.0.2:7474", OK: true}}

	var replyAddr, replyName, replyText string
	origReply := sendQuickReplyRemoteCmd
	sendQuickReplyRemoteCmd = func(addr, host, name, text string) tea.Cmd {
		replyAddr, replyName, replyText = addr, name, text
		return func() tea.Msg { return quickReplySentMsg{Host: host, Name: name} }
	}
	defer func() { sendQuickReplyRemoteCmd = origReply }()

	a, cmd := updateApp(t, a, quickReplySubmitMsg{Host: "mini", Name: "c-api", Text: "y"})
	if cmd == nil {
		t.Fatal("expected quickReply remote cmd")
	}
	_ = cmd()
	if replyAddr != "100.64.0.2:7474" || replyName != "c-api" || replyText != "y" {
		t.Fatalf("remote quickReply routed to (%q, %q, %q)", replyAddr, replyName, replyText)
	}

	var muteAddr, muteName string
	var muteVal bool
	origMute := muteRemoteSessionCmd
	muteRemoteSessionCmd = func(addr, host, name string, muted bool) tea.Cmd {
		muteAddr, muteName, muteVal = addr, name, muted
		return func() tea.Msg { return sessionMutedMsg{Host: host, Name: name, Muted: muted} }
	}
	defer func() { muteRemoteSessionCmd = origMute }()

	_, cmd = updateApp(t, a, sessionMuteToggleMsg{Host: "mini", Name: "c-api", Muted: true})
	if cmd == nil {
		t.Fatal("expected mute remote cmd")
	}
	_ = cmd()
	if muteAddr != "100.64.0.2:7474" || muteName != "c-api" || !muteVal {
		t.Fatalf("remote mute routed to (%q, %q, %v)", muteAddr, muteName, muteVal)
	}
}
