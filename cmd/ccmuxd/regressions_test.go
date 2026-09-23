package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/apns"
	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/fcm"
	"github.com/skzv/ccmux/internal/tmux"
)

// TestPollOnce_ShellSessionIsNotJudgedAsClaude — a bare shell session
// has no project sidecar; untagged it resolved to Claude, and Claude's
// "shell prompt = crashed" rule showed a plain zsh prompt as error.
func TestPollOnce_ShellSessionIsNotJudgedAsClaude(t *testing.T) {
	s := newPollTestServer(t)
	s.list = func(context.Context) ([]tmux.Session, error) {
		return []tmux.Session{{Name: "c-shell-1", Path: "/tmp", Agent: tmux.ShellAgentTag}}, nil
	}
	s.capture = func(context.Context, string, int) (string, error) { return "skz@mac ~ % ", nil }
	pollNTimes(s, 3)
	tr := s.seen["c-shell-1"]
	if tr.state != agent.StateIdle || tr.agentID != shellAgentID {
		t.Errorf("shell session: state=%s agent=%q, want idle/shell", tr.state, tr.agentID)
	}
}

// TestBareSessionAgentTag — the tag follows the same precedence as the
// launch command (request, then configured default, then shell).
func TestBareSessionAgentTag(t *testing.T) {
	for _, tc := range []struct{ req, def, want string }{
		{"codex", "claude", "codex"},
		{"", "codex", "codex"},
		{"shell", "codex", "shell"},
		{"", "", "shell"},
		{"bogus", "", "shell"},
		{"", "shell", "shell"},
	} {
		if got := bareSessionAgentTag(tc.req, tc.def); got != tc.want {
			t.Errorf("bareSessionAgentTag(%q, %q) = %q, want %q", tc.req, tc.def, got, tc.want)
		}
	}
}

// TestPollOnce_VanishedSessionPublishesKilled — a session killed outside
// the daemon (TUI/CLI tmux kill) must reach event-stream subscribers,
// or they keep a ghost row forever.
func TestPollOnce_VanishedSessionPublishesKilled(t *testing.T) {
	s := newPollTestServer(t)
	live := []tmux.Session{{Name: "c-gone", Path: "/tmp"}}
	s.list = func(context.Context) ([]tmux.Session, error) { return live, nil }
	s.capture = func(context.Context, string, int) (string, error) { return "x", nil }
	pollNTimes(s, 1)

	ch := s.events.Subscribe()
	defer s.events.Unsubscribe(ch)
	live = nil
	pollNTimes(s, 1)
	deadline := time.After(time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Kind == "killed" && ev.Session.Name == "c-gone" {
				return
			}
		case <-deadline:
			t.Fatal("no killed event for a session that disappeared")
		}
	}
}

type fakeAPNs struct {
	mu    sync.Mutex
	sends []string
}

func (f *fakeAPNs) Enabled() bool { return true }
func (f *fakeAPNs) Send(_ context.Context, token, _ string, _ apns.Notification) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sends = append(f.sends, token)
	return nil
}

type fakeFCM struct {
	mu    sync.Mutex
	sends []string
}

func (f *fakeFCM) Enabled() bool { return true }
func (f *fakeFCM) Send(token string, _ fcm.Notification) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sends = append(f.sends, token)
	return nil
}

// TestHandleTestPush_RoutesByProvider — a test push for an Android
// (FCM) device must go to FCM. It used to go to APNs, which rejected the
// token as BadDeviceToken, and the dead-token cleanup then deleted the
// phone's registration.
func TestHandleTestPush_RoutesByProvider(t *testing.T) {
	home := t.TempDir()
	store, err := daemon.OpenDeviceStore(filepath.Join(home, "devices.json"))
	if err != nil {
		t.Fatal(err)
	}
	const pub = "ssh-ed25519 AAAAandroid phone"
	if err := store.RegisterWithProvider(pub, "fcm-token", daemon.ProviderFCM, ""); err != nil {
		t.Fatal(err)
	}
	ap, fc := &fakeAPNs{}, &fakeFCM{}
	s := &server{devices: store, apnsSender: ap, fcmSender: fc,
		apnsSlots: make(chan struct{}, 4), fcmSlots: make(chan struct{}, 4)}

	body, _ := json.Marshal(map[string]string{"public_key": pub})
	rec := httptest.NewRecorder()
	s.handleTestPush(rec, httptest.NewRequest(http.MethodPost, "/v1/devices/test", bytes.NewReader(body)))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		fc.mu.Lock()
		n := len(fc.sends)
		fc.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	ap.mu.Lock()
	defer ap.mu.Unlock()
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if len(ap.sends) != 0 || len(fc.sends) != 1 {
		t.Errorf("apns sends=%v fcm sends=%v, want the FCM token sent via FCM only", ap.sends, fc.sends)
	}
	if _, ok := store.Lookup(pub); !ok {
		t.Error("the device registration was removed")
	}
}
