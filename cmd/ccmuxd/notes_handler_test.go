package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/config"
)

// TestHandleNotes_RejectsTraversal pins the path-traversal guard.
// Regression-shaped as a real CVE: notes.Vault.Read trusts its input,
// so a refactor that drops this guard turns /v1/notes into an
// arbitrary-file-read endpoint reachable over the tailnet.
func TestHandleNotes_RejectsTraversal(t *testing.T) {
	root := t.TempDir()
	projDir := filepath.Join(root, "proj")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Seed a real note so the "list" code path has something to find.
	if err := os.WriteFile(filepath.Join(projDir, "README.md"), []byte("# hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Drop the secret OUTSIDE the project, where a successful traversal
	// would read it.
	if err := os.WriteFile(filepath.Join(root, "secret.txt"), []byte("password"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := &server{
		cfg: config.Config{Projects: config.ProjectsConfig{Root: root}},
	}

	cases := []struct {
		name string
		file string
	}{
		{"absolute path", "/etc/passwd"},
		{"absolute windows", `\Windows\system32\config\sam`},
		{"single dotdot", "../secret.txt"},
		{"nested dotdot", "docs/../../secret.txt"},
		{"trailing dotdot", "docs/.."},
		{"non-md file", "Makefile"},
		{"non-md txt", "README.txt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet,
				"/v1/notes?project=proj&file="+tc.file, nil)
			s.handleNotes(rec, req)
			if rec.Code == http.StatusOK {
				t.Errorf("traversal %q got 200 — guard missing! body=%s", tc.file, rec.Body)
			}
			if !strings.Contains(rec.Body.String(), "password") {
				return // good — the secret didn't escape
			}
			t.Errorf("traversal %q leaked the secret: %s", tc.file, rec.Body)
		})
	}
}

// TestHandleNotes_RejectsBadMethod — GET-only.
func TestHandleNotes_RejectsBadMethod(t *testing.T) {
	s := &server{cfg: config.Config{Projects: config.ProjectsConfig{Root: t.TempDir()}}}
	for _, m := range []string{http.MethodPost, http.MethodDelete, http.MethodPut} {
		rec := httptest.NewRecorder()
		s.handleNotes(rec, httptest.NewRequest(m, "/v1/notes?project=any", nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s status = %d, want 405", m, rec.Code)
		}
	}
}

// TestHandleNotes_RequiresProject — missing query rejects with 400.
func TestHandleNotes_RequiresProject(t *testing.T) {
	s := &server{cfg: config.Config{Projects: config.ProjectsConfig{Root: t.TempDir()}}}
	rec := httptest.NewRecorder()
	s.handleNotes(rec, httptest.NewRequest(http.MethodGet, "/v1/notes", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing project: status = %d, want 400", rec.Code)
	}
}

// TestHandleNotes_UnknownProject404 — caller can only address projects
// the daemon's discovery sees.
func TestHandleNotes_UnknownProject404(t *testing.T) {
	s := &server{cfg: config.Config{Projects: config.ProjectsConfig{Root: t.TempDir()}}}
	rec := httptest.NewRecorder()
	s.handleNotes(rec, httptest.NewRequest(http.MethodGet, "/v1/notes?project=nothere", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown project: status = %d, want 404", rec.Code)
	}
}
