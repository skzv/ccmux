package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/tmux"
)

// TestPollOnce_AttachAndDetachArePublished — attaching to a session and
// detaching from it change its attached flag (and attaching marks it
// reviewed), but no event carried either: an event-stream client showed
// it detached and unreviewed until its next state change.
func TestPollOnce_AttachAndDetachArePublished(t *testing.T) {
	s := newPollTestServer(t)
	pushes := countPushes(t, s)
	_, p, _ := waitingSession(t, s, false) // needs input, unreviewed
	ch := s.events.Subscribe()
	defer s.events.Unsubscribe(ch)
	f := newFakeTmux()
	f.wire(s)
	ts := tmux.Session{Name: "c-wait", Path: "/tmp", Created: time.Now().Add(-time.Hour)}

	for _, attached := range []bool{true, false} {
		ts.Attached = attached
		f.addSession(ts, p)
		pollNTimes(s, 2)
		evs := publishedEvents(ch)
		if len(evs) != 1 {
			t.Fatalf("attached=%v: events %+v, want exactly one", attached, evs)
		}
		if ev := evs[0]; ev.Kind != "state_change" || ev.Session.Attached != attached || !ev.Session.Seen || ev.Session.State != "needs_input" {
			t.Errorf("attached=%v: event %s %+v, want a state_change with attached=%v, seen, needs_input", attached, ev.Kind, ev.Session, attached)
		}
	}
	if got := pushes(); got != 1 {
		t.Errorf("attach/detach pushed: %d pushes, want only the turn's 1", got)
	}
}

// TestCreateSession_ResponseHasTmuxFields — POST /v1/sessions answered
// windows:0 and a zero last_change for the session it had just started,
// which GET /v1/sessions never reports for a live session.
func TestCreateSession_ResponseHasTmuxFields(t *testing.T) {
	s, root, logPath := newCreateTestServer(t)
	mkdir(t, filepath.Join(root, "proj"))
	created := time.Now().Add(-time.Second).Truncate(time.Second)
	s.list = func(context.Context) ([]tmux.Session, error) {
		if len(tmuxCallsWith(t, logPath, "new-session")) == 0 { // the looks before creating
			return nil, nil
		}
		return []tmux.Session{{Name: "c-proj", Path: filepath.Join(root, "proj"), Windows: 1, Created: created}}, nil
	}
	rec := post(t, s.createSession, "/v1/sessions", daemon.NewSessionRequest{Project: "proj"})
	var got daemon.SessionState
	if err := json.Unmarshal(rec.Body.Bytes(), &got); rec.Code != http.StatusOK || err != nil {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got.Windows != 1 || !got.Created.Equal(created) || !got.LastChange.Equal(created) {
		t.Errorf("response windows=%d created=%v last_change=%v, want 1 / %v / %v", got.Windows, got.Created, got.LastChange, created, created)
	}
}

// TestPollOnce_ShutdownMidTickLogsNothing — stopping the daemon with a
// poll tick in flight logged one "capture-pane X: context canceled" per
// session: errors the daemon's own cancellation caused.
func TestPollOnce_ShutdownMidTickLogsNothing(t *testing.T) {
	s := newPollTestServer(t)
	s.pollBudget = 30 * time.Second
	s.list = func(context.Context) ([]tmux.Session, error) {
		return []tmux.Session{{Name: "c-a", Path: "/tmp"}, {Name: "c-b", Path: "/tmp"}, {Name: "c-c", Path: "/tmp"}}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	var once sync.Once
	s.capture = func(ctx context.Context, _ string, _ int) (string, error) {
		once.Do(cancel) // shutdown lands while the tick captures
		<-ctx.Done()
		return "", ctx.Err()
	}
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	s.pollOnce(ctx, time.Second)
	if strings.Contains(buf.String(), "capture-pane") {
		t.Errorf("shutdown logged capture errors:\n%s", buf.String())
	}

	// A capture that fails on its own is still logged.
	buf.Reset()
	s.capture = func(context.Context, string, int) (string, error) { return "", os.ErrPermission }
	s.pollOnce(context.Background(), time.Second)
	if n := strings.Count(buf.String(), "capture-pane"); n != 3 {
		t.Errorf("a real capture failure logged %d times, want once per session:\n%s", n, buf.String())
	}
}
