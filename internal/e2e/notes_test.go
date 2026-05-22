//go:build integration

package e2e

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/notes"
)

// TestNotesBrowseAndPreview covers the browse + preview CUJ: the vault
// lists every markdown file in the project — not just docs/ — grouped
// by folder, and a selected note's content reads back.
func TestNotesBrowseAndPreview(t *testing.T) {
	proj := t.TempDir()
	writeFile(t, filepath.Join(proj, "README.md"), "# Readme\n")
	writeFile(t, filepath.Join(proj, "docs", "01_Specs", "00_Vision.md"), "# Vision\n\nthe vision text\n")
	writeFile(t, filepath.Join(proj, "docs", "02_Architecture", "00_System.md"), "# System\n")
	writeFile(t, filepath.Join(proj, "docs", "03_Agent_Logs", "2026-05-20.md"), "# Log\n")

	v := notes.Open(proj)
	entries, err := v.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// All four files, including the project-root README, are listed.
	if len(entries) != 4 {
		t.Fatalf("List returned %d entries, want 4", len(entries))
	}
	// Root-level files sort first (Dir == ""), then the docs/ tree
	// ordered by folder.
	if entries[0].Dir != "" || filepath.Base(entries[0].Rel) != "README.md" {
		t.Errorf("first entry = %+v, want the project-root README", entries[0])
	}
	if entries[1].Dir != "docs/01_Specs" {
		t.Errorf("entry[1] dir = %q, want docs/01_Specs", entries[1].Dir)
	}
	body, err := v.Read(entries[1].Rel)
	if err != nil {
		t.Fatalf("Read(%q): %v", entries[1].Rel, err)
	}
	if !strings.Contains(string(body), "the vision text") {
		t.Errorf("preview body missing expected content: %q", body)
	}
}

// TestNotesSearch covers the search CUJ: a query returns the notes
// containing it and excludes notes that do not.
func TestNotesSearch(t *testing.T) {
	proj := t.TempDir()
	writeFile(t, filepath.Join(proj, "docs", "01_Specs", "00_Auth.md"), "# Auth\n\nWe use OAuth tokens here.\n")
	writeFile(t, filepath.Join(proj, "docs", "01_Specs", "01_Storage.md"), "# Storage\n\nPostgres for everything.\n")

	v := notes.Open(proj)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	hits, err := v.Search(ctx, "OAuth", 20)
	if err != nil {
		t.Fatalf("Search(OAuth): %v", err)
	}
	if len(hits) == 0 {
		t.Fatal(`Search("OAuth") returned no hits`)
	}
	for _, h := range hits {
		if !strings.Contains(h.Path, "00_Auth.md") {
			t.Errorf("unexpected hit %q for query OAuth", h.Path)
		}
	}

	none, err := v.Search(ctx, "Kubernetes", 20)
	if err != nil {
		t.Fatalf("Search(Kubernetes): %v", err)
	}
	if len(none) != 0 {
		t.Errorf(`Search("Kubernetes") returned %d hits, want 0`, len(none))
	}
}
