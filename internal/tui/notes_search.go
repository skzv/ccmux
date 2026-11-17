package tui

import (
	"context"
	"errors"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/notes"
)

// clearSearch drops the query box and any result set, returning the
// list to the file tree.
func (m *notesModel) clearSearch() {
	m.searching = false
	m.searchInput.Blur()
	m.searchInput.SetValue("")
	m.searchResults = nil
	m.searchQuery = ""
	m.searchPartial = false
}

// applySearchResult lands a search (notesSearchResultMsg).
func (m notesModel) applySearchResult(msg notesSearchResultMsg) (notesModel, tea.Cmd) {
	// Drop results for a project (or device) the user has since
	// switched away from — they'd list, and Enter would open, the
	// old project's files.
	if m.project == nil || msg.Path != m.project.Path || msg.Host != projectHost(*m.project) {
		return m, nil
	}
	if msg.Err != "" {
		m.deviceErr = msg.Err
		return m, nil
	}
	m.deviceErr = ""
	m.searchResults = msg.Hits
	m.searchQuery = msg.Query
	m.searchPartial = msg.Partial
	m.cursor = 0
	return m, m.refreshPreview()
}

// hasActiveSearch reports whether the search results pane is showing
// (either because the user just submitted a query or because they
// haven't dismissed previous results yet).
func (m notesModel) hasActiveSearch() bool {
	return m.searchQuery != ""
}

// notesSearchTimeout caps a Notes search so a pathological query can't
// stall the TUI. A var so tests can shrink it.
var notesSearchTimeout = 3 * time.Second

// runSearch is the tea.Cmd that fires Vault.Search in the background
// and posts a notesSearchResultMsg with the hits.
func (m notesModel) runSearch(query string) tea.Cmd {
	if m.project == nil {
		return nil
	}
	label := projectHost(*m.project)
	path := m.project.Path
	timeout := notesSearchTimeout
	if label == localDeviceLabel {
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			hits, err := notes.Open(path).Search(ctx, query, 100)
			msg := notesSearchResultMsg{Query: query, Hits: hits, Host: label, Path: path}
			switch {
			case errors.Is(err, context.DeadlineExceeded):
				// Out of time: show what was found, marked partial —
				// it used to be discarded, reading as "(no matches)".
				msg.Partial = true
			case err != nil:
				msg.Err = err.Error()
			}
			return msg
		}
	}
	// Remote search hits the daemon's /v1/notes/search endpoint.
	addr, ok := m.remoteAddr(label)
	if !ok {
		host := hostLabel(label, m.hosts)
		return func() tea.Msg {
			return notesSearchResultMsg{Query: query, Err: "device " + host + " is unreachable", Host: label, Path: path}
		}
	}
	name := m.project.Name
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		dhits, err := daemon.RemoteClient(addr).SearchNotes(ctx, name, query)
		if err != nil {
			return notesSearchResultMsg{Query: query, Err: err.Error(), Host: label, Path: path}
		}
		return notesSearchResultMsg{Query: query, Hits: searchHitsFromDaemon(dhits), Host: label, Path: path}
	}
}

// truncateSearchSnippet keeps result lines from blowing the column
// when a match line is enormous. Uses runes so multi-byte chars
// survive cleanly within the budget.
func truncateSearchSnippet(s string, n int) string {
	if n <= 1 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}
