package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skzv/ccmux/internal/config"
	"github.com/skzv/ccmux/internal/daemon"
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

// seedSearchProject creates a discoverable project (CLAUDE.md marks it for
// project.Discover) with one note containing a known needle, and returns
// the projects-root for a server cfg.
func seedSearchProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	projDir := filepath.Join(root, "proj")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, "CLAUDE.md"), []byte("# proj"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, "README.md"),
		[]byte("intro\nwe use tailscale here\nbye"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestHandleNotesSearch_ReturnsHits — a known query finds the seeded line.
func TestHandleNotesSearch_ReturnsHits(t *testing.T) {
	s := &server{cfg: config.Config{Projects: config.ProjectsConfig{Root: seedSearchProject(t)}}}
	rec := httptest.NewRecorder()
	s.handleNotesSearch(rec, httptest.NewRequest(http.MethodGet, "/v1/notes/search?project=proj&q=tailscale", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	var hits []daemon.SearchHit
	if err := json.Unmarshal(rec.Body.Bytes(), &hits); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("expected at least one hit for 'tailscale'")
	}
	if !strings.Contains(hits[0].Snippet, "tailscale") {
		t.Errorf("snippet = %q, want it to contain 'tailscale'", hits[0].Snippet)
	}
	if hits[0].Rel != "README.md" {
		t.Errorf("rel = %q, want README.md", hits[0].Rel)
	}
}

// TestHandleNotesSearch_UnknownProject404 — same validation as handleNotes.
func TestHandleNotesSearch_UnknownProject404(t *testing.T) {
	s := &server{cfg: config.Config{Projects: config.ProjectsConfig{Root: t.TempDir()}}}
	rec := httptest.NewRecorder()
	s.handleNotesSearch(rec, httptest.NewRequest(http.MethodGet, "/v1/notes/search?project=nothere&q=x", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown project: status = %d, want 404", rec.Code)
	}
}

// TestHandleNotesSearch_RequiresQuery — missing q rejects with 400.
func TestHandleNotesSearch_RequiresQuery(t *testing.T) {
	s := &server{cfg: config.Config{Projects: config.ProjectsConfig{Root: seedSearchProject(t)}}}
	rec := httptest.NewRecorder()
	s.handleNotesSearch(rec, httptest.NewRequest(http.MethodGet, "/v1/notes/search?project=proj", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing q: status = %d, want 400", rec.Code)
	}
}

// TestHandleNotesSearch_RejectsBadMethod — GET-only.
func TestHandleNotesSearch_RejectsBadMethod(t *testing.T) {
	s := &server{cfg: config.Config{Projects: config.ProjectsConfig{Root: t.TempDir()}}}
	rec := httptest.NewRecorder()
	s.handleNotesSearch(rec, httptest.NewRequest(http.MethodPost, "/v1/notes/search?project=any&q=x", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST status = %d, want 405", rec.Code)
	}
}
