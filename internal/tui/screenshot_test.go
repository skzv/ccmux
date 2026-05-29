package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/notes"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// TestRenderDeviceScreenshots is a manual artifact generator, not an
// assertion. It renders the cross-device Notes screen in a few states
// and writes the ANSI to $CCMUX_SCREENSHOT_DIR so `freeze` can turn each
// into a PNG for the PR. Gated behind CCMUX_SCREENSHOT=1 so it never runs
// in CI. Run with:
//
//	CCMUX_SCREENSHOT=1 CCMUX_SCREENSHOT_DIR=/tmp/shots \
//	  go test ./internal/tui -run TestRenderDeviceScreenshots
func TestRenderDeviceScreenshots(t *testing.T) {
	if os.Getenv("CCMUX_SCREENSHOT") == "" {
		t.Skip("set CCMUX_SCREENSHOT=1 to regenerate feature screenshots")
	}
	dir := os.Getenv("CCMUX_SCREENSHOT_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	// Force truecolor so the rendered ANSI carries the theme colors;
	// in a non-TTY test process lipgloss otherwise drops to no-color.
	lipgloss.SetColorProfile(termenv.TrueColor)

	const w, h = 112, 34
	mod := time.Date(2026, 5, 28, 9, 41, 0, 0, time.UTC)

	hosts := []hostStatus{
		{Name: "studio", Local: true, OK: true, Source: "local", Address: "unix://x"},
		{Name: "mac-mini", OK: true, Source: "configured", Address: "100.64.0.2:7474", Version: "v0.1.18"},
	}
	projects := []project.Project{
		{Name: "ccmux", Host: "local", Path: "/Users/me/Projects/ccmux"},
		{Name: "infra", Host: "mac-mini", Path: "/Users/me/Projects/infra"},
	}

	localPreview := "# Cross-Device Notes\n\n" +
		"Markdown on disk is the source of truth. ccmux is the\n" +
		"terminal-first reader.\n\n" +
		"## Why\n\n" +
		"A note written on the desktop should be readable from the\n" +
		"laptop or phone — that's the context that follows you.\n"

	remotePreview := "# Infra Runbook\n\n" +
		"## Tailscale\n\n" +
		"The daemon binds its HTTP API to the tailnet IP only\n" +
		"(`100.x.x.x:7474`), never `0.0.0.0`.\n\n" +
		"## Rotation\n\n" +
		"We rotate the bearer token nightly via the cron on mac-mini.\n"

	// --- State 1: local device ---
	local := newNotes(styles.Default(), DefaultKeymap())
	local.SetHosts(hosts)
	local.SetProjects(projects)
	local.project = &projects[0]
	local.deviceName = "local"
	writeShot(t, dir, "notes_device_local.ansi", renderNotesShot(&local, w, h,
		[]notes.Entry{
			{Rel: "README.md", Dir: "", Display: "README", Modified: mod},
			{Rel: "docs/00_Vision.md", Dir: "docs", Display: "Vision", Modified: mod},
			{Rel: "docs/01_Notes_System.md", Dir: "docs", Display: "Notes System", Modified: mod},
		}, 0, localPreview))

	// --- State 2: remote device (after pressing H) ---
	remote := newNotes(styles.Default(), DefaultKeymap())
	remote.SetHosts(hosts)
	remote.SetProjects(projects)
	remote.project = &projects[1]
	remote.deviceName = "mac-mini"
	writeShot(t, dir, "notes_device_remote.ansi", renderNotesShot(&remote, w, h,
		entriesFromDaemon([]daemon.NoteEntry{
			{Rel: "README.md", Dir: "", Display: "README", Modified: mod},
			{Rel: "runbooks/infra.md", Dir: "runbooks", Display: "Infra Runbook", Modified: mod},
			{Rel: "runbooks/oncall.md", Dir: "runbooks", Display: "Oncall", Modified: mod},
		}), 1, remotePreview))

	// --- State 3: remote search ---
	search := newNotes(styles.Default(), DefaultKeymap())
	search.SetHosts(hosts)
	search.SetProjects(projects)
	search.project = &projects[1]
	search.deviceName = "mac-mini"
	search.searchQuery = "tailscale"
	search.searchResults = searchHitsFromDaemon([]daemon.SearchHit{
		{Rel: "runbooks/infra.md", LineNum: 4, Snippet: "The daemon binds its HTTP API to the tailnet IP only"},
		{Rel: "runbooks/oncall.md", LineNum: 12, Snippet: "check that tailscale is connected on both ends"},
	})
	search.cursor = 0
	search.SetSize(w, h)
	writeShot(t, dir, "notes_device_remote_search.ansi", search.View(w, h))
}

// renderNotesShot wires entries + a preview body into a notes model and
// returns the rendered full-screen View.
func renderNotesShot(m *notesModel, w, h int, entries []notes.Entry, cursor int, preview string) string {
	m.entries = entries
	m.cursor = cursor
	m.SetSize(w, h)
	if cursor >= 0 && cursor < len(entries) {
		m.previewRel = entries[cursor].Rel
	}
	m.previewSrc = preview
	pw, _ := m.previewPaneSize()
	m.preview.SetContent(m.renderPreviewContent(pw))
	return m.View(w, h)
}

func writeShot(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
