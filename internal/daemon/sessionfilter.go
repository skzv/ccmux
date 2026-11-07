package daemon

import "strings"

// SessionMatchesQuery is the predicate behind the Sessions screen's "/"
// filter and `ccmux list --filter`: a case-insensitive substring match
// on the session's name, project, host, and agent. An empty (or
// all-whitespace) query matches every session.
//
// Path is intentionally not matched: sessions usually share a parent
// directory (~/Projects/…), so a query like "proj" or "/" would match
// every row and the filter would stop narrowing.
func SessionMatchesQuery(s SessionState, query string) bool {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return true
	}
	for _, field := range []string{s.Name, s.Project, s.Host, s.Agent} {
		if strings.Contains(strings.ToLower(field), q) {
			return true
		}
	}
	return false
}

// FilterSessions returns the sessions that match query, in their
// original order. An empty query returns ss unchanged.
func FilterSessions(ss []SessionState, query string) []SessionState {
	if strings.TrimSpace(query) == "" {
		return ss
	}
	out := make([]SessionState, 0, len(ss))
	for _, s := range ss {
		if SessionMatchesQuery(s, query) {
			out = append(out, s)
		}
	}
	return out
}
