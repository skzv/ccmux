package sshsetup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRemoveKnownHostEntries_DropsPort22Plain — the most common
// case: a default-port entry stored as the bare host. Removal
// drops the line and reports count 1.
func TestRemoveKnownHostEntries_DropsPort22Plain(t *testing.T) {
	home := withTempHome(t)
	seedKnownHosts(t, home, ""+
		"sputnik ssh-ed25519 AAAA-fingerprint-1\n"+
		"otherhost ssh-rsa BBBB-fingerprint-2\n")

	removed, err := RemoveKnownHostEntries("sputnik", 22)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	got := readKnownHosts(t, home)
	if strings.Contains(got, "sputnik ") {
		t.Errorf("sputnik entry still present:\n%s", got)
	}
	if !strings.Contains(got, "otherhost") {
		t.Errorf("unrelated entry incorrectly removed:\n%s", got)
	}
}

// TestRemoveKnownHostEntries_DropsBracketPortEntry — non-22 port
// entries are stored in [host]:port form. The remover must also
// match that shape so a wizard run on a non-default port can
// recover.
func TestRemoveKnownHostEntries_DropsBracketPortEntry(t *testing.T) {
	home := withTempHome(t)
	seedKnownHosts(t, home, ""+
		"[sputnik]:2222 ssh-ed25519 AAAA\n"+
		"sputnik ssh-ed25519 CCCC\n")

	removed, err := RemoveKnownHostEntries("sputnik", 2222)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1 (the [sputnik]:2222 entry)", removed)
	}
	got := readKnownHosts(t, home)
	if strings.Contains(got, "[sputnik]:2222") {
		t.Errorf("[sputnik]:2222 entry still present:\n%s", got)
	}
	if !strings.Contains(got, "sputnik ssh-ed25519 CCCC") {
		t.Errorf("port-22 entry incorrectly removed:\n%s", got)
	}
}

// TestRemoveKnownHostEntries_HandlesCommaList — known_hosts allows
// a comma-separated list of patterns per line; if our host is one
// of them, the whole line should drop.
func TestRemoveKnownHostEntries_HandlesCommaList(t *testing.T) {
	home := withTempHome(t)
	seedKnownHosts(t, home, "sputnik,100.64.0.1,sputnik.tail-abcd.ts.net ssh-ed25519 AAAA\n")

	removed, err := RemoveKnownHostEntries("sputnik", 22)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
}

// TestRemoveKnownHostEntries_PreservesComments — comments and
// blank lines must survive verbatim.
func TestRemoveKnownHostEntries_PreservesComments(t *testing.T) {
	home := withTempHome(t)
	in := "# managed by ccmux\n" +
		"\n" +
		"sputnik ssh-ed25519 AAAA\n" +
		"# trailing comment\n"
	seedKnownHosts(t, home, in)

	_, err := RemoveKnownHostEntries("sputnik", 22)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	got := readKnownHosts(t, home)
	if !strings.Contains(got, "# managed by ccmux") {
		t.Errorf("leading comment lost:\n%s", got)
	}
	if !strings.Contains(got, "# trailing comment") {
		t.Errorf("trailing comment lost:\n%s", got)
	}
}

// TestRemoveKnownHostEntries_DoesNothingWhenAbsent — calling
// remove for a host that isn't in the file is a no-op, not an
// error. Important so the wizard can call this defensively
// (e.g. before a fresh install) without caring whether the file
// already has an entry.
func TestRemoveKnownHostEntries_DoesNothingWhenAbsent(t *testing.T) {
	home := withTempHome(t)
	seedKnownHosts(t, home, "otherhost ssh-ed25519 AAAA\n")
	removed, err := RemoveKnownHostEntries("sputnik", 22)
	if err != nil {
		t.Fatalf("Remove on absent: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
}

// TestRemoveKnownHostEntries_MissingFileIsOK — no known_hosts at
// all (fresh HOME) is also a no-op.
func TestRemoveKnownHostEntries_MissingFileIsOK(t *testing.T) {
	withTempHome(t)
	removed, err := RemoveKnownHostEntries("sputnik", 22)
	if err != nil {
		t.Fatalf("Remove on missing file: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
}

// TestRemoveKnownHostEntries_LeavesHashedEntriesAlone — `|1|...`
// hashed entries can't be matched without the salt + plaintext,
// so we explicitly skip them. Documents that ccmux's own writes
// are never hashed, so this only kicks in for entries openssh
// wrote with HashKnownHosts enabled.
func TestRemoveKnownHostEntries_LeavesHashedEntriesAlone(t *testing.T) {
	home := withTempHome(t)
	seedKnownHosts(t, home, ""+
		"|1|abc=|hash= ssh-ed25519 AAAA\n"+
		"sputnik ssh-ed25519 BBBB\n")
	removed, err := RemoveKnownHostEntries("sputnik", 22)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1 (only the plain entry)", removed)
	}
	got := readKnownHosts(t, home)
	if !strings.Contains(got, "|1|abc=|hash=") {
		t.Errorf("hashed entry was wrongly removed:\n%s", got)
	}
}

// TestRemoveKnownHostEntries_EmptyHostErrors — defensive: a blank
// host argument should fail loud, never silently nuke entries.
func TestRemoveKnownHostEntries_EmptyHostErrors(t *testing.T) {
	withTempHome(t)
	if _, err := RemoveKnownHostEntries("  ", 22); err == nil {
		t.Fatal("blank host should error")
	}
}

func seedKnownHosts(t *testing.T, home, content string) {
	t.Helper()
	dir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "known_hosts"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readKnownHosts(t *testing.T, home string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".ssh", "known_hosts"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
