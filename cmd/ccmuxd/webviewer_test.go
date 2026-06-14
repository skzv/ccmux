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

// seedProject writes a minimal discoverable project with one note.
func seedProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	if err := os.MkdirAll(filepath.Join(proj, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	// CLAUDE.md makes it a discoverable project; a note to serve.
	if err := os.WriteFile(filepath.Join(proj, "CLAUDE.md"), []byte("# proj"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "docs", "vision.md"), []byte("# Vision\nthe why"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func viewerServer(root string) *server {
	cfg := config.Defaults()
	cfg.Projects.Root = root
	return &server{cfg: cfg}
}

func TestWebViewer_ServesScopedVault(t *testing.T) {
	s := viewerServer(seedProject(t))
	h := s.webViewerHandler()

	// Vault index lists the note.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/notes/proj", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "docs/vision.md") {
		t.Fatalf("vault index = %d, body %q", rec.Code, rec.Body.String())
	}

	// The file renders as markdown.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/notes/proj/docs/vision.md", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("file = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
		t.Errorf("content-type = %q, want text/markdown", ct)
	}
	if !strings.Contains(rec.Body.String(), "the why") {
		t.Errorf("body missing note content")
	}
}

func TestWebViewer_RejectsTraversalAndNonMarkdown(t *testing.T) {
	s := viewerServer(seedProject(t))
	h := s.webViewerHandler()

	for _, bad := range []string{
		"/notes/proj/../../../etc/passwd",
		"/notes/proj/docs/../../secret.md",
		"/notes/proj/notes.txt",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, bad, nil))
		if rec.Code == http.StatusOK {
			t.Errorf("expected refusal for %q, got 200", bad)
		}
	}
}

func TestWebViewer_UnknownProject404(t *testing.T) {
	s := viewerServer(seedProject(t))
	rec := httptest.NewRecorder()
	s.webViewerHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/notes/ghost", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown project = %d, want 404", rec.Code)
	}
}

func TestWebViewer_DisabledReturnsNoURL(t *testing.T) {
	s := viewerServer(t.TempDir())
	s.cfg.Telegram.WebViewer = false
	if got := s.startWebViewer(nil); got != "" {
		t.Errorf("disabled viewer should return no URL, got %q", got)
	}
}

func TestViewerSafeRel(t *testing.T) {
	good := []string{"docs/x.md", "a.md"}
	bad := []string{"../x.md", "/etc/passwd", "docs/../../x.md", "a.txt", ""}
	for _, g := range good {
		if !viewerSafeRel(g) {
			t.Errorf("viewerSafeRel(%q) = false", g)
		}
	}
	for _, b := range bad {
		if viewerSafeRel(b) {
			t.Errorf("viewerSafeRel(%q) = true", b)
		}
	}
}
