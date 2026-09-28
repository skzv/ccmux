package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/skzv/ccmux/internal/tmux"
)

// fakeTmux is a scripted tmux server for hermetic pollOnce tests: each
// session has panes with a body, an OSC title and a size that a test
// changes between ticks, the way a real agent (or a resize) would.
// wire points every poll seam at it — the pane-by-id path (panes,
// capturePane) and the session-target fallback (capture, paneTitle),
// which reads the session's active pane exactly as tmux resolves a
// bare `=name:` target.
type fakeTmux struct {
	mu       sync.Mutex
	sessions map[string][]*fakePane
	session  map[string]tmux.Session
	order    []string
	reads    []string // pane ids capturePane was asked for, in order
}

type fakePane struct {
	tmux.Pane
	body string
}

func newFakeTmux() *fakeTmux {
	return &fakeTmux{sessions: map[string][]*fakePane{}, session: map[string]tmux.Session{}}
}

// addSession registers a session (as list-sessions reports it) with its
// panes, in window/pane order.
func (f *fakeTmux) addSession(ts tmux.Session, panes ...*fakePane) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.session[ts.Name]; !ok {
		f.order = append(f.order, ts.Name)
	}
	f.session[ts.Name] = ts
	f.sessions[ts.Name] = panes
}

// removeSession ends a session.
func (f *fakeTmux) removeSession(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.session, name)
	delete(f.sessions, name)
	for i, n := range f.order {
		if n == name {
			f.order = append(f.order[:i], f.order[i+1:]...)
			break
		}
	}
}

// update runs fn with the lock held, for changing pane state between ticks.
func (f *fakeTmux) update(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

func (f *fakeTmux) paneReads() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.reads...)
}

var errNoFakeSession = errors.New("can't find session")

func (f *fakeTmux) wire(s *server) {
	s.list = func(context.Context) ([]tmux.Session, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		out := make([]tmux.Session, 0, len(f.order))
		for _, n := range f.order {
			out = append(out, f.session[n])
		}
		return out, nil
	}
	s.panes = func(_ context.Context, name string) ([]tmux.Pane, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		ps, ok := f.sessions[name]
		if !ok {
			return nil, errNoFakeSession
		}
		out := make([]tmux.Pane, 0, len(ps))
		for _, p := range ps {
			out = append(out, p.Pane)
		}
		return out, nil
	}
	s.capturePane = func(_ context.Context, id string, _ int) (string, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.reads = append(f.reads, id)
		for _, ps := range f.sessions {
			for _, p := range ps {
				if p.ID == id {
					return p.body, nil
				}
			}
		}
		return "", errors.New("can't find pane: " + id)
	}
	active := func(name string) (*fakePane, error) {
		ps, ok := f.sessions[name]
		if !ok || len(ps) == 0 {
			return nil, errNoFakeSession
		}
		for _, p := range ps {
			if p.Active {
				return p, nil
			}
		}
		return ps[0], nil
	}
	s.capture = func(_ context.Context, name string, _ int) (string, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		p, err := active(name)
		if err != nil {
			return "", err
		}
		return p.body, nil
	}
	s.paneTitle = func(_ context.Context, name string) (string, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		p, err := active(name)
		if err != nil {
			return "", nil
		}
		return p.Title, nil
	}
}

// readFixture returns a pane fixture from internal/agent/testdata/panes.
func readFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "internal", "agent", "testdata", "panes", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
