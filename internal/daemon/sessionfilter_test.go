package daemon

import (
	"reflect"
	"testing"
)

func TestSessionMatchesQuery(t *testing.T) {
	s := SessionState{
		Name:    "c-ccmux-website",
		Host:    "mini",
		Project: "ccmux-website",
		Path:    "/Users/u/Projects/ccmux-website",
		Agent:   "codex",
	}
	cases := []struct {
		query string
		want  bool
	}{
		{"", true},
		{"   ", true},
		{"website", true},   // name / project
		{"WEBSITE", true},   // case-insensitive
		{" website ", true}, // surrounding whitespace ignored
		{"mini", true},      // host
		{"MIN", true},       // host prefix, case-insensitive
		{"ini", false},      // host matches by prefix, not substring
		{"codex", true},     // agent
		{"cod", true},       // agent prefix
		{"dex", false},      // agent matches by prefix, not substring
		{"Projects", false}, // path is not matched
		{"claude", false},
	}
	for _, tc := range cases {
		if got := SessionMatchesQuery(s, tc.query); got != tc.want {
			t.Errorf("SessionMatchesQuery(%q) = %v, want %v", tc.query, got, tc.want)
		}
	}
}

// Every local session has host "local" and the default agent is
// "claude". Fragments of those words must not match the whole list.
func TestSessionMatchesQuery_SharedHostAndAgentDoNotMatchBySubstring(t *testing.T) {
	s := SessionState{Name: "c-api", Host: "local", Project: "api", Agent: "claude"}
	for _, q := range []string{"cal", "oca", "al", "de", "aud", "laude"} {
		if SessionMatchesQuery(s, q) {
			t.Errorf("SessionMatchesQuery(%q) = true, want false (substring of host/agent)", q)
		}
	}
	for _, q := range []string{"local", "loc", "claude", "cl", "api"} {
		if !SessionMatchesQuery(s, q) {
			t.Errorf("SessionMatchesQuery(%q) = false, want true", q)
		}
	}
	cal := SessionState{Name: "c-calendar", Host: "local", Project: "calendar", Agent: "claude"}
	got := FilterSessions([]SessionState{s, cal}, "cal")
	if len(got) != 1 || got[0].Name != "c-calendar" {
		t.Errorf("FilterSessions(cal) = %v, want only c-calendar", got)
	}
}

func TestFilterSessions(t *testing.T) {
	ss := []SessionState{
		{Name: "c-ccmux", Host: "local"},
		{Name: "c-dotfiles", Host: "local"},
		{Name: "c-ccmux-website", Host: "mini"},
	}
	names := func(ss []SessionState) []string {
		out := []string{}
		for _, s := range ss {
			out = append(out, s.Name)
		}
		return out
	}
	if got := names(FilterSessions(ss, "")); !reflect.DeepEqual(got, names(ss)) {
		t.Errorf("empty query = %v, want all sessions", got)
	}
	if got, want := names(FilterSessions(ss, "ccmux")), []string{"c-ccmux", "c-ccmux-website"}; !reflect.DeepEqual(got, want) {
		t.Errorf("query ccmux = %v, want %v (order preserved)", got, want)
	}
	if got := FilterSessions(ss, "nope"); len(got) != 0 {
		t.Errorf("no-match query = %v, want empty", names(got))
	}
}
