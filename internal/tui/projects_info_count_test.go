package tui

import (
	"testing"

	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/project"
)

// TestCountSessionsForProject_ExactOrNumbered — the count used to be a
// bare prefix match, so project `api` claimed `c-api-server` (another
// project's session). Only the canonical name and the numbered siblings
// uniqueSessionName mints (`c-api-2`, …) belong to the project.
func TestCountSessionsForProject_ExactOrNumbered(t *testing.T) {
	sessions := []daemon.SessionState{
		{Name: "c-api", Host: "local"},
		{Name: "c-api-2", Host: ""},
		{Name: "c-api-1714000000000", Host: "local"}, // uniqueSessionName's timestamp fallback
		{Name: "c-api-server", Host: "local"},        // project `api-server`
		{Name: "c-api-server-2", Host: "local"},
		{Name: "c-api-", Host: "local"},
		{Name: "c-apix", Host: "local"},
		{Name: "c-api", Host: "mini"}, // same name on another host
	}
	cases := []struct {
		p    project.Project
		want int
	}{
		{project.Project{Name: "api"}, 3},
		{project.Project{Name: "api-server"}, 2},
		{project.Project{Name: "api", Host: "mini"}, 1},
		{project.Project{Name: "apix"}, 1},
	}
	for _, tc := range cases {
		if got := countSessionsForProject(tc.p, sessions); got != tc.want {
			t.Errorf("countSessionsForProject(%s@%s) = %d, want %d", tc.p.Name, projectHost(tc.p), got, tc.want)
		}
	}
}
