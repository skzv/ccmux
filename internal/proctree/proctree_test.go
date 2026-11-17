package proctree

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func fixture(t *testing.T, name string) *Table {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return Parse(b)
}

func names(ps []Proc) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Name
	}
	return out
}

// TestParse_Darwin — what Snapshot reads on macOS: ucomm, the kernel's
// name for each process, cut to 16 characters and padded with spaces
// (Claude Code's native installer by its version file's name).
func TestParse_Darwin(t *testing.T) {
	tab := fixture(t, "ps_darwin.txt")
	if tab.Len() != 17 {
		t.Errorf("Len = %d, want 17", tab.Len())
	}
	for _, tc := range []struct {
		pid        int
		name       string
		fg, zombie bool
	}{
		{4101, "zsh", false, false},
		{4102, "2.1.281", true, false},
		{4202, "gitstatusd-darwi", false, false},
		{4300, "zsh", true, false},
		{4601, "codex", true, true},
		{4800, "Google Chrome He", false, false}, // spaces inside, padding trimmed
	} {
		p, ok := tab.Lookup(tc.pid)
		if !ok {
			t.Errorf("pid %d missing", tc.pid)
			continue
		}
		if p.Name != tc.name || p.Foreground != tc.fg || p.Zombie != tc.zombie {
			t.Errorf("pid %d = %+v, want name=%q fg=%v zombie=%v", tc.pid, p, tc.name, tc.fg, tc.zombie)
		}
	}
}

// TestParse_DarwinComm — macOS's comm column (not what Snapshot asks
// for, but Parse reads it the same) is the path the program was started
// by: login shells as `-zsh` or `-/bin/zsh`, binaries by their full path,
// spaces included. All of it normalises to the base name.
func TestParse_DarwinComm(t *testing.T) {
	tab := fixture(t, "ps_darwin_comm.txt")
	if tab.Len() != 31 {
		t.Errorf("Len = %d, want 31", tab.Len())
	}
	for _, tc := range []struct {
		pid        int
		ppid       int
		name       string
		fg, zombie bool
	}{
		{4101, 612, "zsh", false, false},
		{4102, 4101, "claude", true, false},
		{4300, 612, "zsh", true, false},  // -zsh
		{4400, 612, "zsh", false, false}, // -/bin/zsh
		{4502, 4501, "codex", true, false},
		{4601, 4600, "<defunct>", true, true},
		{4701, 4700, "2.1.281", true, false},
		{4800, 1, "Electron", false, false}, // a path with spaces
		{4901, 4900, "sh", true, false},
	} {
		p, ok := tab.Lookup(tc.pid)
		if !ok {
			t.Errorf("pid %d missing", tc.pid)
			continue
		}
		if p.PPID != tc.ppid || p.Name != tc.name || p.Foreground != tc.fg || p.Zombie != tc.zombie {
			t.Errorf("pid %d = %+v, want ppid=%d name=%q fg=%v zombie=%v", tc.pid, p, tc.ppid, tc.name, tc.fg, tc.zombie)
		}
	}
}

// TestParse_Linux — procps reports the kernel's 15-character name:
// process titles the program set itself (`tmux: server`), names cut
// short, zombies flagged in STAT. Lines that don't parse are skipped.
func TestParse_Linux(t *testing.T) {
	tab := fixture(t, "ps_linux.txt")
	if tab.Len() != 19 {
		t.Errorf("Len = %d, want 19 (garbage lines skipped)", tab.Len())
	}
	for _, tc := range []struct {
		pid        int
		name       string
		fg, zombie bool
	}{
		{900, "tmux: server", false, false},
		{1002, "node", true, false}, // Sl+
		{2003, "gitstatusd-linu", false, false},
		{2101, "codex <defunct>", false, true},
		{2200, "bash", true, false}, // -bash
		{2401, "python3.12", true, false},
	} {
		p, ok := tab.Lookup(tc.pid)
		if !ok {
			t.Errorf("pid %d missing", tc.pid)
			continue
		}
		if p.Name != tc.name || p.Foreground != tc.fg || p.Zombie != tc.zombie {
			t.Errorf("pid %d = %+v, want name=%q fg=%v zombie=%v", tc.pid, p, tc.name, tc.fg, tc.zombie)
		}
	}
	if _, ok := tab.Lookup(2600); ok {
		t.Error("a line with a pid and nothing else parsed")
	}
}

func TestTreeAndDescendants(t *testing.T) {
	tab := fixture(t, "ps_darwin_comm.txt")
	tree, ok := tab.Tree(4500)
	if !ok {
		t.Fatal("Tree(4500): not found")
	}
	if got, want := names(tree), []string{"zsh", "node", "codex", "node_repl"}; !slices.Equal(got, want) {
		t.Errorf("Tree(4500) = %v, want %v", got, want)
	}
	if got := tab.Descendants(4300); len(got) != 0 {
		t.Errorf("Descendants of a leaf = %v, want none", names(got))
	}
	if got := len(tab.Descendants(612)); got != 28 {
		t.Errorf("Descendants(tmux) = %d processes, want 28", got)
	}
	if _, ok := tab.Tree(99999); ok {
		t.Error("Tree of a pid not in the snapshot reported ok")
	}
	if got := tab.Descendants(99999); len(got) != 0 {
		t.Errorf("Descendants of a missing pid = %v", names(got))
	}
}

// TestDescendants_Cycle — a pid reused while ps ran can make two
// entries each other's parent; walking the tree must still end.
func TestDescendants_Cycle(t *testing.T) {
	tab := Parse([]byte("10 11 S a\n11 10 S b\n12 12 S self\n"))
	if got := names(tab.Descendants(10)); !slices.Equal(got, []string{"b"}) {
		t.Errorf("Descendants(10) = %v, want [b]", got)
	}
	if got := tab.Descendants(12); len(got) != 0 {
		t.Errorf("a process that is its own parent has descendants %v", names(got))
	}
}

func TestName(t *testing.T) {
	for in, want := range map[string]string{
		"zsh":        "zsh",
		"-zsh":       "zsh",
		"-/bin/zsh":  "zsh",
		"/bin/zsh":   "zsh",
		"  bash  ":   "bash",
		"/usr/bin/-": "",
		"-":          "",
		"":           "",
		"/Applications/Visual Studio Code.app/Contents/MacOS/Electron": "Electron",
		"/opt/homebrew/Cellar/fish/4.0/bin/fish":                       "fish",
	} {
		if got := Name(in); got != want {
			t.Errorf("Name(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSameName(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		same       bool
	}{
		{"cursor-agent", "cursor-agent", true},
		{"gitstatusd-linu", "gitstatusd-linux-x86_64", true},  // Linux: 15
		{"gitstatusd-linux", "gitstatusd-linux-x86_64", true}, // macOS: 16
		{"gitstatusd", "gitstatusd-linux-x86_64", false},
		{"claude", "claude-code", false},
		{"gitstatusd-linu", "gitstatusd-linu", true},
		{"gitstatusd-linx", "gitstatusd-linux-x86_64", false},
	} {
		if got := SameName(tc.name, tc.want); got != tc.same {
			t.Errorf("SameName(%q, %q) = %v, want %v", tc.name, tc.want, got, tc.same)
		}
	}
}

func TestIsShell(t *testing.T) {
	for _, name := range []string{"zsh", "bash", "sh", "dash", "fish", "ksh", "tcsh", "csh", "nu", Name("-zsh"), Name("-/bin/bash")} {
		if !IsShell(name) {
			t.Errorf("IsShell(%q) = false", name)
		}
	}
	for _, name := range []string{"node", "vim", "claude", "zshx", "bash5", "", "-zsh", "tmux: server"} {
		if IsShell(name) {
			t.Errorf("IsShell(%q) = true", name)
		}
	}
}

// FuzzParse — whatever ps prints, parsing never panics, every process
// it keeps has a pid and a name, and walking any tree ends.
func FuzzParse(f *testing.F) {
	for _, name := range []string{"ps_darwin.txt", "ps_darwin_comm.txt", "ps_linux.txt"} {
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Add([]byte("10 11 S a\n11 10 S b\n"))
	f.Fuzz(func(t *testing.T, out []byte) {
		tab := Parse(out)
		for pid, p := range tab.procs {
			if p.PID != pid || pid <= 0 || p.Name == "" {
				t.Fatalf("kept %+v under pid %d", p, pid)
			}
			tree, ok := tab.Tree(pid)
			if !ok || tree[0].PID != pid || len(tree) > tab.Len() {
				t.Fatalf("Tree(%d) = %d procs, ok %v (table has %d)", pid, len(tree), ok, tab.Len())
			}
		}
	})
}
