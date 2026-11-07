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
		{"codex", true},     // agent
		{"Projects", false}, // path is not matched
		{"claude", false},
	}
	for _, tc := range cases {
		if got := SessionMatchesQuery(s, tc.query); got != tc.want {
			t.Errorf("SessionMatchesQuery(%q) = %v, want %v", tc.query, got, tc.want)
		}
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
