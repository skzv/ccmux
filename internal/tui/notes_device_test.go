package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/skzv/ccmux/internal/daemon"
	"github.com/skzv/ccmux/internal/notes"
	"github.com/skzv/ccmux/internal/project"
	"github.com/skzv/ccmux/internal/tui/styles"
)

// TestEntriesFromDaemon_RoundTrip — the adapter that lets remote notes
// render through the same list code as local ones must preserve every
// field the renderer reads. Path is intentionally dropped (it's a remote
// absolute path, meaningless locally).
func TestEntriesFromDaemon_RoundTrip(t *testing.T) {
	mod := time.Now().UTC().Truncate(time.Second)
	in := []daemon.NoteEntry{
		{Rel: "README.md", Dir: "", Display: "README", Modified: mod},
		{Rel: "docs/Design.md", Dir: "docs", Display: "Design", Modified: mod},
	}
	got := entriesFromDaemon(in)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	want := []notes.Entry{
		{Rel: "README.md", Dir: "", Display: "README", Modified: mod},
		{Rel: "docs/Design.md", Dir: "docs", Display: "Design", Modified: mod},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestSearchHitsFromDaemon_RoundTrip mirrors the above for search hits.
func TestSearchHitsFromDaemon_RoundTrip(t *testing.T) {
	in := []daemon.SearchHit{{Rel: "a.md", LineNum: 3, Snippet: "hello"}}
	got := searchHitsFromDaemon(in)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Rel != "a.md" || got[0].LineNum != 3 || got[0].Snippet != "hello" {
		t.Errorf("hit = %+v", got[0])
	}
}

// twoDeviceModel builds a Notes model with the local machine plus one
// reachable remote ("mac-mini"), and one project on each device.
func twoDeviceModel() notesModel {
	m := newNotes(styles.Default(), DefaultKeymap())
	m.SetHosts([]hostStatus{
		{Name: "thiscomputer", Local: true, OK: true, Source: "local", Address: "unix://x"},
		{Name: "mac-mini", OK: true, Source: "configured", Address: "100.64.0.2:7474"},
	})
	m.SetProjects([]project.Project{
		{Name: "local-proj", Host: "local", Path: "/home/me/local-proj"},
		{Name: "remote-proj", Host: "mac-mini", Path: "/home/me/remote-proj"},
	})
	return m
}

// TestSelectableDeviceLabels_LocalFirst — the toggle's device set is
// local first, then reachable peers, deduped. Unreachable / mobile /
// not-installed peers are excluded.
func TestSelectableDeviceLabels_LocalFirst(t *testing.T) {
	m := newNotes(styles.Default(), DefaultKeymap())
	m.SetHosts([]hostStatus{
		{Name: "me", Local: true, OK: true},
		{Name: "mac-mini", OK: true, Address: "100.64.0.2:7474"},
		{Name: "offline", OK: false, Address: "100.64.0.3:7474"},
		{Name: "phone", OK: true, Mobile: true, Address: "100.64.0.4:7474"},
		{Name: "nox", OK: true, NeedsInstall: true, Address: "100.64.0.5:7474"},
	})
	got := m.selectableDeviceLabels()
	want := []string{"local", "mac-mini"}
	if len(got) != len(want) {
		t.Fatalf("labels = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("labels = %v, want %v", got, want)
		}
	}
}

// TestRemoteAddr_ResolvesLabel — local resolves to "" (read locally),
// a known remote resolves to its address, an unknown one fails.
func TestRemoteAddr_ResolvesLabel(t *testing.T) {
	m := twoDeviceModel()
	if addr, ok := m.remoteAddr("local"); !ok || addr != "" {
		t.Errorf("local: addr=%q ok=%v, want \"\",true", addr, ok)
	}
	if addr, ok := m.remoteAddr("mac-mini"); !ok || addr != "100.64.0.2:7474" {
		t.Errorf("mac-mini: addr=%q ok=%v", addr, ok)
	}
	if _, ok := m.remoteAddr("ghost"); ok {
		t.Error("unknown device should not resolve")
	}
}

// TestNotes_DeviceToggleCyclesAndLoads — pressing H advances to the next
// device and selects that device's first project, dispatching a remote
// fetch. Pressing H again cycles back to local.
func TestNotes_DeviceToggleCyclesAndLoads(t *testing.T) {
	m := twoDeviceModel()
	// Start on local with a project selected (as the App would do).
	if cmd := m.SetProject(&project.Project{Name: "local-proj", Host: "local", Path: "/home/me/local-proj"}); cmd != nil {
		flattenCmd(cmd) // drain
	}
	if m.deviceName != "local" {
		t.Fatalf("start device = %q, want local", m.deviceName)
	}

	// Press H → mac-mini.
	m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'H'}})
	if m2.deviceName != "mac-mini" {
		t.Fatalf("after H device = %q, want mac-mini", m2.deviceName)
	}
	if m2.project == nil || m2.project.Name != "remote-proj" {
		t.Fatalf("after H project = %+v, want remote-proj", m2.project)
	}
	// The load Cmd should be a remote fetch — drive it and confirm it
	// produces a notesEntriesLoadedMsg tagged for the remote host.
	msgs := flattenCmd(cmd)
	if le, ok := findMsg[notesEntriesLoadedMsg](msgs); !ok {
		t.Error("expected a notesEntriesLoadedMsg from the device switch")
	} else if le.Host != "mac-mini" {
		t.Errorf("loaded msg Host = %q, want mac-mini", le.Host)
	}

	// Press H again → back to local.
	m3, _ := m2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'H'}})
	if m3.deviceName != "local" {
		t.Errorf("after second H device = %q, want local", m3.deviceName)
	}
}

// TestNotes_DeviceToggleSingleDeviceNoops — with only the local device
// reachable, H is a no-op that surfaces an informational toast.
func TestNotes_DeviceToggleSingleDeviceNoops(t *testing.T) {
	m := newNotes(styles.Default(), DefaultKeymap())
	m.SetHosts([]hostStatus{{Name: "me", Local: true, OK: true}})
	m.SetProjects([]project.Project{{Name: "p", Host: "local", Path: "/tmp/p"}})
	m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'H'}})
	if m2.deviceName != "local" {
		t.Errorf("device = %q, want local (unchanged)", m2.deviceName)
	}
	if tm, ok := findMsg[toastMsg](flattenCmd(cmd)); !ok {
		t.Error("expected a toast when only one device is reachable")
	} else if tm.Kind != toastInfo {
		t.Errorf("toast kind = %v, want info", tm.Kind)
	}
}

// TestNotes_RemoteIsReadOnly — `n` (new note) and edit/enter on a remote
// device must refuse with a toast instead of opening $EDITOR on a file
// that lives on another machine.
func TestNotes_RemoteIsReadOnly(t *testing.T) {
	m := twoDeviceModel()
	m.SetProject(&project.Project{Name: "remote-proj", Host: "mac-mini", Path: "/home/me/remote-proj"})
	if !m.activeIsRemote() {
		t.Fatal("expected activeIsRemote on a mac-mini project")
	}
	// New note refused.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if tm, ok := findMsg[toastMsg](flattenCmd(cmd)); !ok {
		t.Error("n on remote should toast")
	} else if tm.Kind != toastInfo {
		t.Errorf("toast kind = %v, want info", tm.Kind)
	}
}

// TestNotes_HeaderShowsActiveDevice — the active device chip renders in
// the list header, and a switch hint appears when >1 device is reachable.
func TestNotes_HeaderShowsActiveDevice(t *testing.T) {
	m := twoDeviceModel()
	m.SetProject(&project.Project{Name: "local-proj", Host: "local", Path: "/tmp/local-proj"})
	out := m.renderList(70, 40, false)
	assertPresent(t, out, "local", "switch device")
}

// TestNotes_LoadEntriesCmd_RemoteUnreachable — when a project's host has
// no resolvable address, the load reports an error rather than silently
// reading the (nonexistent) remote path off local disk.
func TestNotes_LoadEntriesCmd_RemoteUnreachable(t *testing.T) {
	m := newNotes(styles.Default(), DefaultKeymap())
	m.SetHosts([]hostStatus{{Name: "me", Local: true, OK: true}}) // no remote
	cmd := m.loadEntriesCmd(project.Project{Name: "x", Host: "ghost", Path: "/remote/x"})
	msg, ok := cmd().(notesEntriesLoadedMsg)
	if !ok {
		t.Fatalf("expected notesEntriesLoadedMsg, got %T", cmd())
	}
	if msg.Err == "" {
		t.Error("expected an unreachable error for a project on a missing device")
	}
}
