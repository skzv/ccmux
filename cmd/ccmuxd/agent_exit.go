package main

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/proctree"
)

// Agent exit, from the pane's process tree.
//
// A session ccmux launches runs its agent under `$SHELL -c "agent ||
// agent || zsh || bash || sh"`: when the agent exits, the chain starts
// it once more and then falls back to an interactive shell, so the pane
// stays open. #{pane_current_command} can't see that happen — the
// chain's shell is the pane's foreground process while the agent runs
// (it has no job control, so the agent shares its process group) and
// after. And the screen often can't either: only Claude's rules know a
// shell prompt from the agent's own, so a Codex, Gemini or OpenCode
// that crashed read as waiting for input (and pushed "needs input"), and
// a Claude permission dialog left on screen over the shell prompt read
// as a question still waiting for an answer.
//
// The pane's process tree can. Its first process (#{pane_pid}) is the
// chain's shell; the agent, while it runs, is below it. Once only shells
// are left where the user's keystrokes go, the agent is gone.

// exitConfirmTicks is how many ticks in a row a pane's process tree must
// show its agent gone before the daemon takes it for exited. One tick
// can catch a launch between two processes: a new session's shell before
// it has started the agent, or the chain between `agent --continue`
// failing and the fresh `agent` it starts next. Two ticks is still
// sooner than the idle threshold lets a quiet pane read as waiting for
// input, so a crash never gets as far as a needs-input bell.
const exitConfirmTicks = 2

// procErrLogEvery rate-limits the log line for a failed process
// snapshot: one per this long while ps keeps failing.
const procErrLogEvery = 10 * time.Minute

// runtimes are the interpreters agents are written for: Claude Code
// from npm, Gemini CLI and Codex's npm wrapper run as `node`; others as
// `bun`, `deno` or Python.
var runtimes = map[string]bool{"node": true, "nodejs": true, "bun": true, "deno": true}

// agentProcess reports whether a process (by its normalised name, see
// proctree.Name) may be an agent: an agent's binary — cut short as the
// kernel cuts long names — Claude Code's version-named binary, or a
// runtime one could run in.
func agentProcess(name string) bool {
	if _, ok := agent.ForProcess(name); ok || runtimes[name] {
		return true
	}
	if lower := strings.ToLower(name); strings.HasPrefix(lower, "python") || strings.HasPrefix(lower, "pypy") {
		return true
	}
	for _, a := range agent.All() {
		if proctree.SameName(name, a.Binary()) {
			return true
		}
	}
	return false
}

// paneAgentGone reports whether the agent of the pane whose first
// process is pid has exited, from the process table: known is false
// when the table can't tell (pid isn't in it, or is a zombie — the pane
// is closing).
//
// Gone means nothing in the pane's process tree is, or may be, an agent
// (agentProcess), and every process in it that holds the terminal — the
// foreground process group, where the user's keystrokes go — is a shell.
// Programs in a background group don't count: a prompt's helper daemon
// (powerlevel10k's gitstatusd), a job the user put in the background.
// Anything else in the foreground does, and leaves the pane to the
// screen rules: vim or a build the user started in the fallback shell.
// An agent anywhere in the tree also does, wherever the terminal is: one
// that handed it to a subshell it started is still running.
//
// When no process in the tree holds the terminal (a platform whose ps
// doesn't mark the foreground group, or the terminal held from outside
// the tree), every process in it counts.
func paneAgentGone(tab *proctree.Table, pid int) (gone, known bool) {
	tree, ok := tab.Tree(pid)
	if !ok || tree[0].Zombie {
		return false, false
	}
	var live, fg []proctree.Proc
	for _, p := range tree {
		if p.Zombie {
			continue // exited, waiting to be reaped
		}
		if agentProcess(p.Name) {
			return false, true
		}
		live = append(live, p)
		if p.Foreground {
			fg = append(fg, p)
		}
	}
	if len(fg) == 0 {
		fg = live
	}
	for _, p := range fg {
		if !proctree.IsShell(p.Name) {
			return false, true
		}
	}
	return true, true
}

// noteExit folds one tick's reading of the pane's process tree into m:
// pid is the pane's first process, gone and known what paneAgentGone
// said about it, firstLook whether this is the daemon's first look at a
// session it joined (see below). It reports whether the agent counts as
// exited this tick, and whether it has come back: exited as of the last
// tick that could tell, and something other than a shell runs now — the
// user relaunched the agent in the fallback shell, or started another
// program there.
//
// A pane counts as exited once exitConfirmTicks ticks in a row found it
// gone, and stays exited for as long as every tick that can tell does.
// A first look at a session the daemon joined (one that was running
// before it started) believes a single reading: there is no launch of
// its own in flight to have caught mid-way, and the first look is the
// one a restarted daemon records the session's state from.
//
// A tick that can't tell (no snapshot, the pid not in it) leaves m as it
// was and doesn't count as exited: the screen rules decide, as they did
// before the process tree was read at all.
func (m *paneMemory) noteExit(pid int, gone, known, firstLook bool) (exited, restarted bool) {
	if !known {
		return false, false
	}
	if pid != m.procPID {
		// Another process heads the pane (tmux respawn-pane, or the
		// daemon reads another pane): what was counted was about the old one.
		m.procPID, m.goneTicks, m.exited = pid, 0, false
	}
	was := m.exited
	if gone {
		m.goneTicks++
	} else {
		m.goneTicks = 0
	}
	m.exited = gone && (was || firstLook || m.goneTicks >= exitConfirmTicks)
	return m.exited, was && !gone
}

// noteAgentExits reads the process tree under each agent session's pane
// — one process snapshot for the whole tick, taken only when there is a
// pane to look at — and records which agents have exited (look.exited)
// or come back (look.restarted). See noteExit.
//
// Only sessions with an agent of their own are read: tagged with one, or
// an older ccmux's `c-` session (fixedAgent). A session that follows its
// foreground — a shell, an agent run by hand in one — already sees an
// agent exit as its foreground turning back into the shell.
func (s *server) noteAgentExits(ctx context.Context, looks []look) {
	if s.procs == nil {
		return
	}
	var tab *proctree.Table
	for i := range looks {
		l := &looks[i]
		if l.sn.follow || l.id == shellAgentID || l.obs.pid <= 0 {
			continue
		}
		if tab == nil {
			var err error
			if tab, err = s.procs(ctx); err != nil {
				s.logProcErr(ctx, err)
				return
			}
		}
		gone, known := paneAgentGone(tab, l.obs.pid)
		l.exited, l.restarted = l.mem.noteExit(l.obs.pid, gone, known, l.sn.baseline)
	}
}

// logProcErr logs a failed process snapshot, at most once per
// procErrLogEvery. A platform without one (proctree.ErrUnsupported) and
// a daemon shutting down aren't worth a line.
func (s *server) logProcErr(ctx context.Context, err error) {
	if errors.Is(err, proctree.ErrUnsupported) || ctx.Err() != nil {
		return
	}
	if !s.procErrLoggedAt.IsZero() && time.Since(s.procErrLoggedAt) < procErrLogEvery {
		return
	}
	s.procErrLoggedAt = time.Now()
	log.Printf("ccmuxd: process snapshot failed (agent exits are read from the screen alone): %v", err)
}
