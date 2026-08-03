package sshsetup

import (
	"os"
	"path/filepath"
	"runtime"
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

// TestRemoveKnownHostEntries_PreservesFileMode — regression: the
// rewrite used a hard-coded 0644, silently widening a user's 0600
// known_hosts. The original mode must survive the temp-file+rename.
func TestRemoveKnownHostEntries_PreservesFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix file modes")
	}
	home := withTempHome(t)
	seedKnownHosts(t, home, ""+
		"sputnik ssh-ed25519 AAAA\n"+
		"otherhost ssh-ed25519 BBBB\n")
	path := filepath.Join(home, ".ssh", "known_hosts")
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}

	removed, err := RemoveKnownHostEntries("sputnik", 22)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("known_hosts mode after rewrite = %o, want 600", got)
	}
}

// TestAppendKnownHost_SuffixHostnameStillAppended — regression for the
// substring dedup: with bytes.Contains, appending an entry for "mini"
// when "foo.mini <same key>" was already recorded matched as a
// substring and was skipped — so "mini" never got pinned and TOFU
// protection never engaged for that name. Dedup must compare whole
// lines; a true duplicate is still skipped.
func TestAppendKnownHost_SuffixHostnameStillAppended(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "known_hosts")
	key := genHostKey(t)

	if err := appendKnownHost(path, "foo.mini", key); err != nil {
		t.Fatal(err)
	}
	if err := appendKnownHost(path, "mini", key); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := nonEmptyLines(string(data))
	if len(lines) != 2 {
		t.Fatalf("known_hosts lines = %q, want 2 (suffix hostname must be appended)", lines)
	}
	if !strings.HasPrefix(lines[1], "mini ") {
		t.Errorf("second line = %q, want an entry for %q", lines[1], "mini")
	}

	// A true duplicate is still deduped.
	if err := appendKnownHost(path, "mini", key); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if lines := nonEmptyLines(string(data)); len(lines) != 2 {
		t.Errorf("after duplicate append: lines = %q, want still 2", lines)
	}
}

// nonEmptyLines splits on newlines and drops blanks.
func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
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
