package daemon

import "strings"

// SessionMatchesQuery is the predicate behind the Sessions screen's "/"
// filter and `ccmux list --filter`, ignoring case: the query matches a
// session whose name or project contains it, or whose host or agent
// starts with it. An empty (or all-whitespace) query matches every
// session.
//
// Host and agent match by prefix only because nearly every session
// shares them: every local session's host is "local" and the default
// agent is "claude", so a substring match made "cal" (lo-cal) or "de"
// (clau-de) match the whole list and the filter stopped narrowing.
//
// Path is not matched at all, for the same reason: sessions usually
// share a parent directory (~/Projects/…).
func SessionMatchesQuery(s SessionState, query string) bool {
	q := normalizeSessionQuery(query)
	return q == "" || sessionMatches(s, q)
}

// FilterSessions returns the sessions that match query, in their
// original order. An empty query returns ss unchanged.
func FilterSessions(ss []SessionState, query string) []SessionState {
	q := normalizeSessionQuery(query)
	if q == "" {
		return ss
	}
	out := make([]SessionState, 0, len(ss))
	for _, s := range ss {
		if sessionMatches(s, q) {
			out = append(out, s)
		}
	}
	return out
}

func normalizeSessionQuery(query string) string {
	return strings.ToLower(strings.TrimSpace(query))
}

// sessionMatches applies an already-normalized, non-empty query.
func sessionMatches(s SessionState, q string) bool {
	return strings.Contains(strings.ToLower(s.Name), q) ||
		strings.Contains(strings.ToLower(s.Project), q) ||
		strings.HasPrefix(strings.ToLower(s.Host), q) ||
		strings.HasPrefix(strings.ToLower(s.Agent), q)
}
