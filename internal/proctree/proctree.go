// Package proctree reads the machine's process table — one `ps`
// snapshot — and answers questions about a process's subtree: what runs
// under a tmux pane's first process, and which of it holds the
// terminal.
//
// The daemon uses it to tell an agent that exited from one still
// running. A session ccmux launches runs its agent under
// `$SHELL -c "agent || fallback-shell"`, so tmux's
// #{pane_current_command} names that shell both while the agent runs and
// after it has died and the fallback shell took over the pane. The
// process tree under the pane's pid tells the two apart.
package proctree

import (
	"errors"
	"strconv"
	"strings"
)

// ErrUnsupported is Snapshot's error on a platform without a `ps` it
// knows how to read (Windows). Callers treat it as "unknown", not as a
// failure worth reporting.
var ErrUnsupported = errors.New("proctree: process snapshots are not supported on this platform")

// Proc is one process in a Table.
type Proc struct {
	PID, PPID int
	// Name is the process's normalised command name (see Name).
	Name string
	// Foreground: the process is in the foreground process group of its
	// controlling terminal (a `+` in ps's STAT) — the group that gets the
	// terminal's input. An interactive program reading the pane is in it;
	// a background job (`cmd &`, a prompt's helper daemon) is not.
	Foreground bool
	// Zombie: the process has exited and waits to be reaped (STAT `Z`).
	Zombie bool
}

// Table is one snapshot of the process table, by pid.
type Table struct {
	procs    map[int]Proc
	children map[int][]int
}

// psArgs is the ps invocation Snapshot parses: every process (-A), with
// no header (the `=` suffixes), the columns Parse expects. The name is
// last because it may contain spaces.
//
// ucomm is the kernel's name for the process — the executable's base
// name, cut to 16 characters on macOS and 15 on Linux (where it is the
// same as comm). macOS's comm is argv[0] instead, which ps has to read
// out of every process: ucomm costs about half as much there, and the
// daemon takes a snapshot every poll tick.
var psArgs = []string{"-Ao", "pid=,ppid=,stat=,ucomm="}

// Parse reads `ps -Ao pid=,ppid=,stat=,ucomm=` output (or comm in the
// last column). Lines that don't parse are skipped.
//
// The name column differs by platform and column: the kernel's name,
// cut short (ucomm, and Linux's comm); or on macOS's comm the path the
// program was started by (`/bin/zsh`, `-zsh` or `-/bin/zsh` for a login
// shell). Name normalises all of them.
func Parse(out []byte) *Table {
	t := &Table{procs: map[int]Proc{}, children: map[int][]int{}}
	for line := range strings.SplitSeq(string(out), "\n") {
		p, ok := parseLine(line)
		if !ok {
			continue
		}
		if _, dup := t.procs[p.PID]; dup {
			continue
		}
		t.procs[p.PID] = p
		if p.PPID != p.PID {
			t.children[p.PPID] = append(t.children[p.PPID], p.PID)
		}
	}
	return t
}

// parseLine parses one line of ps output: pid, ppid and stat, then the
// rest of the line as comm.
func parseLine(line string) (Proc, bool) {
	var fields [3]string
	rest := line
	for i := range fields {
		rest = strings.TrimLeft(rest, " \t")
		end := strings.IndexAny(rest, " \t")
		if end < 0 {
			return Proc{}, false
		}
		fields[i], rest = rest[:end], rest[end:]
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil || pid <= 0 {
		return Proc{}, false
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil || ppid < 0 {
		return Proc{}, false
	}
	name := Name(rest)
	if name == "" {
		return Proc{}, false
	}
	stat := fields[2]
	return Proc{
		PID:        pid,
		PPID:       ppid,
		Name:       name,
		Foreground: strings.Contains(stat, "+"),
		Zombie:     strings.HasPrefix(stat, "Z"),
	}, true
}

// Len is the number of processes in the snapshot.
func (t *Table) Len() int { return len(t.procs) }

// Lookup returns the process with the given pid.
func (t *Table) Lookup(pid int) (Proc, bool) {
	p, ok := t.procs[pid]
	return p, ok
}

// Descendants returns every process below pid — its children, theirs,
// and so on — in breadth-first order; empty when pid has none or isn't
// in the snapshot.
func (t *Table) Descendants(pid int) []Proc {
	var out []Proc
	seen := map[int]bool{pid: true}
	queue := []int{pid}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		for _, c := range t.children[next] {
			if seen[c] {
				continue // a pid reused mid-snapshot can't loop us
			}
			seen[c] = true
			out = append(out, t.procs[c])
			queue = append(queue, c)
		}
	}
	return out
}

// Tree returns pid's process followed by its descendants; ok is false
// when pid isn't in the snapshot.
func (t *Table) Tree(pid int) (procs []Proc, ok bool) {
	root, ok := t.procs[pid]
	if !ok {
		return nil, false
	}
	return append([]Proc{root}, t.Descendants(pid)...), true
}

// Name normalises a process name as ps reports it: surrounding space
// trimmed, a login shell's leading `-` dropped, and a path cut to its
// base name — so `-/bin/zsh`, `-zsh` and `/bin/zsh` all read `zsh`.
func Name(comm string) string {
	name := strings.TrimSpace(comm)
	name = strings.TrimPrefix(name, "-")
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	return strings.TrimPrefix(name, "-")
}

// commLimits are the lengths the kernel cuts a process name to: Linux
// keeps 15 characters (TASK_COMM_LEN 16, NUL included), macOS 16
// (MAXCOMLEN).
var commLimits = [...]int{15, 16}

// SameName reports whether name (a normalised process name, see Name)
// is want: equal, or want cut where the kernel cuts long process names.
func SameName(name, want string) bool {
	if name == want {
		return true
	}
	for _, n := range commLimits {
		if len(name) == n && len(want) > n && want[:n] == name {
			return true
		}
	}
	return false
}

// shells are the interactive shells a launch chain falls back to, or a
// user may run, by process name.
var shells = map[string]bool{
	"sh": true, "ash": true, "dash": true, "bash": true, "zsh": true,
	"ksh": true, "mksh": true, "oksh": true, "pdksh": true, "yash": true,
	"csh": true, "tcsh": true, "fish": true, "nu": true, "elvish": true,
}

// IsShell reports whether name (normalised, see Name) is a shell.
func IsShell(name string) bool { return shells[name] }
