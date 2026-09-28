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
	// sent records every send-keys as "<pane id>=<keys>" — the pane the
	// keys landed in, whether targeted by id or through a session's
	// active pane.
	sent []string
	// marks records every spinner mark written as "<session>=<agent>";
	// the session's list row carries it from then on (Session.Spinner),
	// as tmux keeps a session's options.
	marks []string
	// reviews records every review record written, per session; the
	// session's list row carries the latest (Session.Review).
	reviews map[string][]tmux.Review
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

// renameSession renames a session behind the daemon's back, the way
// `tmux rename-session` does: same panes, same options (its tags and
// review record), same creation time — only the name changes.
func (f *fakeTmux) renameSession(oldName, newName string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ts, panes := f.session[oldName], f.sessions[oldName]
	ts.Name = newName
	delete(f.session, oldName)
	delete(f.sessions, oldName)
	f.session[newName], f.sessions[newName] = ts, panes
	for i, n := range f.order {
		if n == oldName {
			f.order[i] = newName
		}
	}
}

// setAttached attaches a client to the session, or detaches it.
func (f *fakeTmux) setAttached(name string, attached bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ts := f.session[name]
	ts.Attached = attached
	f.session[name] = ts
}

// reviewWrites returns every review record written on the session, in order.
func (f *fakeTmux) reviewWrites(name string) []tmux.Review {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]tmux.Review(nil), f.reviews[name]...)
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
	s.sendKeysPane = func(_ context.Context, id, keys string) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, ps := range f.sessions {
			for _, p := range ps {
				if p.ID == id {
					f.sent = append(f.sent, id+"="+keys)
					return nil
				}
			}
		}
		return errors.New("can't find pane: " + id)
	}
	s.sendKeys = func(_ context.Context, name, keys string) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		p, err := active(name)
		if err != nil {
			return err
		}
		f.sent = append(f.sent, p.ID+"="+keys)
		return nil
	}
	s.markSpinner = func(_ context.Context, name, id string) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		ts, ok := f.session[name]
		if !ok {
			return errNoFakeSession
		}
		ts.Spinner = id
		f.session[name] = ts
		f.marks = append(f.marks, name+"="+id)
		return nil
	}
	s.markReview = func(_ context.Context, name string, r tmux.Review) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		ts, ok := f.session[name]
		if !ok {
			return errNoFakeSession
		}
		ts.Review = r
		f.session[name] = ts
		if f.reviews == nil {
			f.reviews = map[string][]tmux.Review{}
		}
		f.reviews[name] = append(f.reviews[name], r)
		return nil
	}
}

func (f *fakeTmux) spinnerMarks() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.marks...)
}

func (f *fakeTmux) sentKeys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sent...)
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
