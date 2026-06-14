package tui

import (
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/tui/styles"
)

func TestNetwork_TelegramStatusLine(t *testing.T) {
	m := newNetwork(styles.Default(), DefaultKeymap())

	// Off: points the user at CLI registration.
	m.SetTelegram(false, 0)
	off := m.View(120, 30)
	if !strings.Contains(off, "Telegram: off") || !strings.Contains(off, "ccmux telegram register") {
		t.Errorf("off state missing or wrong:\n%s", m.telegramStatusLine())
	}

	// On with chats: shows the count and the pair hint.
	m.SetTelegram(true, 2)
	on := m.View(120, 30)
	if !strings.Contains(on, "Telegram: on") || !strings.Contains(on, "2 chats paired") {
		t.Errorf("on state missing count:\n%s", m.telegramStatusLine())
	}
	if !strings.Contains(on, "pair") {
		t.Errorf("on state should advertise the T pair action")
	}

	// Singular grammar.
	m.SetTelegram(true, 1)
	if !strings.Contains(m.telegramStatusLine(), "1 chat paired") {
		t.Errorf("singular grammar wrong: %s", m.telegramStatusLine())
	}
}

func TestNetwork_TelegramHelpHint(t *testing.T) {
	m := newNetwork(styles.Default(), DefaultKeymap())
	props := m.HelpBarProps(120)
	var found bool
	for _, h := range props.Hints {
		if h.Key == "T" && h.Label == "telegram" {
			found = true
		}
	}
	if !found {
		t.Errorf("network help bar should advertise the T telegram hint")
	}
}
