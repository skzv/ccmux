package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/tmux"
)

// multiWindowSession is a session whose agent runs in window 0 (%3)
// while the user sits in a shell window (%4, the active pane).
func multiWindowSession(t *testing.T) (*server, *fakeTmux) {
	t.Helper()
	s := newPollTestServer(t)
	f := newFakeTmux()
	f.addSession(tmux.Session{Name: "c-multi", Path: "/tmp", Created: time.Now().Add(-time.Hour)},
		&fakePane{Pane: tmux.Pane{ID: "%3", Width: 120, Height: 40}, body: "agent: allow this edit? (y/n)\n"},
		&fakePane{Pane: tmux.Pane{ID: "%4", Window: 1, Width: 120, Height: 40, Active: true}, body: "user@host ~ % \n"})
	f.wire(s)
	return s, f
}

// TestSendKeys_TargetsAgentPaneNotActivePane — /send-keys targeted the
// session (`=name:`), i.e. its active pane. With the agent in window 0
// and a shell window active, a phone's or an MCP client's reply meant
// for the agent was typed into the shell — where it would run. The keys
// must reach the pane the daemon classifies.
func TestSendKeys_TargetsAgentPaneNotActivePane(t *testing.T) {
	s, f := multiWindowSession(t)
	for _, keys := range []string{"y", "Enter"} {
		if rec := serve(s, http.MethodPost, "/v1/sessions/c-multi/send-keys", `{"keys":"`+keys+`"}`); rec.Code != http.StatusNoContent {
			t.Fatalf("send-keys %q: status %d (%s)", keys, rec.Code, rec.Body)
		}
	}
	if got, want := f.sentKeys(), []string{"%3=y", "%3=Enter"}; !slices.Equal(got, want) {
		t.Errorf("keys landed in %v, want %v (the agent's pane, not the active shell %%4)", got, want)
	}
}

// TestSendKeys_UsesThePollLoopsPane — the pane the poll loop remembers
// reading wins over the oldest pane, the same way the poll loop keeps
// it (a split made in front of the agent, a replacement pane); a
// remembered id the session no longer has is ignored.
func TestSendKeys_UsesThePollLoopsPane(t *testing.T) {
	s := newPollTestServer(t)
	f := newFakeTmux()
	f.addSession(tmux.Session{Name: "c-split", Path: "/tmp"},
		&fakePane{Pane: tmux.Pane{ID: "%2"}},
		&fakePane{Pane: tmux.Pane{ID: "%8", Index: 1}},
		&fakePane{Pane: tmux.Pane{ID: "%9", Window: 1, Active: true}})
	f.wire(s)

	s.seen["c-split"] = &tracked{}
	s.seen["c-split"].pane.id = "%8"
	if rec := serve(s, http.MethodPost, "/v1/sessions/c-split/send-keys", `{"keys":"a"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("status %d (%s)", rec.Code, rec.Body)
	}
	// A pane id from an earlier tmux server: not in this session's list.
	s.seen["c-split"].pane.id = "%40"
	if rec := serve(s, http.MethodPost, "/v1/sessions/c-split/send-keys", `{"keys":"b"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("status %d (%s)", rec.Code, rec.Body)
	}
	if got, want := f.sentKeys(), []string{"%8=a", "%2=b"}; !slices.Equal(got, want) {
		t.Errorf("keys landed in %v, want %v", got, want)
	}
}

// TestSendKeys_FallsBackToSessionWhenPanesUnresolvable — only when the
// agent's pane can't be resolved at all does send-keys use the session
// target; a missing session still answers 404.
func TestSendKeys_FallsBackToSessionWhenPanesUnresolvable(t *testing.T) {
	s, f := multiWindowSession(t)
	s.panes = func(context.Context, string) ([]tmux.Pane, error) {
		return nil, errors.New("list-panes: server exited")
	}
	if rec := serve(s, http.MethodPost, "/v1/sessions/c-multi/send-keys", `{"keys":"x"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("status %d (%s)", rec.Code, rec.Body)
	}
	if got, want := f.sentKeys(), []string{"%4=x"}; !slices.Equal(got, want) {
		t.Errorf("keys landed in %v, want %v (session-target fallback)", got, want)
	}

	s, _ = multiWindowSession(t)
	if rec := serve(s, http.MethodPost, "/v1/sessions/nope/send-keys", `{"keys":"x"}`); rec.Code != http.StatusNotFound {
		t.Errorf("missing session: status %d (%s), want 404", rec.Code, rec.Body)
	}
}

// TestSendKeys_ResolvedPaneFailureIsNotRetriedOnActivePane — once the
// agent's pane is resolved, a failed send (the pane exited in between)
// is an error, not a retry on whatever pane is active.
func TestSendKeys_ResolvedPaneFailureIsNotRetriedOnActivePane(t *testing.T) {
	s, f := multiWindowSession(t)
	s.sendKeysPane = func(context.Context, string, string) error { return errors.New("can't find pane: %3") }
	if rec := serve(s, http.MethodPost, "/v1/sessions/c-multi/send-keys", `{"keys":"y"}`); rec.Code == http.StatusNoContent {
		t.Error("a failed send to the agent's pane reported success")
	}
	if got := f.sentKeys(); len(got) != 0 {
		t.Errorf("keys went to %v after the agent's pane failed; want nowhere", got)
	}
}

// TestPreview_ReadsAgentPaneNotActivePane — /preview (and MCP
// read_pane) showed the active shell window instead of the agent the
// session's state describes.
func TestPreview_ReadsAgentPaneNotActivePane(t *testing.T) {
	s, _ := multiWindowSession(t)
	rec := serve(s, http.MethodGet, "/v1/sessions/c-multi/preview?lines=5", "")
	var got daemon.PreviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); rec.Code != http.StatusOK || err != nil {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if want := "agent: allow this edit? (y/n)\n"; got.Content != want {
		t.Errorf("preview = %q, want the agent's pane %q", got.Content, want)
	}
}
