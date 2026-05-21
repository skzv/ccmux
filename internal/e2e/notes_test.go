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
// lists every markdown file under docs/, grouped by section, and a
// selected note's content reads back.
func TestNotesBrowseAndPreview(t *testing.T) {
	proj := t.TempDir()
	writeFile(t, filepath.Join(proj, "docs", "01_Specs", "00_Vision.md"), "# Vision\n\nthe vision text\n")
	writeFile(t, filepath.Join(proj, "docs", "02_Architecture", "00_System.md"), "# System\n")
	writeFile(t, filepath.Join(proj, "docs", "03_Agent_Logs", "2026-05-20.md"), "# Log\n")

	v := notes.Open(proj)
	entries, err := v.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("List returned %d entries, want 3", len(entries))
	}
	// Specs sort ahead of Architecture and Agent Logs.
	if entries[0].Section != notes.SectionSpecs {
		t.Errorf("first entry section = %v, want Specs", entries[0].Section)
	}
	body, err := v.Read(entries[0].Rel)
	if err != nil {
		t.Fatalf("Read(%q): %v", entries[0].Rel, err)
	}
	if !strings.Contains(string(body), "the vision text") {
		t.Errorf("preview body missing expected content: %q", body)
	}
}

// TestNotesCreate covers the templated-note CUJ: Spec / ADR / Agent Log
// quick-actions create files at the right path, with frontmatter, and
// auto-numbered or date-stamped filenames.
func TestNotesCreate(t *testing.T) {
	proj := t.TempDir()
	v := notes.Open(proj)

	spec, err := v.NewSpec("My First Spec")
	if err != nil {
		t.Fatalf("NewSpec: %v", err)
	}
	if d := filepath.Base(filepath.Dir(spec)); d != "01_Specs" {
		t.Errorf("spec parent dir = %q, want 01_Specs", d)
	}
	if base := filepath.Base(spec); !strings.HasPrefix(base, "00_") {
		t.Errorf("first spec filename = %q, want a 00_ prefix", base)
	}
	if body := readFile(t, spec); !strings.Contains(body, "title: My First Spec") {
		t.Errorf("spec frontmatter missing title: %q", body)
	}

	// A second spec auto-increments the numeric prefix.
	spec2, err := v.NewSpec("Second Spec")
	if err != nil {
		t.Fatalf("NewSpec (2): %v", err)
	}
	if base := filepath.Base(spec2); !strings.HasPrefix(base, "01_") {
		t.Errorf("second spec filename = %q, want a 01_ prefix", base)
	}

	adr, err := v.NewADR("Some Decision")
	if err != nil {
		t.Fatalf("NewADR: %v", err)
	}
	if d := filepath.Base(filepath.Dir(adr)); d != "02_Architecture" {
		t.Errorf("ADR parent dir = %q, want 02_Architecture", d)
	}

	logPath, created, err := v.NewAgentLog("c-demo")
	if err != nil {
		t.Fatalf("NewAgentLog: %v", err)
	}
	if !created {
		t.Errorf("NewAgentLog reported created=false for a fresh log")
	}
	today := time.Now().Format("2006-01-02")
	if base := filepath.Base(logPath); base != today+".md" {
		t.Errorf("agent log filename = %q, want %s.md", base, today)
	}
	if body := readFile(t, logPath); !strings.Contains(body, "c-demo") {
		t.Errorf("agent log missing the session-start entry: %q", body)
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
