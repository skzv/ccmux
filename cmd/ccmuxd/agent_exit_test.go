package main

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/skzv/ccmux/internal/agent"
	"github.com/skzv/ccmux/internal/proctree"
	"github.com/skzv/ccmux/internal/tmux"
)

// fakeProcs is a scripted process table for the poll loop's procs seam:
// `pid ppid stat name` lines, as ps prints them, that a test changes
// between ticks the way processes come and go.
type fakeProcs struct {
	mu    sync.Mutex
	lines string
	err   error
	calls int
}

func (fp *fakeProcs) set(lines ...string) {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	fp.lines, fp.err = strings.Join(lines, "\n"), nil
}

func (fp *fakeProcs) fail(err error) {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	fp.err = err
}

func (fp *fakeProcs) snapshots() int {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	return fp.calls
}

func (fp *fakeProcs) wire(s *server) {
	s.procs = func(context.Context) (*proctree.Table, error) {
		fp.mu.Lock()
		defer fp.mu.Unlock()
		fp.calls++
		if fp.err != nil {
			return nil, fp.err
		}
		return proctree.Parse([]byte(fp.lines)), nil
	}
}

// Process trees under a pane whose first process is pid 100: the launch
// chain's `zsh -c "agent || … || zsh"`.
var (
	// running: the agent runs under the chain's shell, in its process
	// group (no job control), holding the terminal.
	running = func(agentProc string) []string {
		return []string{"100 1 Ss+ zsh", "101 100 S+ " + agentProc}
	}
	// fellBack: the agent exited (twice: the chain's retry too) and the
	// chain started an interactive zsh, which took the terminal.
	fellBack = []string{"100 1 Ss zsh", "102 100 S+ zsh"}
)

// TestPaneAgentGone pins what in a pane's process tree means its agent
// has exited.
func TestPaneAgentGone(t *testing.T) {
	for _, tc := range []struct {
		name        string
		lines       []string
		gone, known bool
	}{
		{"agent running", running("codex"), false, true},
		{"claude's native binary", running("2.1.281"), false, true},
		{"node agent", running("node"), false, true},
		{"python agent", running("python3.12"), false, true},
		{"agent name cut short", running("cursor-agent"), false, true},
		{"fell back to a shell", fellBack, true, true},
		{"login shells", []string{"100 1 Ss -zsh", "102 100 S+ -/bin/bash"}, true, true},
		{"fell back, prompt helper in the background", append(fellBack, "103 102 S gitstatusd-darwi"), true, true},
		{"fell back, user ran vim", []string{"100 1 Ss zsh", "102 100 S zsh", "104 102 S+ vim"}, false, true},
		{"fell back, user backgrounded a dev server", append(fellBack, "105 102 S node"), false, true},
		{"agent handed the terminal to a subshell", []string{"100 1 Ss zsh", "101 100 S codex", "106 101 S+ bash"}, false, true},
		{"the agent is a zombie", []string{"100 1 Ss zsh", "101 100 Z+ codex", "102 100 S+ zsh"}, true, true},
		{"no foreground marks: every process counts", []string{"100 1 Ss zsh", "102 100 S zsh"}, true, true},
		{"no foreground marks, something else runs", []string{"100 1 Ss zsh", "107 100 S make"}, false, true},
		{"pane pid not in the snapshot", []string{"200 1 Ss zsh"}, false, false},
		{"pane closing", []string{"100 1 Z zsh"}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tab := proctree.Parse([]byte(strings.Join(tc.lines, "\n")))
			gone, known := paneAgentGone(tab, 100)
			if gone != tc.gone || known != tc.known {
				t.Errorf("paneAgentGone = %v, %v; want %v, %v", gone, known, tc.gone, tc.known)
			}
		})
	}
}

// TestPaneMemory_NoteExit pins the confirmation: a pane is exited after
// two gone ticks in a row (one may catch a launch between processes),
// at once on a joined session's first look, stays exited while every
// tick that can tell says gone, and reports the agent coming back.
func TestPaneMemory_NoteExit(t *testing.T) {
	type tick struct {
		pid                int
		gone, known, first bool
		exited, restarted  bool
	}
	for _, tc := range []struct {
		name  string
		ticks []tick
	}{
		{"confirmed on the second tick", []tick{
			{100, true, true, false, false, false},
			{100, true, true, false, true, false},
			{100, true, true, false, true, false},
		}},
		{"one gone tick is not an exit", []tick{
			{100, true, true, false, false, false},
			{100, false, true, false, false, false},
			{100, true, true, false, false, false},
		}},
		{"a joined session's first look believes one", []tick{
			{100, true, true, true, true, false},
		}},
		{"relaunched", []tick{
			{100, true, true, true, true, false},
			{100, false, true, false, false, true},
			{100, false, true, false, false, false},
		}},
		{"a tick that can't tell changes nothing", []tick{
			{100, true, true, true, true, false},
			{100, false, false, false, false, false},
			{100, true, true, false, true, false},
			{100, false, false, false, false, false},
			{100, false, true, false, false, true},
		}},
		{"a new pane process starts over", []tick{
			{100, true, true, true, true, false},
			{200, true, true, false, false, false},
			{200, true, true, false, true, false},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var m paneMemory
			for i, tk := range tc.ticks {
				exited, restarted := m.noteExit(tk.pid, tk.gone, tk.known, tk.first)
				if exited != tk.exited || restarted != tk.restarted {
					t.Fatalf("tick %d: exited=%v restarted=%v, want %v/%v", i, exited, restarted, tk.exited, tk.restarted)
				}
			}
		})
	}
}

// exitSession is a session of agent tag sitting at body, its pane headed
// by pid 100 with the agent running in it, on a server watching it
// through a scripted tmux and process table; settled once the daemon
// has joined it.
func exitSession(t *testing.T, s *server, tag, body, title, agentProc string) (*fakeTmux, *fakePane, *fakeProcs) {
	t.Helper()
	p := &fakePane{Pane: tmux.Pane{ID: "%1", PID: 100, Width: 120, Height: 40, Title: title}, body: body}
	f := newFakeTmux()
	f.addSession(tmux.Session{Name: "c-exit", Path: "/tmp", Agent: tag, Created: time.Now().Add(-time.Hour)}, p)
	f.wire(s)
	fp := &fakeProcs{}
	fp.set(running(agentProc)...)
	fp.wire(s)
	pollNTimes(s, 3)
	return f, p, fp
}

// crashTail is what the launch chain prints under an agent that died:
// the shell's report of the crash, then the fallback shell's prompt.
const crashTail = "\nzsh: segmentation fault  agent\nuser@host ~ % "

// TestPollOnce_AgentExitedToShellIsAnError — an agent with no rule for
// a shell prompt (Codex, Gemini CLI, …) crashed, and its launch chain
// fell back to a shell. The pane went quiet and read as waiting for
// input: a needs-input bell and push for a crash. Its process tree says
// the agent is gone: error, which neither rings nor pushes (as Claude's
// crashes always were), and marks the session unreviewed.
func TestPollOnce_AgentExitedToShellIsAnError(t *testing.T) {
	for _, tc := range []struct {
		tag, proc, body, title string
	}{
		{"codex", "codex", codexPane("> fix it\n\n• Done."), "codex"},
		{"gemini", "node", "Gemini CLI\n\n> fix it\n\n✦ Done.\n\n╭────────╮\n│ >      │\n╰────────╯\n~/demo   gemini-2.5-pro", "◇  Ready (demo)"},
		{"opencode", "opencode", "opencode\n\n> fix it\n\nDone.\n\n┃ >\n  tab agents  ctrl+p commands", "OpenCode"},
		{"claude", "2.1.281", "", "✳ Claude Code"},
	} {
		t.Run(tc.tag, func(t *testing.T) {
			s := newPollTestServer(t)
			pushes := countPushes(t, s)
			bells := countBells(s)
			body := tc.body
			if body == "" {
				body = readFixture(t, "claude_v2_idle.txt")
			}
			f, p, fp := exitSession(t, s, tc.tag, body, tc.title, tc.proc)
			tr := s.seen["c-exit"]
			before := tr.state

			f.update(func() { p.body = body + crashTail })
			fp.set(fellBack...)
			pollNTimes(s, 6)

			if tr.state != agent.StateError {
				t.Errorf("state = %s (was %s), want error: the agent's process is gone", tr.state, before)
			}
			if got := pushes(); got != 0 || *bells != 0 || tr.promptCount != 0 {
				t.Errorf("the crash notified: pushes=%d bells=%d promptCount=%d, want 0", got, *bells, tr.promptCount)
			}
			if tr.seen {
				t.Error("a crash nobody watched left the session reviewed")
			}
			if w := f.reviewWrites("c-exit"); len(w) == 0 || w[len(w)-1].State != string(agent.StateError) || w[len(w)-1].Seen {
				t.Errorf("review record = %+v, want the last one unreviewed, in error", w)
			}
		})
	}
}

// TestPollOnce_FrozenPermissionDialogAboveShellIsAnError — Claude asked
// for permission and was killed with the dialog on screen; its launch
// chain printed the shell prompt under it. The dialog rule outranks the
// shell-prompt rule, so the session kept reading as a question waiting
// for an answer — one no process is left to receive.
func TestPollOnce_FrozenPermissionDialogAboveShellIsAnError(t *testing.T) {
	s := newPollTestServer(t)
	pushes := countPushes(t, s)
	bells := countBells(s)
	dialog := readFixture(t, "claude_v2_permission.txt")
	f, p, fp := exitSession(t, s, "claude", dialog, "✳ Claude Code", "2.1.281")
	tr := s.seen["c-exit"]
	if tr.state != agent.StateNeedsInput {
		t.Fatalf("setup: the live dialog reads %s, want needs_input", tr.state)
	}
	b, n := *bells, pushes()

	f.update(func() { p.body = dialog + "\nuser@host ~ % " })
	fp.set(fellBack...)
	pollNTimes(s, 6)
	if tr.state != agent.StateError {
		t.Errorf("state = %s, want error: the dialog's agent is gone", tr.state)
	}
	if *bells != b || pushes() != n {
		t.Errorf("the crash notified: bells %d→%d pushes %d→%d", b, *bells, n, pushes())
	}
}

// pollStates runs the same scripted session on two servers — one
// reading the process table, one without it — tick by tick, and fails
// on the first tick whose states differ.
func pollStates(t *testing.T, tag, title string, bodies []string, procs []string) {
	t.Helper()
	with, without := newPollTestServer(t), newPollTestServer(t)
	fw, fo := newFakeTmux(), newFakeTmux()
	pw := &fakePane{Pane: tmux.Pane{ID: "%1", PID: 100, Width: 120, Height: 40, Title: title}}
	po := &fakePane{Pane: pw.Pane}
	ts := tmux.Session{Name: "c-same", Path: "/tmp", Agent: tag, Created: time.Now().Add(-time.Hour)}
	fw.addSession(ts, pw)
	fo.addSession(ts, po)
	fw.wire(with)
	fo.wire(without)
	fp := &fakeProcs{}
	fp.set(procs...)
	fp.wire(with)
	for i, body := range bodies {
		fw.update(func() { pw.body = body })
		fo.update(func() { po.body = body })
		for range 3 {
			with.pollOnce(context.Background(), 50*time.Millisecond)
			without.pollOnce(context.Background(), 50*time.Millisecond)
			time.Sleep(60 * time.Millisecond)
			if a, b := with.seen["c-same"].state, without.seen["c-same"].state; a != b {
				t.Fatalf("body %d: state %s with the process table, %s without", i, a, b)
			}
		}
	}
}

// TestPollOnce_LiveAgentClassifiesAsBefore — while the agent's process
// runs, the process table changes nothing: every pane, a shell prompt
// the agent printed included, classifies as the screen rules say.
func TestPollOnce_LiveAgentClassifiesAsBefore(t *testing.T) {
	idle, working, dialog := readFixture(t, "claude_v2_idle.txt"), readFixture(t, "claude_v2_working.txt"), readFixture(t, "claude_v2_permission.txt")
	pollStates(t, "claude", "✳ Claude Code", []string{idle, working, dialog, idle + crashTail}, running("2.1.281"))
	pollStates(t, "codex", "codex", []string{codexPane(""), codexPane("> fix it\n\n• Working"), codexPane("> fix it") + crashTail}, running("codex"))
	pollStates(t, "gemini", "gemini", []string{"Gemini CLI\n\n> ", "Gemini CLI\n\n> hi" + crashTail}, running("node"))
}

// TestPollOnce_ProgramAfterExitClassifiesAsBefore — once something else
// runs in the dead agent's pane (vim, started in the fallback shell),
// the screen rules decide again; and a prompt helper running in the
// background doesn't stop a crash from reading as one.
func TestPollOnce_ProgramAfterExitClassifiesAsBefore(t *testing.T) {
	vim := []string{"100 1 Ss zsh", "102 100 S zsh", "104 102 S+ vim"}
	pollStates(t, "codex", "codex", []string{codexPane("> fix it") + crashTail, "~\n~\n~\n\"notes.md\" 0L, 0B"}, vim)

	s := newPollTestServer(t)
	f, p, fp := exitSession(t, s, "codex", codexPane("> fix it\n\n• Done."), "codex", "codex")
	f.update(func() { p.body += crashTail })
	fp.set(append(fellBack, "103 102 S gitstatusd-darwi")...)
	pollNTimes(s, 3)
	if st := s.seen["c-exit"].state; st != agent.StateError {
		t.Fatalf("crash with gitstatusd in the background: state = %s, want error", st)
	}
	f.update(func() { p.body = "~\n~\n~\n\"notes.md\" 0L, 0B" })
	fp.set(vim...)
	pollNTimes(s, 3)
	if st := s.seen["c-exit"].state; st == agent.StateError {
		t.Errorf("vim in the dead agent's pane: state = %s, want the screen rules' verdict", st)
	}
}

// TestPollOnce_ProcessSnapshotUnavailableChangesNothing — when ps fails,
// or the pane's process isn't in its snapshot, the screen rules decide
// as they always did; the failure is logged once, not every tick.
func TestPollOnce_ProcessSnapshotUnavailableChangesNothing(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })

	bodies := []string{codexPane("> fix it\n\n• Done."), codexPane("> fix it\n\n• Done.") + crashTail}
	pollStates(t, "codex", "codex", bodies, []string{"200 1 Ss zsh", "201 200 S+ zsh"}) // not the pane's

	s := newPollTestServer(t)
	f, p, fp := exitSession(t, s, "codex", bodies[0], "codex", "codex")
	fp.fail(errors.New("ps: exec: no such file"))
	f.update(func() { p.body = bodies[1] })
	pollNTimes(s, 6)
	if st := s.seen["c-exit"].state; st != agent.StateNeedsInput {
		t.Errorf("state = %s, want needs_input (the screen rules alone)", st)
	}
	if n := strings.Count(buf.String(), "process snapshot failed"); n != 1 {
		t.Errorf("logged the ps failure %d times over 6 ticks, want once:\n%s", n, buf.String())
	}
}

// TestPollOnce_OneSnapshotPerTick — however many sessions, the process
// table is read once per tick, and not at all when no session has an
// agent of its own.
func TestPollOnce_OneSnapshotPerTick(t *testing.T) {
	s := newPollTestServer(t)
	f := newFakeTmux()
	for i, name := range []string{"c-a", "c-b", "c-c"} {
		f.addSession(tmux.Session{Name: name, Path: "/tmp", Agent: "codex", Created: time.Now().Add(-time.Hour)},
			&fakePane{Pane: tmux.Pane{ID: "%" + string(rune('1'+i)), PID: 100 + i, Width: 80, Height: 24}, body: codexPane("")})
	}
	f.wire(s)
	fp := &fakeProcs{}
	fp.set(running("codex")...)
	fp.wire(s)
	pollNTimes(s, 4)
	if n := fp.snapshots(); n != 4 {
		t.Errorf("%d snapshots over 4 ticks of 3 sessions, want 4", n)
	}

	shells := newPollTestServer(t)
	g := newFakeTmux()
	g.addSession(tmux.Session{Name: "plain", Path: "/tmp", Created: time.Now().Add(-time.Hour)},
		&fakePane{Pane: tmux.Pane{ID: "%9", PID: 300, Width: 80, Height: 24}, body: "user@host ~ % "})
	g.wire(shells)
	gp := &fakeProcs{}
	gp.set("300 1 Ss+ zsh")
	gp.wire(shells)
	pollNTimes(shells, 2)
	if n := gp.snapshots(); n != 0 {
		t.Errorf("%d snapshots for a session without an agent, want 0", n)
	}
}

// TestPollOnce_ShellSessionsUnaffected — a session with no agent of its
// own (a ccmux shell, a tmux session made by hand) is a shell at its
// prompt, never a crashed agent; and an agent run by hand in one is
// still seen, and seen to exit, through its foreground.
func TestPollOnce_ShellSessionsUnaffected(t *testing.T) {
	s := newPollTestServer(t)
	p := &fakePane{Pane: tmux.Pane{ID: "%1", PID: 100, Width: 120, Height: 40, Command: "zsh"}, body: "user@host ~ % "}
	f := newFakeTmux()
	f.addSession(tmux.Session{Name: "c-shell-1", Path: "/tmp", Agent: tmux.ShellAgentTag, Created: time.Now().Add(-time.Hour)}, p)
	f.wire(s)
	fp := &fakeProcs{}
	fp.set("100 1 Ss+ -zsh")
	fp.wire(s)
	pollNTimes(s, 3)
	tr := s.seen["c-shell-1"]
	if tr.state != agent.StateIdle || tr.agentID != shellAgentID {
		t.Fatalf("a shell at its prompt: %s/%s, want idle shell", tr.agentID, tr.state)
	}

	idle := readFixture(t, "claude_v2_idle.txt")
	f.update(func() { p.Command, p.body = "claude", idle })
	fp.set("100 1 Ss -zsh", "101 100 S+ claude")
	pollNTimes(s, 3)
	if tr.agentID != agent.IDClaude || tr.state != agent.StateNeedsInput {
		t.Fatalf("claude run by hand: %s/%s, want claude/needs_input", tr.agentID, tr.state)
	}
	f.update(func() { p.Command, p.body = "zsh", idle+"\nuser@host ~ % " })
	fp.set("100 1 Ss+ -zsh")
	pollNTimes(s, 3)
	if tr.agentID != shellAgentID || tr.state != agent.StateIdle {
		t.Errorf("after claude exited by hand: %s/%s, want idle shell", tr.agentID, tr.state)
	}
}

// TestPollOnce_NewSessionLaunchIsNotAnExit — a session created while the
// daemon watches can be caught before its shell has started the agent:
// only shells in its tree. One such tick is not a crash.
func TestPollOnce_NewSessionLaunchIsNotAnExit(t *testing.T) {
	s := newPollTestServer(t)
	s.startedAt = time.Now().Add(-time.Hour)
	p := &fakePane{Pane: tmux.Pane{ID: "%1", PID: 100, Width: 120, Height: 40, Title: "codex"}}
	f := newFakeTmux()
	f.addSession(tmux.Session{Name: "c-new", Path: "/tmp", Agent: "codex", Created: time.Now()}, p)
	f.wire(s)
	fp := &fakeProcs{}
	fp.set("100 1 Ss+ zsh") // the chain's shell, the agent not yet forked
	fp.wire(s)
	for i := range 5 {
		pollNTimes(s, 1)
		if st := s.seen["c-new"].state; st == agent.StateError {
			t.Fatalf("tick %d: a launch caught between processes read as a crash", i+1)
		}
		f.update(func() { p.body = codexPane("") })
		fp.set(running("codex")...)
	}
}

// TestPollOnce_RelaunchAfterExitIsAStartup — Codex crashed to the
// fallback shell (error), and the user started it again there. Its
// startup settling at the caret notified as a turn that "finished": the
// relaunch read as the agent working. Something starting again in a
// dead agent's pane is a startup, like an agent starting in a shell's.
func TestPollOnce_RelaunchAfterExitIsAStartup(t *testing.T) {
	s := newPollTestServer(t)
	pushes := countPushes(t, s)
	bells := countBells(s)
	idle := codexPane("> fix it\n\n• Done.")
	f, p, fp := exitSession(t, s, "codex", idle, "codex", "codex")
	f.update(func() { p.body = idle + crashTail })
	fp.set(fellBack...)
	pollNTimes(s, 4)
	tr := s.seen["c-exit"]
	if tr.state != agent.StateError {
		t.Fatalf("setup: crash: state = %s, want error", tr.state)
	}

	f.update(func() { p.body = idle + crashTail + "codex" })
	pollNTimes(s, 1) // typing `codex` into the fallback shell
	f.update(func() { p.body = "OpenAI Codex\n\n  Booting MCP servers…" })
	fp.set("100 1 Ss zsh", "102 100 S zsh", "103 102 S+ codex")
	pollNTimes(s, 1) // its startup screen
	f.update(func() { p.body = codexPane("") })
	pollNTimes(s, 4) // at its caret
	if tr.state != agent.StateIdle {
		t.Fatalf("state = %s after the relaunch, want idle at the caret", tr.state)
	}
	if got := pushes(); got != 0 || *bells != 0 {
		t.Errorf("the relaunch notified: pushes=%d bells=%d, want 0", got, *bells)
	}

	codexTurn(s, f, p, "and the test") // its first real turn does
	if got := pushes(); got != 1 {
		t.Errorf("the first turn after the relaunch: pushes=%d, want 1", got)
	}
}

// TestPollOnce_JoinedCrashSettlesWithoutWaitingOutItsSpinner — a daemon
// restarted next to an agent that had crashed mid-turn finds the dead
// agent's spinner title still set. While a spinner title may be live the
// daemon doesn't take a session it joined for settled (spinnerPending),
// so the session stayed joined — nothing it did notified — for the
// spinner's whole stale window. An agent known to have exited has no
// live spinner: it settles after an idle window, like any other pane.
func TestPollOnce_JoinedCrashSettlesWithoutWaitingOutItsSpinner(t *testing.T) {
	s := newPollTestServer(t)
	s.startedAt = time.Now()
	p := &fakePane{Pane: tmux.Pane{ID: "%1", PID: 100, Width: 120, Height: 40, Title: "⠙ codex"}, body: codexPane("> fix it\n\n• Working") + crashTail}
	f := newFakeTmux()
	f.addSession(tmux.Session{Name: "c-joined", Path: "/tmp", Agent: "codex", Created: time.Now().Add(-time.Hour)}, p)
	f.wire(s)
	fp := &fakeProcs{}
	fp.set(fellBack...)
	fp.wire(s)
	pollNTimes(s, 3) // 3 idle windows, far inside the 10s spinner window
	tr := s.seen["c-joined"]
	if tr.state != agent.StateError {
		t.Fatalf("state = %s, want error from the first look", tr.state)
	}
	if tr.joined {
		t.Error("still joined: a dead agent's spinner held off the settle")
	}
}
